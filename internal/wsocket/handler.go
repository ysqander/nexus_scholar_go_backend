package wsocket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nexus_scholar_go_backend/internal/models"
	"nexus_scholar_go_backend/internal/services"
	"nexus_scholar_go_backend/internal/utils/broker"

	"github.com/google/generative-ai-go/genai"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"google.golang.org/api/iterator"
)

type Handler struct {
	researchChatService  *services.ResearchChatService
	upgrader             websocket.Upgrader
	sessionCheckInterval time.Duration
	log                  zerolog.Logger
}

type Message struct {
	Type              string `json:"type"`
	Content           string `json:"content"`
	SessionID         string `json:"sessionId"`
	CachedContentName string `json:"cachedContentName,omitempty"`
}

func NewHandler(researchChatService *services.ResearchChatService, upgrader websocket.Upgrader, sessionCheckInterval time.Duration, sessionMemoryTimeout time.Duration, log zerolog.Logger) *Handler {
	log.Info().Msg("Creating new Handler")
	return &Handler{
		researchChatService:  researchChatService,
		upgrader:             upgrader,
		sessionCheckInterval: sessionCheckInterval,
		log:                  log,
	}
}

func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request, user interface{}, messageBroker *broker.Broker) {
	h.log.Info().Msg("Handling new WebSocket connection")
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		h.log.Warn().Msg("No sessionId provided in WebSocket connection")
		http.Error(w, "No sessionId provided", http.StatusBadRequest)
		return
	}
	h.log.Info().Str("sessionId", sessionID).Msg("Received sessionId")
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Error().Err(err).Msg("Error upgrading connection")
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

	isTerminated := false

	go func() {
		for {
			select {
			case <-ctx.Done():
				h.log.Info().Msg("Context done, exiting goroutine")
				return
			case msg, ok := <-creditUpdateChan:
				if !ok {
					h.log.Info().Msg("Credit update channel closed, exiting goroutine")
					return
				}
				if msgStr, ok := msg.(string); ok {
					if err := conn.WriteJSON(Message{
						Type:      "credit_update",
						Content:   msgStr,
						SessionID: sessionID,
					}); err != nil {
						h.log.Error().Err(err).Msg("Error sending credit update")
					}
				} else {
					h.log.Warn().Interface("msg", msg).Msg("Received non-string message on credit update channel")
				}
			case <-ticker.C:
				if isTerminated {
					return
				}
				status, err := h.researchChatService.GetSessionStatus(sessionID)
				if err != nil {
					h.log.Error().Err(err).Msg("Error getting session status")
					continue
				}
				isLowCredit, _, remainingCredit, err := h.researchChatService.CheckCreditStatus(sessionID)
				if err != nil {
					h.log.Error().Err(err).Msg("Error getting remaining credit")
					continue
				}
				if isLowCredit {
					h.log.Info().Msg("Session low credit")
					if err := conn.WriteJSON(Message{
						Type:      "credit_warning",
						Content:   fmt.Sprintf(`{"remainingCredit": %.6f}`, remainingCredit),
						SessionID: sessionID,
					}); err != nil {
						h.log.Error().Err(err).Msg("Error sending low credit message")
						return
					}
				}
				statusJSON, err := json.Marshal(status)
				if err != nil {
					h.log.Error().Err(err).Msg("Error marshaling session status")
					continue
				}
				if err := conn.WriteJSON(Message{
					Type:      "session_status",
					Content:   string(statusJSON),
					SessionID: sessionID,
				}); err != nil {
					h.log.Error().Err(err).Msg("Error sending session status")
					return
				}

				if status.Status == "expired" {
					h.log.Info().Msg("Session expired")
					if err := conn.WriteJSON(Message{
						Type:      "expired",
						Content:   "Your session has expired.",
						SessionID: sessionID,
					}); err != nil {
						h.log.Error().Err(err).Msg("Error sending expiration message")
					}
					return
				}
			}
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			h.log.Error().Err(err).Msg("Error reading message")
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			h.log.Error().Err(err).Msg("Error unmarshaling message")
			continue
		}
		sessionID := msg.SessionID
		h.log.Info().Str("type", msg.Type).Str("sessionId", sessionID).Msg("Received message")

		switch msg.Type {
		case "message":
			h.log.Info().Msg("Handling chat message")
			h.handleChatMessage(conn, msg, ctx)
			// Update session activity after processing any message
			if err := h.researchChatService.UpdateSessionActivity(ctx, sessionID); err != nil {
				h.log.Error().Err(err).Msg("Failed to update session activity")
				conn.WriteJSON(Message{
					Type:      "error",
					Content:   fmt.Sprintf("Failed to update session activity: %v", err),
					SessionID: sessionID,
				})
			}
		case "terminate":
			h.log.Info().Msg("Terminating session")
			if err := h.researchChatService.EndResearchSession(ctx, sessionID); err != nil {
				h.log.Error().Err(err).Msg("Error ending research session")
			} else {
				if err := conn.WriteJSON(Message{
					Type:      "info",
					Content:   "Research session terminated successfully",
					SessionID: sessionID,
				}); err != nil {
					h.log.Error().Err(err).Msg("Error sending termination confirmation")
				}
			}
			isTerminated = true
			time.Sleep(500 * time.Millisecond)
			cancel()
			return
		case "get_session_status":
			h.log.Info().Msg("Getting session status")
			h.sendSessionStatus(conn, sessionID)
		case "extend_session":
			h.log.Info().Msg("Extending session")
			if err := h.researchChatService.ExtendSession(ctx, sessionID); err != nil {
				h.log.Error().Err(err).Msg("Failed to extend session")
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
			h.log.Warn().Str("type", msg.Type).Msg("Unknown message type")
		}
	}
}

func (h *Handler) handleChatMessage(conn *websocket.Conn, msg Message, ctx context.Context) {
	h.log.Info().Str("sessionId", msg.SessionID).Msg("Handling chat message")
	responseIterator, err := h.researchChatService.SendMessage(ctx, msg.SessionID, msg.Content)
	if err != nil {
		h.log.Error().Err(err).Msg("Failed to send message")
		conn.WriteJSON(Message{
			Type:      "error",
			Content:   fmt.Sprintf("Failed to send message: %v", err),
			SessionID: msg.SessionID,
		})
		return
	}

	// Save user message
	if err := h.researchChatService.SaveMessageToDB(ctx, msg.SessionID, "user", msg.Content); err != nil {
		h.log.Error().Err(err).Msg("Failed to save user message")
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
			h.log.Info().Msg("AI response complete")
			// Update the session's chat history with the AI response
			if err := h.researchChatService.SaveMessageToDB(ctx, msg.SessionID, "ai", aiResponse.String()); err != nil {
				h.log.Error().Err(err).Msg("Failed to save AI response to chat history")
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
				h.log.Error().Err(err).Msg("Error sending end message")
			}
			break
		}
		if err != nil {
			h.log.Error().Err(err).Msg("Error getting response")
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
				h.log.Warn().Msg("Unexpected content type in response")
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
				h.log.Error().Err(err).Msg("Error sending AI response")
				return
			}
		}
	}
}

func (h *Handler) sendSessionStatus(conn *websocket.Conn, sessionID string) error {
	statusInfo, err := h.researchChatService.GetSessionStatus(sessionID)
	if err != nil {
		h.log.Error().Err(err).Msg("Error getting session status")
		return conn.WriteJSON(Message{Type: "error", Content: "Failed to get session status"})
	}
	statusInfoJSON, _ := json.Marshal(statusInfo)
	return conn.WriteJSON(Message{
		Type:      "session_status",
		Content:   string(statusInfoJSON),
		SessionID: sessionID,
	})
}
