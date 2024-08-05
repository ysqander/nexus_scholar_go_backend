package services_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"nexus_scholar_go_backend/internal/models"
	"nexus_scholar_go_backend/internal/services"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestStartResearchSession(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockContentAggregation := new(MockContentAggregator)
	mockCacheManagement := new(MockCacheManager)
	mockChatSession := new(MockChatSessionManager)
	mockChatServiceDB := new(MockChatServiceDB)
	mockCloudStorage := new(MockCloudStorageManager)

	service := services.NewResearchChatService(
		mockContentAggregation,
		mockCacheManagement,
		mockChatSession,
		mockChatServiceDB,
		10*time.Minute,
		mockCloudStorage,
		"test-bucket",
	)

	// Common test data
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	ctx.Request = req

	userID := uuid.New()
	ctx.Set("user", &models.User{ID: userID})
	arxivIDs := []string{"1234.5678", "8765.4321"}
	userPDFs := []string{"/path/to/user1.pdf", "/path/to/user2.pdf"}

	aggregatedContent := "Aggregated content"
	cacheName := "cache123"
	priceTier := "Base"

	t.Run("Successful StartResearchSession", func(t *testing.T) {
		// Expectations for success case
		mockContentAggregation.On("AggregateDocuments", arxivIDs, userPDFs).Return(aggregatedContent, nil).Once()
		mockCloudStorage.On("UploadFile", mock.Anything, "test-bucket", mock.AnythingOfType("string"), mock.AnythingOfType("*strings.Reader")).Return(nil).Once()
		mockCacheManagement.On("CreateContentCache", mock.Anything, userID, mock.AnythingOfType("string"), priceTier, aggregatedContent).Return(cacheName, nil).Once()
		mockChatSession.On("StartChatSession", mock.Anything, userID, cacheName, mock.AnythingOfType("string")).Return(nil).Once()
		mockChatServiceDB.On("SaveChatToDB", userID, mock.AnythingOfType("string")).Return(nil).Once()

		// Execute
		resultSessionID, resultCacheName, err := service.StartResearchSession(ctx, arxivIDs, userPDFs, priceTier)

		// Assert
		assert.NoError(t, err)
		assert.NotEmpty(t, resultSessionID)
		assert.Equal(t, cacheName, resultCacheName)

		// Verify expectations
		mockContentAggregation.AssertExpectations(t)
		mockCloudStorage.AssertExpectations(t)
		mockCacheManagement.AssertExpectations(t)
		mockChatSession.AssertExpectations(t)
		mockChatServiceDB.AssertExpectations(t)

	})

	t.Run("Failed StartChatSession with Cache Cleanup", func(t *testing.T) {
		// Reset mocks
		mockContentAggregation.ExpectedCalls = nil
		mockCacheManagement.ExpectedCalls = nil
		mockChatSession.ExpectedCalls = nil
		mockChatServiceDB.ExpectedCalls = nil
		mockCloudStorage.ExpectedCalls = nil

		// Expectations for failure case
		mockContentAggregation.On("AggregateDocuments", arxivIDs, userPDFs).Return(aggregatedContent, nil).Once()
		mockCloudStorage.On("UploadFile", mock.Anything, "test-bucket", mock.AnythingOfType("string"), mock.AnythingOfType("*strings.Reader")).Return(nil).Once()
		mockCacheManagement.On("CreateContentCache", mock.Anything, userID, mock.AnythingOfType("string"), priceTier, aggregatedContent).Return(cacheName, nil).Once()
		mockChatSession.On("StartChatSession", mock.Anything, userID, cacheName, mock.AnythingOfType("string")).Return(fmt.Errorf("failed to start chat session")).Once()
		mockCacheManagement.On("DeleteCache", mock.Anything, mock.Anything, mock.AnythingOfType("string"), cacheName).Return(nil).Once()
		mockCloudStorage.On("DeleteFile", mock.Anything, "test-bucket", mock.AnythingOfType("string")).Return(nil).Once()

		// Execute
		resultSessionID, resultCacheName, err := service.StartResearchSession(ctx, arxivIDs, userPDFs, priceTier)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to start chat session")
		assert.Empty(t, resultSessionID)
		assert.Empty(t, resultCacheName)

		// Verify expectations
		mockContentAggregation.AssertExpectations(t)
		mockCloudStorage.AssertExpectations(t)
		mockCacheManagement.AssertExpectations(t)
		mockChatSession.AssertExpectations(t)
		mockChatServiceDB.AssertExpectations(t)
	})
}

func TestSendMessage(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockChatSession := new(MockChatSessionManager)
	mockChatServiceDB := new(MockChatServiceDB)
	mockCloudStorage := new(MockCloudStorageManager)

	service := services.NewResearchChatService(
		nil, // MockContentAggregator (not needed for this test)
		nil, // MockCacheManager (not needed for this test)
		mockChatSession,
		mockChatServiceDB,
		10*time.Minute,
		mockCloudStorage,
		"test-bucket",
	)

	// Test data
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("POST", "/send-message", nil)
	c.Request = req

	sessionID := "test-session-id"
	message := "Hello, AI!"

	// Create a mock iterator
	mockIterator := &genai.GenerateContentResponseIterator{}

	// Expectations
	mockChatSession.On("StreamChatMessage", mock.Anything, sessionID, message).Return(mockIterator, nil)

	// Execute
	resultIterator, err := service.SendMessage(c.Request.Context(), sessionID, message)

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, mockIterator, resultIterator)

	// Verify expectations
	mockChatSession.AssertExpectations(t)
	mockChatServiceDB.AssertExpectations(t)
}

func TestSaveAIResponse(t *testing.T) {
	// Setup
	mockChatServiceDB := new(MockChatServiceDB)
	mockCloudStorage := new(MockCloudStorageManager)

	service := services.NewResearchChatService(
		nil, // MockContentAggregator (not needed for this test)
		nil, // MockCacheManager (not needed for this test)
		nil, // MockChatSessionManager (not needed for this test)
		mockChatServiceDB,
		10*time.Minute,
		mockCloudStorage,
		"test-bucket",
	)

	// Test data
	sessionID := "test-session-123"
	aiResponse := "This is the AI's response."

	t.Run("Successful SaveAIResponse", func(t *testing.T) {
		// Expectation
		mockChatServiceDB.On("SaveMessageToDB", sessionID, "ai", aiResponse).Return(nil).Once()

		// Execute
		err := service.SaveAIResponse(sessionID, aiResponse)

		// Assert
		assert.NoError(t, err)

		// Verify expectation
		mockChatServiceDB.AssertExpectations(t)
	})

	t.Run("Failed SaveAIResponse", func(t *testing.T) {
		// Reset mock
		mockChatServiceDB.ExpectedCalls = nil

		// Expectation for failure case
		expectedError := fmt.Errorf("failed to save message")
		mockChatServiceDB.On("SaveMessageToDB", sessionID, "ai", aiResponse).Return(expectedError).Once()

		// Execute
		err := service.SaveAIResponse(sessionID, aiResponse)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to save AI response")
		assert.Contains(t, err.Error(), expectedError.Error())

		// Verify expectation
		mockChatServiceDB.AssertExpectations(t)
	})
}

func TestEndResearchSession(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockChatSession := new(MockChatSessionManager)
	mockChatServiceDB := new(MockChatServiceDB)
	mockCacheManager := new(MockCacheManager)
	mockCloudStorage := new(MockCloudStorageManager)

	service := services.NewResearchChatService(
		nil, // MockContentAggregator (not needed for this test)
		mockCacheManager,
		mockChatSession,
		mockChatServiceDB,
		10*time.Minute,
		mockCloudStorage,
		"test-bucket",
	)

	// Test data
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("POST", "/end-session", nil)
	ctx.Request = req

	sessionID := "test-session-123"
	reason := services.UserInitiated

	t.Run("Successful EndResearchSession", func(t *testing.T) {
		// Expectations
		mockChatSession.On("TerminateSession", mock.Anything, sessionID, reason).Return(nil).Once()

		// Execute
		err := service.EndResearchSession(ctx.Request.Context(), sessionID)

		// Assert
		assert.NoError(t, err)

		// Verify expectations
		mockChatSession.AssertExpectations(t)
	})

	t.Run("Failed EndResearchSession", func(t *testing.T) {
		// Reset mocks
		mockChatSession.ExpectedCalls = nil

		// Expectations for failure case
		expectedError := fmt.Errorf("failed to terminate session")
		mockChatSession.On("TerminateSession", mock.Anything, sessionID, reason).Return(expectedError).Once()

		// Execute
		err := service.EndResearchSession(ctx.Request.Context(), sessionID)

		// Assert
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to terminate chat session")
		assert.Contains(t, err.Error(), expectedError.Error())

		// Verify expectations
		mockChatSession.AssertExpectations(t)
	})
}
