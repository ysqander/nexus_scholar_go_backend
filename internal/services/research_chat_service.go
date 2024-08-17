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
	"github.com/rs/zerolog"
)

type ResearchChatService struct {
	contentAggregation ContentAggregator
	cacheManagement    CacheManager
	chatSession        ChatSessionManager
	chatService        ChatServiceDB
	cacheServiceDB     CacheServiceDB
	cloudStorage       CloudStorageManager
	cacheExpiration    time.Duration
	bucketName         string
	logger             zerolog.Logger
}

func NewResearchChatService(
	ca ContentAggregator,
	cm CacheManager,
	cs ChatSessionManager,
	chat ChatServiceDB,
	cacheServiceDB CacheServiceDB,
	cacheExpiration time.Duration,
	cloudStorage CloudStorageManager,
	bucketName string,
	logger zerolog.Logger,
) *ResearchChatService {
	return &ResearchChatService{
		contentAggregation: ca,
		cacheManagement:    cm,
		chatSession:        cs,
		chatService:        chat,
		cacheServiceDB:     cacheServiceDB,
		cacheExpiration:    cacheExpiration,
		cloudStorage:       cloudStorage,
		bucketName:         bucketName,
		logger:             logger,
	}
}

func (s *ResearchChatService) StartResearchSession(c *gin.Context, arxivIDs []string, userPDFs []string, priceTier string) (string, string, error) {
	s.logger.Info().Msg("Starting research session")
	user, exists := c.Get("user")
	if !exists {
		s.logger.Error().Msg("User not found in context")
		return "", "", fmt.Errorf("user not found in context")
	}
	userModel, ok := user.(*models.User)
	if !ok {
		s.logger.Error().Msg("Invalid user type in context")
		return "", "", fmt.Errorf("invalid user type in context")
	}

	// Check if user has enough credits
	budget, err := s.cacheServiceDB.GetTierTokenBudgetDB(userModel.ID, priceTier)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to get user budget")
		return "", "", fmt.Errorf("failed to get user budget: %w", err)
	}

	remainingCredit := budget.TokenHoursBought - budget.TokenHoursUsed
	if remainingCredit <= (999_999.0 / 1_000_000.0 * 11.0 / 60.0) {
		s.logger.Error().Msg("Insufficient credits to start a new session")
		return "", "", fmt.Errorf("insufficient credits to start a new session")
	}

	s.logger.Info().Msgf("Aggregating documents for arXiv IDs: %v and user PDFs: %v\n", arxivIDs, userPDFs)
	// Aggregate content
	aggregatedContent, err := s.contentAggregation.AggregateDocuments(arxivIDs, userPDFs)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to aggregate documents")
		return "", "", fmt.Errorf("failed to aggregate documents: %w", err)
	}

	// Save raw text cache to Google Cloud Storage
	sessionID := uuid.New().String() // Generate a unique session ID
	s.logger.Info().Msgf("Saving raw text cache for session ID: %s", sessionID)
	err = s.SaveRawTextCache(c.Request.Context(), sessionID, aggregatedContent)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to save raw text cache")
		return "", "", fmt.Errorf("failed to save raw text cache: %w", err)
	}

	s.logger.Info().Msgf("Creating content cache for user ID: %s, session ID: %s, price tier: %s", userModel.ID, sessionID, priceTier)
	// Create cache
	cacheName, cacheExpiryTime, err := s.cacheManagement.CreateContentCache(c.Request.Context(), userModel.ID, sessionID, priceTier, aggregatedContent)
	// Printout cacheCreateTime
	s.logger.Info().Msgf("Cache expiry time from CreateContentCache: %v", cacheExpiryTime)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to create content cache")
		return "", "", fmt.Errorf("failed to create content cache: %w", err)
	}

	s.logger.Info().Msgf("Starting chat session for user ID: %s, cache name: %s, session ID: %s", userModel.ID, cacheName, sessionID)
	// Start chat session
	err = s.chatSession.StartChatSession(c.Request.Context(), userModel.ID, cacheName, sessionID, cacheExpiryTime)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to start chat session")
		// Clean up cache if session start fails
		s.logger.Info().Msgf("Cleaning up cache for user ID: %s, session ID: %s, cache name: %s", userModel.ID, sessionID, cacheName)
		if err := s.cacheManagement.DeleteCache(c.Request.Context(), userModel.ID, sessionID, cacheName); err != nil {
			s.logger.Error().Err(err).Msg("Failed to delete cache during cleanup")
		}

		// cleanup the storage file
		fileName := fmt.Sprintf("raw_cache_%s.txt", sessionID)
		s.logger.Info().Msgf("Deleting file from storage: %s", fileName)
		if err := s.cloudStorage.DeleteFile(c.Request.Context(), s.bucketName, fileName); err != nil {
			s.logger.Error().Err(err).Msg("Failed to delete file from storage during cleanup")
		}

		return "", "", fmt.Errorf("failed to start chat session: %w", err)
	}

	s.logger.Info().Msgf("Research session started successfully. Session ID: %s, Cache Name: %s", sessionID, cacheName)
	return sessionID, cacheName, nil
}

func (s *ResearchChatService) SaveRawTextCache(ctx context.Context, sessionID string, content string) error {
	objectName := fmt.Sprintf("raw_cache_%s.txt", sessionID)
	s.logger.Info().Msgf("Uploading file to storage: %s", objectName)
	err := s.cloudStorage.UploadFile(ctx, s.bucketName, objectName, strings.NewReader(content))
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to upload file to storage")
	}
	return err
}

func (s *ResearchChatService) GetRawTextCache(ctx context.Context, sessionID string) (string, error) {
	objectName := fmt.Sprintf("raw_cache_%s.txt", sessionID)
	s.logger.Info().Msgf("Downloading file from storage: %s", objectName)
	content, err := s.cloudStorage.DownloadFile(ctx, s.bucketName, objectName)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to download file from storage")
		return "", err
	}
	return string(content), nil
}

func (s *ResearchChatService) SendMessage(ctx context.Context, sessionID, message string) (*genai.GenerateContentResponseIterator, error) {
	// Send message and get response
	responseIterator, err := s.chatSession.StreamChatMessage(ctx, sessionID, message)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to send message")
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	return responseIterator, nil
}

func (s *ResearchChatService) SaveAIResponse(sessionID, response string) error {
	// Save AI response to history
	if err := s.chatService.SaveMessageToDB(sessionID, "ai", response); err != nil {
		s.logger.Error().Err(err).Msg("Failed to save AI response")
		return fmt.Errorf("failed to save AI response: %w", err)
	}

	return nil
}

func (s *ResearchChatService) EndResearchSession(ctx context.Context, sessionID string) error {
	// Assumes the only caller of this method is an api endpoint whena user Terminates a chat
	var reason TerminationReason = UserInitiated
	if err := s.chatSession.TerminateSession(ctx, sessionID, reason); err != nil {
		s.logger.Error().Err(err).Msg("Failed to terminate chat session")
		return fmt.Errorf("failed to terminate chat session: %w", err)
	}
	return nil
}

func (s *ResearchChatService) GetUserChatHistory(userID uuid.UUID) ([]models.Chat, error) {
	return s.chatService.GetChatsByUserIDFromDB(userID)
}

func (s *ResearchChatService) UpdateSessionActivity(ctx context.Context, sessionID string) error {
	return s.chatSession.UpdateSessionActivity(ctx, sessionID)
}

func (s *ResearchChatService) SaveMessageToDB(ctx context.Context, sessionID, msgType, content string) error {
	return s.chatService.SaveMessageToDB(sessionID, msgType, content)
}

func (s *ResearchChatService) CheckSessionStatus(sessionID string) (SessionStatus, time.Time, error) {
	return s.chatSession.CheckSessionStatus(sessionID)
}

func (s *ResearchChatService) GetSessionStatus(sessionID string) (SessionStatusInfo, error) {
	return s.chatSession.GetSessionStatus(sessionID)
}
func (s *ResearchChatService) ExtendSession(ctx context.Context, sessionID string) error {
	return s.chatSession.ExtendSession(ctx, sessionID)
}
func (s *ResearchChatService) CheckCreditStatus(sessionID string) (bool, bool, float64, error) {
	return s.chatSession.CheckCreditStatus(sessionID)
}
