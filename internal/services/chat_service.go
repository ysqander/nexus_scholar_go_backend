package services

import (
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"
)

func SaveChatHistory(userID uint, sessionID string, history []byte) error {
	chat := &models.Chat{
		UserID:    userID,
		SessionID: sessionID,
		History:   history,
	}

	result := database.DB.Create(chat)
	return result.Error
}

func GetChatBySessionID(sessionID string) (*models.Chat, error) {
	var chat models.Chat
	result := database.DB.Where("session_id = ?", sessionID).First(&chat)
	if result.Error != nil {
		return nil, result.Error
	}
	return &chat, nil
}

func GetChatsByUserID(userID uint) ([]models.Chat, error) {
	var chats []models.Chat
	result := database.DB.Where("user_id = ?", userID).Find(&chats)
	if result.Error != nil {
		return nil, result.Error
	}
	return chats, nil
}

func DeleteChatBySessionID(sessionID string) error {
	result := database.DB.Where("session_id = ?", sessionID).Delete(&models.Chat{})
	return result.Error
}
