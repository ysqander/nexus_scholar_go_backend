package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"nexus_scholar_go_backend/internal/models"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gorm.io/gorm"
)

type MockGenAIClient struct {
	mock.Mock
}

func (m *MockGenAIClient) CreateCachedContent(ctx context.Context, cc *genai.CachedContent) (*genai.CachedContent, error) {
	args := m.Called(ctx, cc)
	return args.Get(0).(*genai.CachedContent), args.Error(1)
}

func (m *MockGenAIClient) GetCachedContent(ctx context.Context, name string) (*genai.CachedContent, error) {
	args := m.Called(ctx, name)
	return args.Get(0).(*genai.CachedContent), args.Error(1)
}

func (m *MockGenAIClient) DeleteCachedContent(ctx context.Context, name string) error {
	args := m.Called(ctx, name)
	return args.Error(0)
}

func (m *MockGenAIClient) UpdateCachedContent(ctx context.Context, cc *genai.CachedContent, update *genai.CachedContentToUpdate) (*genai.CachedContent, error) {
	args := m.Called(ctx, cc, update)
	return args.Get(0).(*genai.CachedContent), args.Error(1)
}

func (m *MockGenAIClient) GenerativeModelFromCachedContent(cc *genai.CachedContent) *genai.GenerativeModel {
	args := m.Called(cc)
	return args.Get(0).(*genai.GenerativeModel)
}

type MockDB struct {
	mock.Mock
}

func (m *MockDB) Where(query interface{}, args ...interface{}) *gorm.DB {
	called := m.Called(query, args)
	return called.Get(0).(*gorm.DB)
}

func (m *MockDB) Assign(attrs ...interface{}) *gorm.DB {
	called := m.Called(attrs)
	return called.Get(0).(*gorm.DB)
}

func (m *MockDB) FirstOrCreate(dest interface{}, conds ...interface{}) *gorm.DB {
	called := m.Called(dest, conds)
	return called.Get(0).(*gorm.DB)
}

func (m *MockDB) Create(value interface{}) *gorm.DB {
	called := m.Called(value)
	return called.Get(0).(*gorm.DB)
}

type MockChatService struct {
	mock.Mock
}

func (m *MockChatService) SaveChat(userID uuid.UUID, sessionID string) error {
	args := m.Called(userID, sessionID)
	return args.Error(0)
}

func (m *MockChatService) SaveMessage(sessionID, msgType, content string) error {
	args := m.Called(sessionID, msgType, content)
	return args.Error(0)
}

func (m *MockChatService) GetChatBySessionID(sessionID string) (*models.Chat, error) {
	args := m.Called(sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Chat), args.Error(1)
}

func (m *MockChatService) GetChatsByUserID(userID uuid.UUID) ([]models.Chat, error) {
	args := m.Called(userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Chat), args.Error(1)
}

func (m *MockChatService) DeleteChatBySessionID(sessionID string) error {
	args := m.Called(sessionID)
	return args.Error(0)
}

func (m *MockChatService) GetMessagesByChatID(chatID uint) ([]models.Message, error) {
	args := m.Called(chatID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Message), args.Error(1)
}

func TestStartChatSession(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	mockClient := new(MockGenAIClient)
	mockDB := new(MockDB)
	mockChatService := new(MockChatService)

	cacheService := NewCacheService(mockClient, mockDB, mockChatService, "test-project",
		WithExpirationTime(5*time.Minute),
		WithHeartbeatTimeout(30*time.Second),
	)

	cachedContentName := "testCacheName"
	mockCachedContent := &genai.CachedContent{
		Name: cachedContentName,
	}
	mockGenerativeModel := &genai.GenerativeModel{}

	// Set up expectations
	mockClient.On("GetCachedContent", mock.Anything, cachedContentName).Return(mockCachedContent, nil)
	mockClient.On("GenerativeModelFromCachedContent", mockCachedContent).Return(mockGenerativeModel)

	t.Run("Successful session start", func(t *testing.T) {
		// Create a mock Gin context
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		// Add a valid HTTP request to the context
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.Request = req

		// Add a user to the context
		userID := uuid.New()
		user := &models.User{ID: userID}
		c.Set("user", user)

		mockChatService.On("SaveChat", userID, mock.AnythingOfType("string")).Return(nil).Once()

		// Test execution
		sessionID, err := cacheService.StartChatSession(c, cachedContentName)

		// Assertions
		assert.NoError(t, err)
		assert.NotEmpty(t, sessionID)

		mockClient.AssertExpectations(t)
		mockChatService.AssertExpectations(t)

		// Verify that a session was stored
		_, ok := cacheService.sessions.Load(sessionID)
		assert.True(t, ok, "Session should be stored in the CacheService")
	})

	t.Run("No user in context", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.Request = req

		_, err := cacheService.StartChatSession(c, cachedContentName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user not found in context")
	})

	t.Run("Invalid user type in context", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.Request = req
		c.Set("user", "invalid user")

		_, err := cacheService.StartChatSession(c, cachedContentName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid user type in context")
	})

	t.Run("Error saving chat", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.Request = req

		userID := uuid.New()
		user := &models.User{ID: userID}
		c.Set("user", user)

		mockChatService.On("SaveChat", userID, mock.AnythingOfType("string")).Return(assert.AnError).Once()

		_, err := cacheService.StartChatSession(c, cachedContentName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to save chat")
	})
}

func TestUpdateSessionHeartbeat(t *testing.T) {
	mockClient := new(MockGenAIClient)
	mockDB := new(MockDB)

	cacheService := &CacheService{
		genaiClient:       mockClient,
		db:                mockDB,
		heartbeatTimeout:  1 * time.Minute,
		sessionTimeout:    10 * time.Minute,
		cacheExtendPeriod: 5 * time.Minute,
	}

	sessionID := "test-session-id"
	cachedContentName := "test-cached-content"

	// Set up initial session
	cacheService.sessions.Store(sessionID, ChatSessionInfo{
		Session:           nil, // We don't need an actual session for this test
		LastAccessed:      time.Now().Add(-2 * time.Minute),
		CachedContentName: cachedContentName,
		LastHeartbeat:     time.Now().Add(-2 * time.Minute),
		HeartbeatsMissed:  1,
		LastCacheExtend:   time.Now().Add(-6 * time.Minute),
	})

	// Mock the cache extension
	mockClient.On("UpdateCachedContent", mock.Anything, mock.Anything, mock.Anything).Return(&genai.CachedContent{}, nil)

	err := cacheService.UpdateSessionHeartbeat(sessionID)

	assert.NoError(t, err)

	// Verify session was updated
	sessionInfo, ok := cacheService.sessions.Load(sessionID)
	assert.True(t, ok)
	assert.Equal(t, 0, sessionInfo.(ChatSessionInfo).HeartbeatsMissed)
	assert.True(t, sessionInfo.(ChatSessionInfo).LastHeartbeat.After(time.Now().Add(-1*time.Second)))
	assert.True(t, sessionInfo.(ChatSessionInfo).LastCacheExtend.After(time.Now().Add(-1*time.Second)))

	mockClient.AssertExpectations(t)
}

func TestTerminateSession(t *testing.T) {
	mockClient := new(MockGenAIClient)
	mockDB := new(MockDB)
	mockChatService := new(MockChatService)

	cacheService := NewCacheService(mockClient, mockDB, mockChatService, "test-project")

	ctx := context.Background()
	sessionID := "test-session-id"
	cachedContentName := "test-cached-content"

	t.Run("Successful termination", func(t *testing.T) {
		cacheService.sessions.Store(sessionID, ChatSessionInfo{
			Session:           nil,
			CachedContentName: cachedContentName,
		})

		mockClient.On("GetCachedContent", ctx, cachedContentName).Return(&genai.CachedContent{}, nil).Once()
		mockClient.On("DeleteCachedContent", ctx, cachedContentName).Return(nil).Once()

		err := cacheService.TerminateSession(ctx, sessionID)
		assert.NoError(t, err)

		_, ok := cacheService.sessions.Load(sessionID)
		assert.False(t, ok)

		mockClient.AssertExpectations(t)
	})

	t.Run("Cached content not found", func(t *testing.T) {
		cacheService.sessions.Store(sessionID, ChatSessionInfo{
			Session:           nil,
			CachedContentName: cachedContentName,
		})

		mockClient.On("GetCachedContent", ctx, cachedContentName).Return((*genai.CachedContent)(nil), fmt.Errorf("not found")).Once()

		err := cacheService.TerminateSession(ctx, sessionID)
		assert.NoError(t, err)

		_, ok := cacheService.sessions.Load(sessionID)
		assert.False(t, ok)

		mockClient.AssertExpectations(t)
	})

	t.Run("Error deleting cached content", func(t *testing.T) {
		cacheService.sessions.Store(sessionID, ChatSessionInfo{
			Session:           nil,
			CachedContentName: cachedContentName,
		})

		mockClient.On("GetCachedContent", ctx, cachedContentName).Return(&genai.CachedContent{}, nil).Once()
		mockClient.On("DeleteCachedContent", ctx, cachedContentName).Return(fmt.Errorf("delete error")).Once()

		err := cacheService.TerminateSession(ctx, sessionID)
		assert.NoError(t, err)

		_, ok := cacheService.sessions.Load(sessionID)
		assert.False(t, ok)

		mockClient.AssertExpectations(t)
	})

	t.Run("Session not found", func(t *testing.T) {
		err := cacheService.TerminateSession(ctx, "non-existent-session")
		assert.NoError(t, err)

		mockClient.AssertNotCalled(t, "GetCachedContent")
		mockClient.AssertNotCalled(t, "DeleteCachedContent")
	})
}

func TestUpdateSessionChatHistory(t *testing.T) {
	mockClient := new(MockGenAIClient)
	mockDB := new(MockDB)
	mockChatService := new(MockChatService)

	cacheService := NewCacheService(mockClient, mockDB, mockChatService, "test-project")

	sessionID := "test-session-id"
	chatType := "user"
	content := "Hello, AI!"

	t.Run("Successful update", func(t *testing.T) {
		cacheService.sessions.Store(sessionID, ChatSessionInfo{
			Session:     nil,
			ChatHistory: []ChatMessage{},
		})

		mockChatService.On("SaveMessage", sessionID, chatType, content).Return(nil).Once()

		err := cacheService.UpdateSessionChatHistory(sessionID, chatType, content)
		assert.NoError(t, err)

		sessionInfo, ok := cacheService.sessions.Load(sessionID)
		assert.True(t, ok)
		assert.Len(t, sessionInfo.(ChatSessionInfo).ChatHistory, 1)
		assert.Equal(t, chatType, sessionInfo.(ChatSessionInfo).ChatHistory[0].Type)
		assert.Equal(t, content, sessionInfo.(ChatSessionInfo).ChatHistory[0].Content)

		mockChatService.AssertExpectations(t)
	})

	t.Run("Session not found", func(t *testing.T) {
		err := cacheService.UpdateSessionChatHistory("non-existent-session", chatType, content)
		assert.Contains(t, err.Error(), "session not found")
		mockChatService.AssertNotCalled(t, "SaveMessage")
	})

	t.Run("Error saving message", func(t *testing.T) {
		cacheService.sessions.Store(sessionID, ChatSessionInfo{
			Session:     nil,
			ChatHistory: []ChatMessage{},
		})

		mockChatService.On("SaveMessage", sessionID, chatType, content).Return(fmt.Errorf("save error")).Once()

		err := cacheService.UpdateSessionChatHistory(sessionID, chatType, content)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to save message")

		mockChatService.AssertExpectations(t)
	})
}

func TestCleanupExpiredSessions(t *testing.T) {
	mockClient := new(MockGenAIClient)
	mockDB := new(MockDB)

	cacheService := &CacheService{
		genaiClient:      mockClient,
		db:               mockDB,
		sessionTimeout:   5 * time.Minute,
		heartbeatTimeout: 1 * time.Minute,
	}

	ctx := context.Background()

	// Set up sessions
	activeSessiosnID := "active-session"
	expiredSessionID := "expired-session"
	missedHeartbeatsSessionID := "missed-heartbeats-session"

	cacheService.sessions.Store(activeSessiosnID, ChatSessionInfo{
		Session:       nil,
		LastAccessed:  time.Now(),
		LastHeartbeat: time.Now(),
	})
	cacheService.sessions.Store(expiredSessionID, ChatSessionInfo{
		Session:       nil,
		LastAccessed:  time.Now().Add(-10 * time.Minute),
		LastHeartbeat: time.Now().Add(-10 * time.Minute),
	})
	cacheService.sessions.Store(missedHeartbeatsSessionID, ChatSessionInfo{
		Session:          nil,
		LastAccessed:     time.Now(),
		LastHeartbeat:    time.Now().Add(-2 * time.Minute),
		HeartbeatsMissed: 3,
	})

	mockClient.On("GetCachedContent", mock.Anything, mock.Anything).Return(&genai.CachedContent{}, nil)
	mockClient.On("DeleteCachedContent", mock.Anything, mock.Anything).Return(nil)

	cacheService.cleanupExpiredSessions(ctx)

	// Verify active session remains
	_, ok := cacheService.sessions.Load(activeSessiosnID)
	assert.True(t, ok)

	// Verify expired and missed heartbeats sessions are removed
	_, ok = cacheService.sessions.Load(expiredSessionID)
	assert.False(t, ok)
	_, ok = cacheService.sessions.Load(missedHeartbeatsSessionID)
	assert.False(t, ok)

	mockClient.AssertExpectations(t)
}

func TestConcurrentAccess(t *testing.T) {
	mockClient := new(MockGenAIClient)
	mockDB := new(MockDB)
	mockChatService := new(MockChatService)
	cacheService := NewCacheService(mockClient, mockDB, mockChatService, "test-project")

	sessionID := "test-session-id"
	chatType := "user"
	content := "Hello, AI!"

	cacheService.sessions.Store(sessionID, ChatSessionInfo{
		Session:           nil,
		LastAccessed:      time.Now(),
		LastHeartbeat:     time.Now(),
		HeartbeatsMissed:  0,
		ChatHistory:       []ChatMessage{},
		CachedContentName: "test-cached-content",
	})

	mockClient.On("GetCachedContent", mock.Anything, mock.Anything).Return(&genai.CachedContent{}, nil)
	mockClient.On("DeleteCachedContent", mock.Anything, mock.Anything).Return(nil)
	mockChatService.On("SaveMessage", sessionID, chatType, content).Return(nil)

	numGoroutines := 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3)

	// Use atomic operations to count successful operations
	var (
		heartbeatsUpdated  int32
		messagesAdded      int32
		sessionsTerminated int32
	)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			err := cacheService.UpdateSessionHeartbeat(sessionID)
			if err == nil {
				atomic.AddInt32(&heartbeatsUpdated, 1)
			}
		}()

		go func() {
			defer wg.Done()
			err := cacheService.UpdateSessionChatHistory(sessionID, chatType, content)
			if err == nil {
				atomic.AddInt32(&messagesAdded, 1)
			}
		}()

		go func() {
			defer wg.Done()
			err := cacheService.TerminateSession(context.Background(), sessionID)
			if err == nil {
				atomic.AddInt32(&sessionsTerminated, 1)
			}
		}()
	}

	wg.Wait()

	// Check the final state
	_, ok := cacheService.sessions.Load(sessionID)
	assert.False(t, ok, "Session should be terminated after concurrent operations")

	// Log the counts of successful operations
	t.Logf("Heartbeats updated: %d", heartbeatsUpdated)
	t.Logf("Messages added: %d", messagesAdded)
	t.Logf("Sessions terminated: %d", sessionsTerminated)

	// Assert that at least some operations were successful
	assert.True(t, heartbeatsUpdated > 0, "Some heartbeats should have been updated")
	assert.True(t, messagesAdded > 0, "Some messages should have been added")
	assert.True(t, sessionsTerminated > 0, "Session should have been terminated at least once")

	// Check that the total number of operations is correct
	assert.Equal(t, int32(numGoroutines*3), heartbeatsUpdated+messagesAdded+sessionsTerminated,
		"Total number of successful operations should equal the number of goroutines * 3")

	mockClient.AssertExpectations(t)
	mockChatService.AssertExpectations(t)
	mockDB.AssertExpectations(t)
}
