package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/generative-ai-go/genai"
)

type ContentCreator interface {
	CreateCachedContent(ctx context.Context, cc *genai.CachedContent) (*genai.CachedContent, error)
}

type ContentRetriever interface {
	GetCachedContent(ctx context.Context, name string) (*genai.CachedContent, error)
}

type ContentDeleter interface {
	DeleteCachedContent(ctx context.Context, name string) error
}

type ContentUpdater interface {
	UpdateCachedContent(ctx context.Context, cc *genai.CachedContent, update *genai.CachedContentToUpdate) (*genai.CachedContent, error)
}

type ModelGenerator interface {
	GenerativeModelFromCachedContent(cc *genai.CachedContent) *genai.GenerativeModel
}

type GenAIClient interface {
	ContentCreator
	ContentRetriever
	ContentDeleter
	ContentUpdater
	ModelGenerator
}

type CacheManagementService struct {
	genAIClient        GenAIClient
	contentAggregation *ContentAggregationService
	expirationTime     time.Duration
	cacheExtendPeriod  time.Duration
}

func NewCacheManagementService(
	genAIClient GenAIClient,
	contentAggregation *ContentAggregationService,
	expirationTime,
	cacheExtendPeriod time.Duration,
) *CacheManagementService {
	return &CacheManagementService{
		genAIClient:        genAIClient,
		contentAggregation: contentAggregation,
		expirationTime:     expirationTime,
		cacheExtendPeriod:  cacheExtendPeriod,
	}
}

func (cms *CacheManagementService) CreateContentCache(ctx context.Context, aggregatedContent string) (string, error) {
	cc := &genai.CachedContent{
		Model: "gemini-1.5-flash-001",
		Expiration: genai.ExpireTimeOrTTL{
			TTL: cms.expirationTime,
		},
		Contents: []*genai.Content{
			genai.NewUserContent(genai.Text(aggregatedContent)),
		},
	}

	cachedContent, err := cms.genAIClient.CreateCachedContent(ctx, cc)
	if err != nil {
		return "", fmt.Errorf("failed to create cached content: %v", err)
	}

	return cachedContent.Name, nil
}

func (cms *CacheManagementService) ExtendCacheLifetime(ctx context.Context, cachedContentName string) error {
	cachedContent := &genai.CachedContent{
		Name: cachedContentName,
	}

	newExpiration := genai.ExpireTimeOrTTL{
		TTL: cms.expirationTime,
	}

	updateContent := &genai.CachedContentToUpdate{
		Expiration: &newExpiration,
	}

	_, err := cms.genAIClient.UpdateCachedContent(ctx, cachedContent, updateContent)
	if err != nil {
		return fmt.Errorf("failed to update cached content expiration: %v", err)
	}

	return nil
}

func (cms *CacheManagementService) DeleteCache(ctx context.Context, cachedContentName string) error {
	err := cms.genAIClient.DeleteCachedContent(ctx, cachedContentName)
	if err != nil {
		return fmt.Errorf("failed to delete cached content: %v", err)
	}
	return nil
}

func (cms *CacheManagementService) GetGenerativeModel(ctx context.Context, cachedContentName string) (*genai.GenerativeModel, error) {
	cachedContent, err := cms.genAIClient.GetCachedContent(ctx, cachedContentName)
	if err != nil {
		return nil, fmt.Errorf("failed to get cached content: %v", err)
	}

	return cms.genAIClient.GenerativeModelFromCachedContent(cachedContent), nil
}
