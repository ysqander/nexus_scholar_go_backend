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
	"github.com/google/uuid"
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
			if err := h.cacheService.SaveChatHistoryToDB(msg.SessionID, userModel.ID); err != nil {
				log.Printf("Error saving chat history: %v", err)
				// Send an error message to the client
				errorMsg := Message{
					Type:      "error",
					Content:   "Failed to save chat history",
					SessionID: msg.SessionID,
				}
				if err := conn.WriteJSON(errorMsg); err != nil {
					log.Printf("Error sending error message: %v", err)
				}
			} else {
				// Send a success message to the client
				successMsg := Message{
					Type:      "info",
					Content:   "Chat history saved successfully",
					SessionID: msg.SessionID,
				}
				if err := conn.WriteJSON(successMsg); err != nil {
					log.Printf("Error sending success message: %v", err)
				}
			}
			h.cacheService.TerminateSession(ctx, msg.SessionID)
			return
		default:
			log.Println("Unknown message type:", msg.Type)
		}
	}
}

func (h *Handler) handleChatMessage(conn *websocket.Conn, msg Message, ctx context.Context, userID uuid.UUID) {
	log.Printf("Handling chat message for session: %s, user: %d", msg.SessionID, userID)

	responseIterator, err := h.cacheService.StreamChatMessage(ctx, msg.SessionID, msg.Content)
	if err != nil {
		log.Println("Error getting stream:", err)
		return
	}

	var aiResponse strings.Builder

	for {
		response, err := responseIterator.Next()
		if err == iterator.Done {
			log.Println("Stream iterator done")

			// Update the session's chat history with the AI response
			h.cacheService.UpdateSessionChatHistory(msg.SessionID, "ai", aiResponse.String())
			log.Printf("Updated session chat history for session: %s with AI response", msg.SessionID)

			// Send end-of-message signal
			endMsg := Message{
				Type:      "ai",
				Content:   "[END]",
				SessionID: msg.SessionID,
			}
			if err := conn.WriteJSON(endMsg); err != nil {
				log.Println("Error writing end message:", err)
			} else {
				log.Printf("Sent end-of-message signal for session: %s", msg.SessionID)
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
			log.Printf("Aggregated AI response content for session: %s", msg.SessionID)

			// Send the content as it is returned from the iterator
			responseMsg := Message{
				Type:      "ai",
				Content:   content,
				SessionID: msg.SessionID,
			}
			if err := conn.WriteJSON(responseMsg); err != nil {
				log.Println("Error writing response:", err)
				return
			} else {
				log.Printf("Sent AI response content for session: %s", msg.SessionID)
			}
		}
	}
}
