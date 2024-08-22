package services

import (
	"log"
	"nexus_scholar_go_backend/internal/models"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

// ChatService defines the interface for chat-related operations
type ChatServiceDB interface {
	SaveChatToDB(userID uuid.UUID, sessionID string) error
	SaveMessageToDB(sessionID, msgType, content string) error
	GetChatBySessionIDFromDB(sessionID string) (*models.Chat, error)
	GetChatsByUserIDFromDB(userID uuid.UUID) ([]models.Chat, error)
	DeleteChatBySessionIDFromDB(sessionID string) error
	GetMessagesByChatIDFromDB(chatID uint) ([]models.Message, error)
	UpdateChatMetrics(sessionID string, chatDuration float64, tokenCountUsed int32, priceTier string, tokenHoursUsed float64, terminationTime time.Time) error
	GetHistoricalChatMetricsByUserID(userID uuid.UUID, log zerolog.Logger) ([]models.Chat, error)
}

// DefaultChatService implements ChatService
type DefaultChatService struct {
	db *gorm.DB
}

// NewChatService creates a new DefaultChatService
func NewChatServiceDB(db *gorm.DB) ChatServiceDB {
	return &DefaultChatService{db: db}
}

// SaveChat creates a new chat session or updates an existing one
func (s *DefaultChatService) SaveChatToDB(userID uuid.UUID, sessionID string) error {
	chat := &models.Chat{
		UserID:    userID,
		SessionID: sessionID,
	}
	result := s.db.Where(models.Chat{SessionID: sessionID}).Assign(chat).FirstOrCreate(chat)
	return result.Error
}

// SaveMessage adds a new message to an existing chat
func (s *DefaultChatService) SaveMessageToDB(sessionID, msgType, content string) error {
	var chat models.Chat
	if err := s.db.Where("session_id = ?", sessionID).First(&chat).Error; err != nil {
		return err
	}
	message := &models.Message{
		ChatID:    chat.ID,
		Type:      msgType,
		Content:   content,
		Timestamp: time.Now(),
	}
	return s.db.Create(message).Error
}

// GetChatBySessionID retrieves a chat and its messages by session ID
func (s *DefaultChatService) GetChatBySessionIDFromDB(sessionID string) (*models.Chat, error) {
	var chat models.Chat
	result := s.db.Preload("Messages").Where("session_id = ?", sessionID).First(&chat)
	if result.Error != nil {
		return nil, result.Error
	}
	return &chat, nil
}

// GetChatsByUserID retrieves all chats for a given user
func (s *DefaultChatService) GetChatsByUserIDFromDB(userID uuid.UUID) ([]models.Chat, error) {
	var chats []models.Chat
	result := s.db.Preload("Messages").Where("user_id = ?", userID).Find(&chats)
	if result.Error != nil {
		return nil, result.Error
	}
	return chats, nil
}

// DeleteChatBySessionID deletes a chat and its associated messages
func (s *DefaultChatService) DeleteChatBySessionIDFromDB(sessionID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var chat models.Chat
		if err := tx.Where("session_id = ?", sessionID).First(&chat).Error; err != nil {
			return err
		}
		// Delete associated messages
		if err := tx.Where("chat_id = ?", chat.ID).Delete(&models.Message{}).Error; err != nil {
			return err
		}
		// Delete the chat
		return tx.Delete(&chat).Error
	})
}

// GetMessagesByChatID retrieves all messages for a given chat
func (s *DefaultChatService) GetMessagesByChatIDFromDB(chatID uint) ([]models.Message, error) {
	var messages []models.Message
	result := s.db.Where("chat_id = ?", chatID).Order("timestamp asc").Find(&messages)
	if result.Error != nil {
		return nil, result.Error
	}
	return messages, nil
}

func (s *DefaultChatService) UpdateChatMetrics(sessionID string, chatDuration float64, tokenCountUsed int32, priceTier string, tokenHoursUsed float64, terminationTime time.Time) error {
	result := s.db.Model(&models.Chat{}).
		Where("session_id = ?", sessionID).
		Updates(map[string]interface{}{
			"chat_duration":    chatDuration,
			"token_count_used": tokenCountUsed,
			"price_tier":       priceTier,
			"token_hours_used": tokenHoursUsed,
			"termination_time": terminationTime,
		})

	if result.Error != nil {
		return result.Error
	}

	// Log chat duration
	log.Printf("Chat duration updated for session %s: %.2f seconds", sessionID, chatDuration)

	return nil
}

// Implement the new method in DefaultChatService
func (s *DefaultChatService) GetHistoricalChatMetricsByUserID(userID uuid.UUID, log zerolog.Logger) ([]models.Chat, error) {
	log.Info().Str("userID", userID.String()).Msg("Retrieving historical chat metrics")
	var chats []models.Chat
	result := s.db.Where("user_id = ?", userID).
		Select("session_id, token_count_used, token_hours_used, chat_duration, price_tier, termination_time").
		Find(&chats)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			log.Info().Str("userID", userID.String()).Msg("No historical chat metrics found")
			return []models.Chat{}, nil // Return an empty slice instead of an error
		}
		log.Error().Err(result.Error).Str("userID", userID.String()).Msg("Failed to retrieve historical chat metrics")
		return nil, result.Error
	}
	log.Info().Str("userID", userID.String()).Int("chatCount", len(chats)).Msg("Successfully retrieved historical chat metrics")
	return chats, nil
}
