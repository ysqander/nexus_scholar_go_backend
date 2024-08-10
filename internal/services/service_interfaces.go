package services

import (
	"context"
	"io"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
)

type ContentAggregator interface {
	AggregateDocuments(arxivIDs []string, userPDFs []string) (string, error)
}

type CacheManager interface {
	CreateContentCache(ctx context.Context, userID uuid.UUID, sessionID string, priceTier, aggregatedContent string) (string, time.Time, error)
	ExtendCacheLifetime(ctx context.Context, cachedContentName string, newExpirationTime time.Time) error
	DeleteCache(ctx context.Context, userID uuid.UUID, sessionID string, cachedContentName string) error
	GetGenerativeModel(ctx context.Context, cachedContentName string) (*genai.GenerativeModel, error)
}

type ChatSessionManager interface {
	StartChatSession(ctx context.Context, userID uuid.UUID, cachedContentName string, sessionID string, cacheCreateTime time.Time) error
	CheckSessionStatus(sessionID string) (SessionStatus, time.Time, error)
	UpdateSessionActivity(ctx context.Context, sessionID string) error
	TerminateSession(ctx context.Context, sessionID string, reason TerminationReason) error
	StreamChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponseIterator, error)
	GetSessionStatus(sessionID string) (SessionStatusInfo, error)
	ExtendSession(ctx context.Context, sessionID string) error
}

type CloudStorageManager interface {
	UploadFile(ctx context.Context, bucketName, objectName string, content io.Reader) error
	DownloadFile(ctx context.Context, bucketName, objectName string) ([]byte, error)
	DeleteFile(ctx context.Context, bucketName, objectName string) error
	ListFiles(ctx context.Context, bucketName string) ([]string, error)
}
