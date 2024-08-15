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
	cacheServiceDB     CacheServiceDB
	chatServiceDB      ChatServiceDB
}

func NewCacheManagementService(
	genAIClient GenAIClient,
	contentAggregation *ContentAggregationService,
	expirationTime time.Duration,
	cacheServiceDB CacheServiceDB,
	chatServiceDB ChatServiceDB,
) *CacheManagementService {
	return &CacheManagementService{
		genAIClient:        genAIClient,
		contentAggregation: contentAggregation,
		expirationTime:     expirationTime,
		cacheServiceDB:     cacheServiceDB,
		chatServiceDB:      chatServiceDB,
	}
}

func (cms *CacheManagementService) CreateContentCache(ctx context.Context, userID uuid.UUID, sessionID string, priceTier, aggregatedContent string) (string, time.Time, error) {
	var modelName string
	if priceTier == "pro" {
		modelName = "gemini-1.5-pro-001"
	} else {
		modelName = "gemini-1.5-flash-001"
	}
	cc := &genai.CachedContent{
		Model: modelName,
		Expiration: genai.ExpireTimeOrTTL{
			TTL: cms.expirationTime,
		},
		Contents: []*genai.Content{
			genai.NewUserContent(genai.Text(aggregatedContent)),
		},
	}

	cachedContent, err := cms.genAIClient.CreateCachedContent(ctx, cc)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create cached content: %v", err)
	}

	// Get the token count, name and creation time from the usage metadata
	tokenCount := cachedContent.UsageMetadata.TotalTokenCount
	fmt.Printf("DEBUG: Token count: %v\n", tokenCount)

	cacheExpiryTime := cachedContent.CreateTime.Add(cms.expirationTime)
	fmt.Printf("DEBUG: Cache expiry time: %v\n", cacheExpiryTime)
	cacheName := cachedContent.Name

	// Save the cache data to the database
	err = cms.cacheServiceDB.CreateCacheDB(userID, sessionID, cacheName, priceTier, tokenCount, cachedContent.CreateTime)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to save cache data: %v", err)
	}

	return cacheName, cacheExpiryTime, nil
}

func (cms *CacheManagementService) ExtendCacheLifetime(ctx context.Context, cachedContentName string, newExpirationTime time.Time) error {
	cachedContent := &genai.CachedContent{
		Name: cachedContentName,
	}

	newExpiration := genai.ExpireTimeOrTTL{
		ExpireTime: newExpirationTime,
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

	err = cms.RecordCacheTokenUsage(ctx, userID, sessionID)
	if err != nil {
		return fmt.Errorf("failed to record cache token usage: %v", err)
	}

	return nil
}

func (cms *CacheManagementService) RecordCacheTokenUsage(ctx context.Context, userID uuid.UUID, sessionID string) error {
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
	durationSeconds := duration.Seconds()
	fmt.Printf("DEBUG: Duration (seconds): %v\n", durationSeconds)
	tokenHours := float64(cache.TotalTokenCount) * durationSeconds / (3600 * 1_000_000) // Convert to million token-hours
	fmt.Printf("DEBUG: Total token count: %v\n", cache.TotalTokenCount)
	fmt.Printf("DEBUG: Token hours: %v\n", tokenHours)

	// Update chat metrics in table Chats in the DB
	err = cms.chatServiceDB.UpdateChatMetrics(sessionID, durationSeconds, cache.TotalTokenCount, cache.PriceTier, tokenHours, terminationTime)
	if err != nil {
		return fmt.Errorf("failed to update chat metrics: %v", err)
	}

	err = cms.LogCacheUsage(ctx, userID, cache.PriceTier, tokenHours, durationSeconds, cache.TotalTokenCount)
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

func (cms *CacheManagementService) UpdateAllowedCacheUsage(ctx context.Context, userID uuid.UUID, priceTier string, additionalTokens float64) error {

	// Handle TierTokenBudget
	budget, err := cms.cacheServiceDB.GetTierTokenBudgetDB(userID, priceTier)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// Create new budget if it doesn't exist
			budget = &models.TierTokenBudget{
				UserID:           userID,
				PriceTier:        priceTier,
				TokenHoursBought: additionalTokens,
				TokenHoursUsed:   0, // Initialize TokensUsed to 0
			}
			return cms.cacheServiceDB.CreateTierTokenBudgetDB(budget)
		}
		return fmt.Errorf("failed to get tier token budget: %v", err)
	}

	// Update existing budget
	budget.TokenHoursBought += additionalTokens
	return cms.cacheServiceDB.UpdateTierTokenBudgetDB(budget)
}

func (cms *CacheManagementService) LogCacheUsage(ctx context.Context, userID uuid.UUID, priceTier string, tokenHoursUsed float64, chatDuration float64, tokenCountUsed int32) error {
	budget, err := cms.cacheServiceDB.GetTierTokenBudgetDB(userID, priceTier)
	if err != nil {
		return fmt.Errorf("failed to get tier token budget: %v", err)
	}

	if budget.TokenHoursUsed+tokenHoursUsed > budget.TokenHoursBought {
		return fmt.Errorf("usage limit exceeded for tier: %s", priceTier)
	}

	budget.TokenHoursUsed += tokenHoursUsed
	return cms.cacheServiceDB.UpdateTierTokenBudgetDB(budget)
}

func (cms *CacheManagementService) GetNetTokensByTier(ctx context.Context, userID uuid.UUID) (float64, float64, error) {
	budgets, err := cms.cacheServiceDB.GetAllTierTokenBudgetsDB(userID)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get tier token budgets: %v", err)
	}

	var baseNetTokens, proNetTokens float64
	for _, budget := range budgets {
		netTokens := budget.TokenHoursBought - budget.TokenHoursUsed
		if budget.PriceTier == "base" {
			baseNetTokens += netTokens
		} else if budget.PriceTier == "pro" {
			proNetTokens += netTokens
		}
	}

	return baseNetTokens, proNetTokens, nil
}
