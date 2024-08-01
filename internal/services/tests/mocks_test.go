package services_test

import (
	"context"
	"nexus_scholar_go_backend/internal/models"
	"nexus_scholar_go_backend/internal/services"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
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

type MockChatServiceDB struct {
	mock.Mock
}

func (m *MockChatServiceDB) SaveChatToDB(userID uuid.UUID, sessionID string) error {
	args := m.Called(userID, sessionID)
	return args.Error(0)
}

func (m *MockChatServiceDB) SaveMessageToDB(sessionID, msgType, content string) error {
	args := m.Called(sessionID, msgType, content)
	return args.Error(0)
}

func (m *MockChatServiceDB) GetChatBySessionIDFromDB(sessionID string) (*models.Chat, error) {
	args := m.Called(sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Chat), args.Error(1)
}

func (m *MockChatServiceDB) GetChatsByUserIDFromDB(userID uuid.UUID) ([]models.Chat, error) {
	args := m.Called(userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Chat), args.Error(1)
}

func (m *MockChatServiceDB) DeleteChatBySessionIDFromDB(sessionID string) error {
	args := m.Called(sessionID)
	return args.Error(0)
}

func (m *MockChatServiceDB) GetMessagesByChatIDFromDB(chatID uint) ([]models.Message, error) {
	args := m.Called(chatID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Message), args.Error(1)
}

type MockContentAggregator struct {
	mock.Mock
}

func (m *MockContentAggregator) AggregateDocuments(arxivIDs []string, userPDFs []string) (string, error) {
	args := m.Called(arxivIDs, userPDFs)
	return args.String(0), args.Error(1)
}

type MockCacheManager struct {
	mock.Mock
}

func (m *MockCacheManager) CreateContentCache(ctx context.Context, aggregatedContent string) (string, error) {
	args := m.Called(ctx, aggregatedContent)
	return args.String(0), args.Error(1)
}

func (m *MockCacheManager) ExtendCacheLifetime(ctx context.Context, cachedContentName string) error {
	args := m.Called(ctx, cachedContentName)
	return args.Error(0)
}

func (m *MockCacheManager) DeleteCache(ctx context.Context, cachedContentName string) error {
	args := m.Called(ctx, cachedContentName)
	return args.Error(0)
}

func (m *MockCacheManager) GetGenerativeModel(ctx context.Context, cachedContentName string) (*genai.GenerativeModel, error) {
	args := m.Called(ctx, cachedContentName)
	return args.Get(0).(*genai.GenerativeModel), args.Error(1)
}

type MockChatSessionManager struct {
	mock.Mock
}

func (m *MockChatSessionManager) StartChatSession(ctx context.Context, userID uuid.UUID, cachedContentName string) (string, error) {
	args := m.Called(ctx, userID, cachedContentName)
	return args.String(0), args.Error(1)
}

func (m *MockChatSessionManager) UpdateSessionHeartbeat(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

func (m *MockChatSessionManager) UpdateSessionChatHistory(sessionID, chatType, content string) error {
	args := m.Called(sessionID, chatType, content)
	return args.Error(0)
}

func (m *MockChatSessionManager) TerminateSession(ctx context.Context, sessionID string, reason services.TerminationReason) error {
	args := m.Called(ctx, sessionID, reason)
	return args.Error(0)
}

func (m *MockChatSessionManager) StreamChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponseIterator, error) {
	args := m.Called(ctx, sessionID, message)
	return args.Get(0).(*genai.GenerateContentResponseIterator), args.Error(1)
}
