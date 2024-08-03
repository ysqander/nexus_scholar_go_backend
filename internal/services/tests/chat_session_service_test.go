package services_test

import (
	"context"
	"errors"
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
	sessionID := uuid.New().String()

	t.Run("Successful StartChatSession", func(t *testing.T) {
		// Expectations
		mockCacheManager.On("GetGenerativeModel", mock.Anything, cachedContentName).Return(mockModel, nil).Once()
		mockChatServiceDB.On("SaveChatToDB", userID, mock.AnythingOfType("string")).Return(nil).Once()

		// Execute
		err := chatSessionService.StartChatSession(ctx, userID, cachedContentName, sessionID)

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
		err := chatSessionService.StartChatSession(ctx, userID, cachedContentName, sessionID)

		// Assert
		assert.Error(t, err)
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
		err := chatSessionService.StartChatSession(ctx, userID, cachedContentName, sessionID)

		// Assert
		assert.Error(t, err)
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

func TestTerminateSession(t *testing.T) {
	mockGenAIClient := new(MockGenAIClient)
	mockChatServiceDB := new(MockChatServiceDB)
	mockCacheManager := new(MockCacheManager)

	css := services.NewChatSessionService(
		mockGenAIClient,
		mockChatServiceDB,
		mockCacheManager,
		1*time.Minute,  // heartbeatTimeout
		10*time.Minute, // sessionTimeout
	)

	ctx := context.Background()
	sessionID := "test-session-id"
	cachedContentName := "test-cached-content"
	userID := uuid.New()

	t.Run("Successful TerminateSession", func(t *testing.T) {
		// Setup
		sessionInfo := services.ChatSessionInfo{
			Session:           nil, // Not needed for this test
			CachedContentName: cachedContentName,
			LastAccessed:      time.Now(),
			LastHeartbeat:     time.Now(),
			HeartbeatsMissed:  0,
			LastCacheExtend:   time.Now(),
			UserID:            userID,
		}
		css.Sessions().Store(sessionID, sessionInfo)

		mockCacheManager.On("DeleteCache", mock.Anything, cachedContentName).Return(nil).Once()

		// Execute
		err := css.TerminateSession(ctx, sessionID, services.UserInitiated)

		// Assert
		assert.NoError(t, err)
		_, exists := css.Sessions().Load(sessionID)
		assert.False(t, exists, "Session should be removed")

		// Verify expectations
		mockCacheManager.AssertExpectations(t)
		mockChatServiceDB.AssertExpectations(t)
	})

	t.Run("Session Not Found", func(t *testing.T) {
		// Execute
		err := css.TerminateSession(ctx, "non-existent-session", services.UserInitiated)

		// Assert
		assert.Error(t, err)
		assert.Equal(t, services.ErrSessionNotFound, err)
	})

	t.Run("Cache Deletion Failure", func(t *testing.T) {
		// Setup
		sessionInfo := services.ChatSessionInfo{
			Session:           nil,
			CachedContentName: cachedContentName,
			LastAccessed:      time.Now(),
			LastHeartbeat:     time.Now(),
			HeartbeatsMissed:  0,
			LastCacheExtend:   time.Now(),
			UserID:            userID,
		}
		css.Sessions().Store(sessionID, sessionInfo)

		mockCacheManager.On("DeleteCache", mock.Anything, cachedContentName).Return(errors.New("cache deletion error")).Once()

		// Execute
		err := css.TerminateSession(ctx, sessionID, services.UserInitiated)

		// Assert
		assert.NoError(t, err)
		_, exists := css.Sessions().Load(sessionID)
		assert.False(t, exists, "Session should be removed even if cache deletion fails")

		// Verify expectations
		mockCacheManager.AssertExpectations(t)
		mockChatServiceDB.AssertExpectations(t)
	})

}

func TestCleanupExpiredSessions(t *testing.T) {
	mockGenAIClient := new(MockGenAIClient)
	mockChatServiceDB := new(MockChatServiceDB)
	mockCacheManager := new(MockCacheManager)

	heartbeatTimeout := 5 * time.Minute
	sessionTimeout := 30 * time.Minute

	css := services.NewChatSessionService(
		mockGenAIClient,
		mockChatServiceDB,
		mockCacheManager,
		heartbeatTimeout,
		sessionTimeout,
	)

	now := time.Now()

	// Helper function to create a session
	createSession := func(lastAccessed, lastHeartbeat time.Time, heartbeatsMissed int) services.ChatSessionInfo {
		return services.ChatSessionInfo{
			Session:           nil,
			CachedContentName: "test-cached-content",
			LastAccessed:      lastAccessed,
			LastHeartbeat:     lastHeartbeat,
			HeartbeatsMissed:  heartbeatsMissed,
			LastCacheExtend:   now.Add(-10 * time.Minute),
			UserID:            uuid.New(),
		}
	}

	// Test cases
	testCases := []struct {
		name                 string
		sessions             map[string]services.ChatSessionInfo
		expectedTerminations int
		expectedActive       int
	}{
		{
			name: "No expired sessions",
			sessions: map[string]services.ChatSessionInfo{
				"session1": createSession(now.Add(-5*time.Minute), now.Add(-1*time.Minute), 0),
				"session2": createSession(now.Add(-10*time.Minute), now.Add(-2*time.Minute), 1),
			},
			expectedTerminations: 0,
			expectedActive:       2,
		},
		{
			name: "One session expired due to inactivity",
			sessions: map[string]services.ChatSessionInfo{
				"session1": createSession(now.Add(-5*time.Minute), now.Add(-1*time.Minute), 0),
				"session2": createSession(now.Add(-31*time.Minute), now.Add(-31*time.Minute), 1),
			},
			expectedTerminations: 1,
			expectedActive:       1,
		},
		{
			name: "One session expired due to missed heartbeats",
			sessions: map[string]services.ChatSessionInfo{
				"session1": createSession(now.Add(-5*time.Minute), now.Add(-1*time.Minute), 0),
				"session2": createSession(now.Add(-10*time.Minute), now.Add(-6*time.Minute), 3),
			},
			expectedTerminations: 1,
			expectedActive:       1,
		},
		{
			name: "Multiple expired sessions",
			sessions: map[string]services.ChatSessionInfo{
				"session1": createSession(now.Add(-5*time.Minute), now.Add(-1*time.Minute), 0),
				"session2": createSession(now.Add(-31*time.Minute), now.Add(-31*time.Minute), 1),
				"session3": createSession(now.Add(-10*time.Minute), now.Add(-6*time.Minute), 3),
				"session4": createSession(now.Add(-40*time.Minute), now.Add(-35*time.Minute), 5),
			},
			expectedTerminations: 3,
			expectedActive:       1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			css.Sessions().Range(func(key, value interface{}) bool {
				css.Sessions().Delete(key)
				return true
			})
			for id, session := range tc.sessions {
				css.Sessions().Store(id, session)
			}

			// Set up expectations for mock calls
			if tc.expectedTerminations > 0 {
				mockCacheManager.On("DeleteCache", mock.Anything, mock.Anything).Return(nil).Times(tc.expectedTerminations)
			}

			// Execute
			css.CleanupExpiredSessions()

			// Assert
			activeSessions := 0
			css.Sessions().Range(func(key, value interface{}) bool {
				activeSessions++
				return true
			})

			assert.Equal(t, tc.expectedActive, activeSessions, "Number of active sessions after cleanup")
			mockCacheManager.AssertExpectations(t)
			mockChatServiceDB.AssertExpectations(t)

			// Reset mocks for the next test case
			mockCacheManager.ExpectedCalls = nil
			mockChatServiceDB.ExpectedCalls = nil
		})
	}
}
