package wsocket

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"nexus_scholar_go_backend/internal/services"

	"github.com/google/generative-ai-go/genai"
	"github.com/gorilla/websocket"
	"google.golang.org/api/iterator"
)

type Handler struct {
	cacheService *services.CacheService
	upgrader     websocket.Upgrader
}

type Message struct {
	Type              string `json:"type"`
	Content           string `json:"content"`
	SessionID         string `json:"sessionId"`
	CachedContentName string `json:"cachedContentName,omitempty"`
}

func NewHandler(cacheService *services.CacheService, upgrader websocket.Upgrader) *Handler {
	return &Handler{
		cacheService: cacheService,
		upgrader:     upgrader,
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
			h.cacheService.UpdateSessionHeartbeat(msg.SessionID)
		case "terminate":
			if err := h.cacheService.TerminateSession(ctx, msg.SessionID); err == nil {
				conn.WriteJSON(Message{
					Type:      "info",
					Content:   "Session terminated successfully",
					SessionID: msg.SessionID,
				})
			}
		default:
		}
	}
}

func (h *Handler) handleChatMessage(conn *websocket.Conn, msg Message, ctx context.Context) {
	responseIterator, err := h.cacheService.StreamChatMessage(ctx, msg.SessionID, msg.Content)
	if err != nil {
		return
	}

	// Save user message
	if err := h.cacheService.UpdateSessionChatHistory(msg.SessionID, "user", msg.Content); err != nil {
	}

	var aiResponse strings.Builder

	for {
		response, err := responseIterator.Next()
		if err == iterator.Done {
			// Update the session's chat history with the AI response
			h.cacheService.UpdateSessionChatHistory(msg.SessionID, "ai", aiResponse.String())

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
