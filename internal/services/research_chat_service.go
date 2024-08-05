package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nexus_scholar_go_backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
)

type ResearchChatService struct {
	contentAggregation ContentAggregator
	cacheManagement    CacheManager
	chatSession        ChatSessionManager
	chatService        ChatServiceDB
	cloudStorage       CloudStorageManager
	cacheExpiration    time.Duration
	bucketName         string
}

func NewResearchChatService(
	ca ContentAggregator,
	cm CacheManager,
	cs ChatSessionManager,
	chat ChatServiceDB,
	cacheExpiration time.Duration,
	cloudStorage CloudStorageManager,
	bucketName string,
) *ResearchChatService {
	return &ResearchChatService{
		contentAggregation: ca,
		cacheManagement:    cm,
		chatSession:        cs,
		chatService:        chat,
		cacheExpiration:    cacheExpiration,
		cloudStorage:       cloudStorage,
		bucketName:         bucketName,
	}
}

func (s *ResearchChatService) StartResearchSession(c *gin.Context, arxivIDs []string, userPDFs []string, priceTier string) (string, string, error) {
	user, exists := c.Get("user")
	if !exists {
		return "", "", fmt.Errorf("user not found in context")
	}
	userModel, ok := user.(*models.User)
	if !ok {
		return "", "", fmt.Errorf("invalid user type in context")
	}

	// Aggregate content
	aggregatedContent, err := s.contentAggregation.AggregateDocuments(arxivIDs, userPDFs)
	if err != nil {
		return "", "", fmt.Errorf("failed to aggregate documents: %w", err)
	}

	// Save raw text cache to Google Cloud Storage
	sessionID := uuid.New().String() // Generate a unique session ID
	err = s.SaveRawTextCache(c.Request.Context(), sessionID, aggregatedContent)
	if err != nil {
		return "", "", fmt.Errorf("failed to save raw text cache: %w", err)
	}

	// Create cache
	cacheName, err := s.cacheManagement.CreateContentCache(c.Request.Context(), userModel.ID, sessionID, priceTier, aggregatedContent)
	if err != nil {
		return "", "", fmt.Errorf("failed to create content cache: %w", err)
	}

	// Start chat session
	err = s.chatSession.StartChatSession(c.Request.Context(), userModel.ID, cacheName, sessionID)
	if err != nil {
		// Clean up cache if session start fails
		_ = s.cacheManagement.DeleteCache(c.Request.Context(), userModel.ID, sessionID, cacheName)

		// cleanup the storage file
		fileName := fmt.Sprintf("raw_cache_%s.txt", sessionID)
		_ = s.cloudStorage.DeleteFile(c.Request.Context(), s.bucketName, fileName)

		return "", "", fmt.Errorf("failed to start chat session: %w", err)
	}

	// Save chat session to history
	if err := s.chatService.SaveChatToDB(userModel.ID, sessionID); err != nil {
		return "", "", fmt.Errorf("failed to save chat to history: %w", err)
	}

	return sessionID, cacheName, nil
}
func (s *ResearchChatService) SaveRawTextCache(ctx context.Context, sessionID string, content string) error {
	objectName := fmt.Sprintf("raw_cache_%s.txt", sessionID)
	return s.cloudStorage.UploadFile(ctx, s.bucketName, objectName, strings.NewReader(content))
}

func (s *ResearchChatService) GetRawTextCache(ctx context.Context, sessionID string) (string, error) {
	objectName := fmt.Sprintf("raw_cache_%s.txt", sessionID)
	content, err := s.cloudStorage.DownloadFile(ctx, s.bucketName, objectName)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func (s *ResearchChatService) SendMessage(ctx context.Context, sessionID, message string) (*genai.GenerateContentResponseIterator, error) {
	// Send message and get response
	responseIterator, err := s.chatSession.StreamChatMessage(ctx, sessionID, message)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	return responseIterator, nil
}

func (s *ResearchChatService) SaveAIResponse(sessionID, response string) error {
	// Save AI response to history
	if err := s.chatService.SaveMessageToDB(sessionID, "ai", response); err != nil {
		return fmt.Errorf("failed to save AI response: %w", err)
	}

	return nil
}

func (s *ResearchChatService) EndResearchSession(ctx context.Context, sessionID string) error {
	// Assumes the only caller of this method is an api endpoint whena user Terminates a chat
	var reason TerminationReason = UserInitiated
	if err := s.chatSession.TerminateSession(ctx, sessionID, reason); err != nil {
		return fmt.Errorf("failed to terminate chat session: %w", err)
	}
	return nil
}

func (s *ResearchChatService) GetUserChatHistory(userID uuid.UUID) ([]models.Chat, error) {
	return s.chatService.GetChatsByUserIDFromDB(userID)
}

func (s *ResearchChatService) UpdateSessionHeartbeat(ctx context.Context, sessionID string) error {
	return s.chatSession.UpdateSessionHeartbeat(ctx, sessionID)
}

func (s *ResearchChatService) SaveMessageToDB(ctx context.Context, sessionID, msgType, content string) error {
	return s.chatService.SaveMessageToDB(sessionID, msgType, content)
}
