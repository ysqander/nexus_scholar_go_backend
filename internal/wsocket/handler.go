package wsocket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nexus_scholar_go_backend/internal/services"

	"github.com/google/generative-ai-go/genai"
	"github.com/gorilla/websocket"
	"google.golang.org/api/iterator"
)

type Handler struct {
	researchChatService *services.ResearchChatService
	upgrader            websocket.Upgrader
}

type Message struct {
	Type              string `json:"type"`
	Content           string `json:"content"`
	SessionID         string `json:"sessionId"`
	CachedContentName string `json:"cachedContentName,omitempty"`
}

func NewHandler(researchChatService *services.ResearchChatService, upgrader websocket.Upgrader) *Handler {
	return &Handler{
		researchChatService: researchChatService,
		upgrader:            upgrader,
	}
}

func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request, user interface{}) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// heartbeat listening mechanism
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "message":
			h.handleChatMessage(conn, msg, ctx)
		case "heartbeat":
			err := h.researchChatService.UpdateSessionHeartbeat(ctx, msg.SessionID)
			if err != nil {
				conn.WriteJSON(Message{
					Type:      "error",
					Content:   fmt.Sprintf("Failed to update session heartbeat: %v", err),
					SessionID: msg.SessionID,
				})
			}
		case "terminate":
			if err := h.researchChatService.EndResearchSession(ctx, msg.SessionID); err == nil {
				conn.WriteJSON(Message{
					Type:      "info",
					Content:   "Research session terminated successfully",
					SessionID: msg.SessionID,
				})
			}
		default:
		}
	}
}

func (h *Handler) handleChatMessage(conn *websocket.Conn, msg Message, ctx context.Context) {
	responseIterator, err := h.researchChatService.SendMessage(ctx, msg.SessionID, msg.Content)
	if err != nil {
		conn.WriteJSON(Message{
			Type:      "error",
			Content:   fmt.Sprintf("Failed to send message: %v", err),
			SessionID: msg.SessionID,
		})

		return
	}

	// Save user message
	if err := h.researchChatService.SaveMessageToDB(ctx, msg.SessionID, "user", msg.Content); err != nil {
		conn.WriteJSON(Message{
			Type:      "error",
			Content:   fmt.Sprintf("Failed to save user message: %v", err),
			SessionID: msg.SessionID,
		})
		return
	}

	var aiResponse strings.Builder

	for {
		response, err := responseIterator.Next()
		if err == iterator.Done {
			// Update the session's chat history with the AI response
			if err := h.researchChatService.SaveMessageToDB(ctx, msg.SessionID, "ai", aiResponse.String()); err != nil {
				conn.WriteJSON(Message{
					Type:      "error",
					Content:   fmt.Sprintf("Failed to save AI response to chat history: %v", err),
					SessionID: msg.SessionID,
				})
			}
			// Send end-of-message signal
			endMsg := Message{
				Type:      "ai",
				Content:   "[END]",
				SessionID: msg.SessionID,
			}
			if err := conn.WriteJSON(endMsg); err != nil {
			}
			break
		}
		if err != nil {
			conn.WriteJSON(Message{
				Type:      "error",
				Content:   fmt.Sprintf("Error getting response: %v", err),
				SessionID: msg.SessionID,
			})
			break
		}

		if len(response.Candidates) > 0 && len(response.Candidates[0].Content.Parts) > 0 {
			var content string
			switch part := response.Candidates[0].Content.Parts[0].(type) {
			case genai.Text:
				content = string(part)
			case *genai.Text:
				content = string(*part)
			default:
				continue
			}
			// Aggregating the ai response to later save in DB.
			aiResponse.WriteString(content)

			// Send the content as it is returned from the iterator
			responseMsg := Message{
				Type:      "ai",
				Content:   content,
				SessionID: msg.SessionID,
			}
			if err := conn.WriteJSON(responseMsg); err != nil {
				return
			}
		}
	}
}
