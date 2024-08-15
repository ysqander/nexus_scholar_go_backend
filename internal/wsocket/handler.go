package wsocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"nexus_scholar_go_backend/internal/broker"
	"nexus_scholar_go_backend/internal/models"
	"nexus_scholar_go_backend/internal/services"

	"github.com/google/generative-ai-go/genai"
	"github.com/gorilla/websocket"
	"google.golang.org/api/iterator"
)

type Handler struct {
	researchChatService  *services.ResearchChatService
	upgrader             websocket.Upgrader
	sessionCheckInterval time.Duration
}

type Message struct {
	Type              string `json:"type"`
	Content           string `json:"content"`
	SessionID         string `json:"sessionId"`
	CachedContentName string `json:"cachedContentName,omitempty"`
}

func NewHandler(researchChatService *services.ResearchChatService, upgrader websocket.Upgrader, sessionCheckInterval time.Duration, sessionMemoryTimeout time.Duration) *Handler {
	log.Println("DEBUG: Creating new Handler")
	return &Handler{
		researchChatService:  researchChatService,
		upgrader:             upgrader,
		sessionCheckInterval: sessionCheckInterval,
	}
}

func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request, user interface{}, messageBroker *broker.Broker) {
	log.Println("DEBUG: Handling new WebSocket connection")
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		log.Println("DEBUG: No sessionId provided in WebSocket connection")
		http.Error(w, "No sessionId provided", http.StatusBadRequest)
		return
	}
	log.Printf("DEBUG: Received sessionId: %s", sessionID)
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("DEBUG: Error upgrading connection: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	ticker := time.NewTicker(h.sessionCheckInterval)
	defer ticker.Stop()

	userID := user.(*models.User).ID.String()
	creditUpdateChan := messageBroker.Subscribe("credit_update_" + userID)
	defer messageBroker.Unsubscribe("credit_update_"+userID, creditUpdateChan)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Println("DEBUG: Context done, exiting goroutine")
				return
			case msg := <-creditUpdateChan:
				if err := conn.WriteJSON(Message{
					Type:      "credit_update",
					Content:   msg.(string),
					SessionID: sessionID,
				}); err != nil {
					log.Printf("Error sending credit update: %v", err)
				}
			case <-ticker.C:
				status, err := h.researchChatService.GetSessionStatus(sessionID)
				if err != nil {
					log.Printf("DEBUG: Error getting session status: %v", err)
					continue
				}
				isLowCredit, remainingCredit, err := h.researchChatService.CheckCreditStatus(sessionID)
				if err != nil {
					log.Printf("DEBUG: Error getting remaining credit: %v", err)
					continue
				}
				if isLowCredit {
					log.Printf("DEBUG: Session low credit")
					if err := conn.WriteJSON(Message{
						Type:      "credit_warning",
						Content:   fmt.Sprintf(`{"remainingCredit": %.6f}`, remainingCredit),
						SessionID: sessionID,
					}); err != nil {
						log.Printf("DEBUG: Error sending low credit message: %v", err)
						return
					}
				}
				statusJSON, err := json.Marshal(status)
				if err != nil {
					log.Printf("DEBUG: Error marshaling session status: %v", err)
					continue
				}
				if err := conn.WriteJSON(Message{
					Type:      "session_status",
					Content:   string(statusJSON),
					SessionID: sessionID,
				}); err != nil {
					log.Printf("DEBUG: Error sending session status: %v", err)
					return
				}

				if status.Status == "expired" {
					log.Println("DEBUG: Session expired")
					if err := conn.WriteJSON(Message{
						Type:      "expired",
						Content:   "Your session has expired.",
						SessionID: sessionID,
					}); err != nil {
						log.Printf("DEBUG: Error sending expiration message: %v", err)
					}
					return
				}
			}
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("DEBUG: Error reading message: %v", err)
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("DEBUG: Error unmarshaling message: %v", err)
			continue
		}
		sessionID := msg.SessionID
		log.Printf("DEBUG: Received message of type: %s for session: %s", msg.Type, sessionID)

		switch msg.Type {
		case "message":
			log.Println("DEBUG: Handling chat message")
			h.handleChatMessage(conn, msg, ctx)
			// Update session activity after processing any message
			if err := h.researchChatService.UpdateSessionActivity(ctx, sessionID); err != nil {
				log.Printf("DEBUG: Failed to update session activity: %v", err)
				conn.WriteJSON(Message{
					Type:      "error",
					Content:   fmt.Sprintf("Failed to update session activity: %v", err),
					SessionID: sessionID,
				})
			}
		case "terminate":
			log.Println("DEBUG: Terminating session")
			if err := h.researchChatService.EndResearchSession(ctx, sessionID); err != nil {
				log.Printf("DEBUG: Error ending research session: %v", err)
			} else {
				if err := conn.WriteJSON(Message{
					Type:      "info",
					Content:   "Research session terminated successfully",
					SessionID: sessionID,
				}); err != nil {
					log.Printf("DEBUG: Error sending termination confirmation: %v", err)
				}
			}
		case "get_session_status":
			log.Println("DEBUG: Getting session status")
			h.sendSessionStatus(conn, sessionID)
		case "extend_session":
			log.Println("DEBUG: Extending session")
			if err := h.researchChatService.ExtendSession(ctx, sessionID); err != nil {
				log.Printf("DEBUG: Failed to extend session: %v", err)
				conn.WriteJSON(Message{
					Type:      "error",
					Content:   fmt.Sprintf("Failed to extend session: %v", err),
					SessionID: sessionID,
				})
			} else {
				conn.WriteJSON(Message{
					Type:      "info",
					Content:   "Session extended successfully",
					SessionID: sessionID,
				})
			}
		default:
			log.Printf("DEBUG: Unknown message type: %s", msg.Type)
		}
	}
}

func (h *Handler) handleChatMessage(conn *websocket.Conn, msg Message, ctx context.Context) {
	log.Printf("DEBUG: Handling chat message for session: %s", msg.SessionID)
	responseIterator, err := h.researchChatService.SendMessage(ctx, msg.SessionID, msg.Content)
	if err != nil {
		log.Printf("DEBUG: Failed to send message: %v", err)
		conn.WriteJSON(Message{
			Type:      "error",
			Content:   fmt.Sprintf("Failed to send message: %v", err),
			SessionID: msg.SessionID,
		})

		return
	}

	// Save user message
	if err := h.researchChatService.SaveMessageToDB(ctx, msg.SessionID, "user", msg.Content); err != nil {
		log.Printf("DEBUG: Failed to save user message: %v", err)
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
			log.Println("DEBUG: AI response complete")
			// Update the session's chat history with the AI response
			if err := h.researchChatService.SaveMessageToDB(ctx, msg.SessionID, "ai", aiResponse.String()); err != nil {
				log.Printf("DEBUG: Failed to save AI response to chat history: %v", err)
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
				log.Printf("DEBUG: Error sending end message: %v", err)
			}
			break
		}
		if err != nil {
			log.Printf("DEBUG: Error getting response: %v", err)
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
				log.Println("DEBUG: Unexpected content type in response")
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
				log.Printf("DEBUG: Error sending AI response: %v", err)
				return
			}
		}
	}
}

func (h *Handler) sendSessionStatus(conn *websocket.Conn, sessionID string) error {
	statusInfo, err := h.researchChatService.GetSessionStatus(sessionID)
	if err != nil {
		log.Printf("DEBUG: Error getting session status: %v", err)
		return conn.WriteJSON(Message{Type: "error", Content: "Failed to get session status"})
	}
	statusInfoJSON, _ := json.Marshal(statusInfo)
	return conn.WriteJSON(Message{
		Type:      "session_status",
		Content:   string(statusInfoJSON),
		SessionID: sessionID,
	})
}
