package services_test

import (
	"context"
	"fmt"
	"nexus_scholar_go_backend/internal/services"
	"testing"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestStartChatSession(t *testing.T) {
	// Setup
	mockGenAIClient := new(MockGenAIClient)
	mockChatServiceDB := new(MockChatServiceDB)
	mockCacheManager := new(MockCacheManager)

	chatSessionService := services.NewChatSessionService(
		mockGenAIClient,
		mockChatServiceDB,
		mockCacheManager,
		1*time.Minute,  // heartbeatTimeout
		10*time.Minute, // sessionTimeout
	)

	// Test data
	ctx := context.Background()
	userID := uuid.New()
	cachedContentName := "test-cached-content"
	mockModel := &genai.GenerativeModel{}

	t.Run("Successful StartChatSession", func(t *testing.T) {
		// Expectations
		mockCacheManager.On("GetGenerativeModel", mock.Anything, cachedContentName).Return(mockModel, nil).Once()
		mockChatServiceDB.On("SaveChatToDB", userID, mock.AnythingOfType("string")).Return(nil).Once()

		// Execute
		sessionID, err := chatSessionService.StartChatSession(ctx, userID, cachedContentName)

		// Assert
		assert.NoError(t, err)
		assert.NotEmpty(t, sessionID)

		// Verify expectations
		mockCacheManager.AssertExpectations(t)
		mockChatServiceDB.AssertExpectations(t)
	})

	t.Run("Failed to Get GenerativeModel", func(t *testing.T) {
		// Reset mocks
		mockCacheManager.ExpectedCalls = nil
		mockGenAIClient.ExpectedCalls = nil
		mockChatServiceDB.ExpectedCalls = nil

		// Expectations
		expectedError := fmt.Errorf("failed to get generative model")
		var nilModel *genai.GenerativeModel = nil
		mockCacheManager.On("GetGenerativeModel", mock.Anything, cachedContentName).Return(nilModel, expectedError).Once()

		// Execute
		sessionID, err := chatSessionService.StartChatSession(ctx, userID, cachedContentName)

		// Assert
		assert.Error(t, err)
		assert.Empty(t, sessionID)
		assert.Contains(t, err.Error(), expectedError.Error())

		// Verify expectations
		mockCacheManager.AssertExpectations(t)
	})

	t.Run("Failed to Save Chat", func(t *testing.T) {
		// Reset mocks
		mockCacheManager.ExpectedCalls = nil
		mockChatServiceDB.ExpectedCalls = nil

		// Expectations
		mockCacheManager.On("GetGenerativeModel", mock.Anything, cachedContentName).Return(mockModel, nil).Once()
		expectedError := fmt.Errorf("failed to save chat")
		mockChatServiceDB.On("SaveChatToDB", userID, mock.AnythingOfType("string")).Return(expectedError).Once()

		// Execute
		sessionID, err := chatSessionService.StartChatSession(ctx, userID, cachedContentName)

		// Assert
		assert.Error(t, err)
		assert.Empty(t, sessionID)
		assert.Contains(t, err.Error(), expectedError.Error())

		// Verify expectations
		mockCacheManager.AssertExpectations(t)
		mockChatServiceDB.AssertExpectations(t)
	})
}

func TestUpdateSessionHeartbeat(t *testing.T) {
	// Setup
	mockGenAIClient := new(MockGenAIClient)
	mockChatServiceDB := new(MockChatServiceDB)
	mockCacheManager := new(MockCacheManager)

	heartbeatTimeout := 1 * time.Minute
	sessionTimeout := 10 * time.Minute

	chatSessionService := services.NewChatSessionService(
		mockGenAIClient,
		mockChatServiceDB,
		mockCacheManager,
		heartbeatTimeout,
		sessionTimeout,
	)

	ctx := context.Background()
	sessionID := "test-session-id"
	cachedContentName := "test-cached-content"
	userID := uuid.New()

	t.Run("Successful UpdateSessionHeartbeat", func(t *testing.T) {
		// Setup the initial session
		initialSession := services.ChatSessionInfo{
			Session:           nil, // Not needed for this test
			CachedContentName: cachedContentName,
			LastAccessed:      time.Now().Add(-5 * time.Minute),
			LastHeartbeat:     time.Now().Add(-5 * time.Minute),
			HeartbeatsMissed:  2,
			LastCacheExtend:   time.Now().Add(-6 * time.Minute),
			UserID:            userID,
		}

		// Set up the mock expectations
		mockCacheManager.On("ExtendCacheLifetime", mock.Anything, cachedContentName).Return(nil).Once()

		// Manually add the session to the service
		chatSessionService.Sessions().Store(sessionID, initialSession)

		// Execute
		err := chatSessionService.UpdateSessionHeartbeat(ctx, sessionID)

		// Assert
		assert.NoError(t, err)

		// Verify the session was updated correctly
		sessionInterface, exists := chatSessionService.Sessions().Load(sessionID)
		assert.True(t, exists)
		updatedSession := sessionInterface.(services.ChatSessionInfo)

		assert.True(t, updatedSession.LastHeartbeat.After(initialSession.LastHeartbeat))
		assert.Equal(t, 0, updatedSession.HeartbeatsMissed)
		assert.True(t, updatedSession.LastAccessed.After(initialSession.LastAccessed))
		assert.True(t, updatedSession.LastCacheExtend.After(initialSession.LastCacheExtend))

		// Verify mock expectations
		mockCacheManager.AssertExpectations(t)
	})

	t.Run("Session Not Found", func(t *testing.T) {
		// Execute with a non-existent session ID
		err := chatSessionService.UpdateSessionHeartbeat(ctx, "non-existent-session")

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("Cache Extension Failure", func(t *testing.T) {
		// Setup the initial session
		initialSession := services.ChatSessionInfo{
			Session:           nil,
			CachedContentName: cachedContentName,
			LastAccessed:      time.Now().Add(-5 * time.Minute),
			LastHeartbeat:     time.Now().Add(-5 * time.Minute),
			HeartbeatsMissed:  2,
			LastCacheExtend:   time.Now().Add(-11 * time.Minute), // Ensure cache extension is triggered
			UserID:            userID,
		}

		// Set up the mock expectations
		mockCacheManager.On("ExtendCacheLifetime", mock.Anything, cachedContentName).Return(assert.AnError).Once()

		// Manually add the session to the service
		chatSessionService.Sessions().Store(sessionID, initialSession)

		// Execute
		err := chatSessionService.UpdateSessionHeartbeat(ctx, sessionID)

		// Assert
		assert.NoError(t, err) // The method should not return an error even if cache extension fails

		// Verify the session was updated correctly despite cache extension failure
		sessionInterface, exists := chatSessionService.Sessions().Load(sessionID)
		assert.True(t, exists)
		updatedSession := sessionInterface.(services.ChatSessionInfo)

		assert.True(t, updatedSession.LastHeartbeat.After(initialSession.LastHeartbeat))
		assert.Equal(t, 0, updatedSession.HeartbeatsMissed)
		assert.True(t, updatedSession.LastAccessed.After(initialSession.LastAccessed))
		assert.Equal(t, initialSession.LastCacheExtend, updatedSession.LastCacheExtend) // Should not be updated due to failure

		// Verify mock expectations
		mockCacheManager.AssertExpectations(t)
	})
}
