package services

import (
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"
	"time"
)

func CreateChat(userID uint, sessionID, chatType, content string) (*models.Chat, error) {
	chat := &models.Chat{
		UserID:    userID,
		SessionID: sessionID,
		Type:      chatType,
		Content:   content,
		Timestamp: time.Now().Unix(),
	}

	result := database.DB.Create(chat)
	if result.Error != nil {
		return nil, result.Error
	}

	return chat, nil
}

func GetChatsBySessionID(sessionID string) ([]models.Chat, error) {
	var chats []models.Chat
	result := database.DB.Where("session_id = ?", sessionID).Order("timestamp asc").Find(&chats)
	if result.Error != nil {
		return nil, result.Error
	}
	return chats, nil
}

func GetChatsByUserID(userID uint) ([]models.Chat, error) {
	var chats []models.Chat
	result := database.DB.Where("user_id = ?", userID).Order("timestamp desc").Find(&chats)
	if result.Error != nil {
		return nil, result.Error
	}
	return chats, nil
}

func DeleteChatsBySessionID(sessionID string) error {
	result := database.DB.Where("session_id = ?", sessionID).Delete(&models.Chat{})
	return result.Error
}
