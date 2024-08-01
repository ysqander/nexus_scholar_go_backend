package services

import (
	"context"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
)

type ContentAggregator interface {
	AggregateDocuments(arxivIDs []string, userPDFs []string) (string, error)
}

type CacheManager interface {
	CreateContentCache(ctx context.Context, aggregatedContent string) (string, error)
	ExtendCacheLifetime(ctx context.Context, cachedContentName string) error
	DeleteCache(ctx context.Context, cachedContentName string) error
	GetGenerativeModel(ctx context.Context, cachedContentName string) (*genai.GenerativeModel, error)
}

type ChatSessionManager interface {
	StartChatSession(ctx context.Context, userID uuid.UUID, cachedContentName string) (string, error)
	UpdateSessionHeartbeat(ctx context.Context, sessionID string) error
	TerminateSession(ctx context.Context, sessionID string, reason TerminationReason) error
	StreamChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponseIterator, error)
}
