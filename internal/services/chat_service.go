package services

import (
	"time"

	"nexus_scholar_go_backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SaveChat creates a new chat session or updates an existing one
func SaveChat(db *gorm.DB, userID uuid.UUID, sessionID string) error {
	chat := &models.Chat{
		UserID:    userID,
		SessionID: sessionID,
	}

	return db.Where(models.Chat{SessionID: sessionID}).Assign(chat).FirstOrCreate(chat).Error
}

// SaveMessage adds a new message to an existing chat
func SaveMessage(db *gorm.DB, sessionID, msgType, content string) error {
	var chat models.Chat
	if err := db.Where("session_id = ?", sessionID).First(&chat).Error; err != nil {
		return err
	}

	message := &models.Message{
		ChatID:    chat.ID,
		Type:      msgType,
		Content:   content,
		Timestamp: time.Now(),
	}

	return db.Create(message).Error
}

// GetChatBySessionID retrieves a chat and its messages by session ID
func GetChatBySessionID(db *gorm.DB, sessionID string) (*models.Chat, error) {
	var chat models.Chat
	result := db.Preload("Messages").Where("session_id = ?", sessionID).First(&chat)
	if result.Error != nil {
		return nil, result.Error
	}
	return &chat, nil
}

// GetChatsByUserID retrieves all chats for a given user
func GetChatsByUserID(db *gorm.DB, userID uuid.UUID) ([]models.Chat, error) {
	var chats []models.Chat
	result := db.Preload("Messages").Where("user_id = ?", userID).Find(&chats)
	if result.Error != nil {
		return nil, result.Error
	}
	return chats, nil
}

// DeleteChatBySessionID deletes a chat and its associated messages
func DeleteChatBySessionID(db *gorm.DB, sessionID string) error {
	return db.Transaction(func(tx *gorm.DB) error {
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
func GetMessagesByChatID(db *gorm.DB, chatID uint) ([]models.Message, error) {
	var messages []models.Message
	result := db.Where("chat_id = ?", chatID).Order("timestamp asc").Find(&messages)
	if result.Error != nil {
		return nil, result.Error
	}
	return messages, nil
}
