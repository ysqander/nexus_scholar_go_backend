package wsocket

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"nexus_scholar_go_backend/internal/models"
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
		log.Println("Failed to upgrade connection:", err)
		return
	}
	defer conn.Close()

	userModel, ok := user.(*models.User)
	if !ok {
		log.Println("Failed to cast user to *models.User")
		return
	}

	// You can now use the authenticated user information
	log.Printf("Authenticated user connected: %v", userModel.ID)

	// heartbeat listening mechanism
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("Error reading message:", err)
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Println("Error unmarshalling message:", err)
			continue
		}

		switch msg.Type {
		case "message":
			h.handleChatMessage(conn, msg, ctx, userModel.ID)
		case "heartbeat":
			log.Printf("Received heartbeat for session: %s", msg.SessionID)
			h.cacheService.UpdateSessionHeartbeat(msg.SessionID)
		case "terminate":
			log.Printf("Terminating session: %s", msg.SessionID)
			h.cacheService.TerminateSession(ctx, msg.SessionID)
			return
		default:
			log.Println("Unknown message type:", msg.Type)
		}
	}
}

func (h *Handler) handleChatMessage(conn *websocket.Conn, msg Message, ctx context.Context, userID uint) {
	// Persist user message
	_, err := services.CreateChat(userID, msg.SessionID, "user", msg.Content)
	if err != nil {
		log.Println("Error persisting user chat:", err)
	}

	responseIterator, err := h.cacheService.StreamChatMessage(ctx, msg.SessionID, msg.Content)
	if err != nil {
		log.Println("Error getting stream:", err)
		return
	}

	var aiResponse strings.Builder

	for {
		response, err := responseIterator.Next()
		if err == iterator.Done {
			// Persist complete AI response
			_, err := services.CreateChat(userID, msg.SessionID, "ai", aiResponse.String())
			if err != nil {
				log.Println("Error persisting AI chat:", err)
			}

			// Update the session's chat history with the AI response
			h.cacheService.UpdateSessionChatHistory(msg.SessionID, "ai", aiResponse.String())

			// Send end-of-message signal
			endMsg := Message{
				Type:      "ai",
				Content:   "[END]",
				SessionID: msg.SessionID,
			}
			if err := conn.WriteJSON(endMsg); err != nil {
				log.Println("Error writing end message:", err)
			}
			break
		}
		if err != nil {
			log.Println("Error streaming response:", err)
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
				log.Printf("Unexpected content type: %T", part)
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
				log.Println("Error writing response:", err)
				return
			}
		}
	}
}
