package wsocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

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

func NewHandler(researchChatService *services.ResearchChatService, upgrader websocket.Upgrader, sessionCheckInterval time.Duration) *Handler {
	return &Handler{
		researchChatService:  researchChatService,
		upgrader:             upgrader,
		sessionCheckInterval: sessionCheckInterval,
	}
}

func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request, user interface{}) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var sessionID string

	// Read the initial message to get the sessionID
	_, message, err := conn.ReadMessage()
	if err != nil {
		log.Printf("Error reading initial message: %v", err)
		return
	}

	var initialMsg Message
	if err := json.Unmarshal(message, &initialMsg); err != nil {
		log.Printf("Error unmarshaling initial message: %v", err)
		return
	}

	sessionID = initialMsg.SessionID

	// Send initial session status
	statusInfo, err := h.researchChatService.GetSessionStatus(sessionID)
	if err != nil {
		log.Printf("Error getting initial session status: %v", err)
		return
	}
	statusInfoJSON, err := json.Marshal(statusInfo)
	if err != nil {
		log.Printf("Error marshaling session status: %v", err)
		return
	}
	if err := conn.WriteJSON(Message{
		Type:      "session_status",
		Content:   string(statusInfoJSON),
		SessionID: sessionID,
	}); err != nil {
		log.Printf("Error sending initial session status: %v", err)
		return
	}

	ticker := time.NewTicker(h.sessionCheckInterval)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				status, err := h.researchChatService.GetSessionStatus(sessionID)
				if err != nil {
					log.Printf("Error getting session status: %v", err)
					continue
				}
				statusJSON, err := json.Marshal(status)
				if err != nil {
					log.Printf("Error marshaling session status: %v", err)
					continue
				}
				if err := conn.WriteJSON(Message{
					Type:      "session_status",
					Content:   string(statusJSON),
					SessionID: sessionID,
				}); err != nil {
					log.Printf("Error sending session status: %v", err)
					return
				}

				if status.Status == "expired" {
					if err := conn.WriteJSON(Message{
						Type:      "expired",
						Content:   "Your session has expired.",
						SessionID: sessionID,
					}); err != nil {
						log.Printf("Error sending expiration message: %v", err)
					}
					return
				}
			}
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}
		sessionID := msg.SessionID

		switch msg.Type {
		case "message":
			h.handleChatMessage(conn, msg, ctx)
			// Update session activity after processing any message
			if err := h.researchChatService.UpdateSessionActivity(ctx, sessionID); err != nil {
				conn.WriteJSON(Message{
					Type:      "error",
					Content:   fmt.Sprintf("Failed to update session activity: %v", err),
					SessionID: sessionID,
				})
			}
		case "terminate":
			if err := h.researchChatService.EndResearchSession(ctx, sessionID); err != nil {
				log.Printf("Error ending research session: %v", err)
			} else {
				if err := conn.WriteJSON(Message{
					Type:      "info",
					Content:   "Research session terminated successfully",
					SessionID: sessionID,
				}); err != nil {
					log.Printf("Error sending termination confirmation: %v", err)
				}
			}
		case "get_session_status":
			status, err := h.researchChatService.GetSessionStatus(sessionID)
			if err != nil {
				conn.WriteJSON(Message{
					Type:      "error",
					Content:   fmt.Sprintf("Failed to get session status: %v", err),
					SessionID: sessionID,
				})
			} else {
				statusJSON, _ := json.Marshal(status)
				conn.WriteJSON(Message{
					Type:      "session_status",
					Content:   string(statusJSON),
					SessionID: sessionID,
				})
			}
		case "extend_session":
			if err := h.researchChatService.ExtendSession(ctx, sessionID); err != nil {
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
			log.Printf("Unknown message type: %s", msg.Type)
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
				log.Printf("Error sending end message: %v", err)
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
