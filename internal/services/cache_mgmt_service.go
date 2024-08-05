package services

import (
	"context"
	"fmt"
	"nexus_scholar_go_backend/internal/models"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"gorm.io/gorm"
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
	cacheServiceDB     CacheServiceDB
}

func NewCacheManagementService(
	genAIClient GenAIClient,
	contentAggregation *ContentAggregationService,
	expirationTime,
	cacheExtendPeriod time.Duration,
	cacheServiceDB CacheServiceDB,
) *CacheManagementService {
	return &CacheManagementService{
		genAIClient:        genAIClient,
		contentAggregation: contentAggregation,
		expirationTime:     expirationTime,
		cacheExtendPeriod:  cacheExtendPeriod,
		cacheServiceDB:     cacheServiceDB,
	}
}

func (cms *CacheManagementService) CreateContentCache(ctx context.Context, userID uuid.UUID, sessionID string, priceTier, aggregatedContent string) (string, error) {
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

	// Get the token count from the usage metadata
	tokenCount := cachedContent.UsageMetadata.TotalTokenCount
	createTime := cachedContent.CreateTime

	// Save the cache data to the database
	err = cms.cacheServiceDB.CreateCacheDB(userID, sessionID, cachedContent.Name, tokenCount, createTime)
	if err != nil {
		return "", fmt.Errorf("failed to save cache data: %v", err)
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

func (cms *CacheManagementService) DeleteCache(ctx context.Context, userID uuid.UUID, sessionID string, cachedContentName string) error {
	err := cms.genAIClient.DeleteCachedContent(ctx, cachedContentName)
	if err != nil {
		return fmt.Errorf("failed to delete cached content: %v", err)
	}

	cache, err := cms.cacheServiceDB.GetCacheDB(sessionID)
	if err != nil {
		return fmt.Errorf("failed to get cache: %v", err)
	}
	terminationTime := time.Now()
	err = cms.cacheServiceDB.UpdateCacheTerminationTimeDB(sessionID, terminationTime)
	if err != nil {
		return fmt.Errorf("failed to update cache termination time: %v", err)
	}
	// Calculate and log the final usage for this cache session
	duration := terminationTime.Sub(cache.CreationTime)
	tokenHours := float64(cache.TotalTokenCount) * duration.Hours() / 1_000_000 // Convert to million token-hours
	err = cms.LogCacheUsage(ctx, userID, cache.PriceTier, tokenHours)
	if err != nil {
		return fmt.Errorf("failed to log final cache usage: %v", err)
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

// Cache Usage functions

func (cms *CacheManagementService) GetCacheUsage(ctx context.Context, userID uuid.UUID) (*models.CacheUsage, error) {
	return cms.cacheServiceDB.GetCacheUsageDB(userID)
}

func (cms *CacheManagementService) UpdateAllowedCacheUsage(ctx context.Context, userID uuid.UUID, priceTier string, additionalTokens float64) error {
	usage, err := cms.cacheServiceDB.GetCacheUsageDB(userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// Create new usage record if it doesn't exist
			usage = &models.CacheUsage{
				UserID: userID,
				ModelUsages: []models.ModelUsage{
					{
						PriceTier:    priceTier,
						TokensBought: additionalTokens,
					},
				},
			}
			return cms.cacheServiceDB.CreateCacheUsageDB(usage)
		}
		return fmt.Errorf("failed to get cache usage: %v", err)
	}

	// Find or create ModelUsage for the specific model
	var modelUsage *models.ModelUsage
	for i := range usage.ModelUsages {
		if usage.ModelUsages[i].PriceTier == priceTier {
			modelUsage = &usage.ModelUsages[i]
			break
		}
	}
	if modelUsage == nil {
		usage.ModelUsages = append(usage.ModelUsages, models.ModelUsage{
			PriceTier:    priceTier,
			TokensBought: additionalTokens,
		})
	} else {
		modelUsage.TokensBought += additionalTokens
	}

	return cms.cacheServiceDB.UpdateCacheUsageDB(usage)
}

func (cms *CacheManagementService) LogCacheUsage(ctx context.Context, userID uuid.UUID, priceTier string, tokensUsed float64) error {
	usage, err := cms.cacheServiceDB.GetCacheUsageDB(userID)
	if err != nil {
		return fmt.Errorf("failed to get cache usage: %v", err)
	}

	var modelUsage *models.ModelUsage
	for i := range usage.ModelUsages {
		if usage.ModelUsages[i].PriceTier == priceTier {
			modelUsage = &usage.ModelUsages[i]
			break
		}
	}

	if modelUsage == nil {
		return fmt.Errorf("no usage record found for price tier: %s", priceTier)
	}

	if modelUsage.TokensUsed+tokensUsed > modelUsage.TokensBought {
		return fmt.Errorf("usage limit exceeded for model: %s", priceTier)
	}

	modelUsage.TokensUsed += tokensUsed

	return cms.cacheServiceDB.UpdateCacheUsageDB(usage)
}
