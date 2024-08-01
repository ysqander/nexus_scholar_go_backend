package services

import (
	"context"
	"fmt"
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
	cacheExpiration    time.Duration
}

func NewResearchChatService(
	ca ContentAggregator,
	cm CacheManager,
	cs ChatSessionManager,
	chat ChatServiceDB,
	cacheExpiration time.Duration,
) *ResearchChatService {
	return &ResearchChatService{
		contentAggregation: ca,
		cacheManagement:    cm,
		chatSession:        cs,
		chatService:        chat,
		cacheExpiration:    cacheExpiration,
	}
}

func (s *ResearchChatService) StartResearchSession(c *gin.Context, arxivIDs []string, userPDFs []string) (string, string, error) {
	user, exists := c.Get("user")
	if !exists {
		return "", "", fmt.Errorf("user not found in context")
	}
	userModel, ok := user.(*models.User)
	if !ok {
		return "", "", fmt.Errorf("invalid user type in context")
	}

	// Step 1: Aggregate content
	aggregatedContent, err := s.contentAggregation.AggregateDocuments(arxivIDs, userPDFs)
	if err != nil {
		return "", "", fmt.Errorf("failed to aggregate documents: %w", err)
	}

	// Step 2: Create cache
	cacheName, err := s.cacheManagement.CreateContentCache(c.Request.Context(), aggregatedContent)
	if err != nil {
		return "", "", fmt.Errorf("failed to create content cache: %w", err)
	}

	// Step 3: Start chat session
	sessionID, err := s.chatSession.StartChatSession(c.Request.Context(), userModel.ID, cacheName)
	if err != nil {
		// Clean up cache if session start fails
		_ = s.cacheManagement.DeleteCache(c.Request.Context(), cacheName)
		return "", "", fmt.Errorf("failed to start chat session: %w", err)
	}

	// Step 4: Save chat session to history
	if err := s.chatService.SaveChatToDB(userModel.ID, sessionID); err != nil {
		return "", "", fmt.Errorf("failed to save chat to history: %w", err)
	}

	return sessionID, cacheName, nil
}

func (s *ResearchChatService) SendMessage(ctx context.Context, sessionID, message string) (*genai.GenerateContentResponseIterator, error) {
	// Send message and get response
	responseIterator, err := s.chatSession.StreamChatMessage(ctx, sessionID, message)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// Save user message to history
	if err := s.chatService.SaveMessageToDB(sessionID, "user", message); err != nil {
		return nil, fmt.Errorf("failed to save user message: %w", err)
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
	if err := s.chatSession.TerminateSession(ctx, sessionID); err != nil {
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
