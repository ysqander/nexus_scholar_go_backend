package services

import (
	"context"
	"fmt"
	"nexus_scholar_go_backend/internal/models"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
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
	logger             zerolog.Logger
}

func NewCacheManagementService(
	genAIClient GenAIClient,
	contentAggregation *ContentAggregationService,
	expirationTime time.Duration,
	cacheServiceDB CacheServiceDB,
	chatServiceDB ChatServiceDB,
	logger zerolog.Logger,
) *CacheManagementService {
	return &CacheManagementService{
		genAIClient:        genAIClient,
		contentAggregation: contentAggregation,
		expirationTime:     expirationTime,
		cacheServiceDB:     cacheServiceDB,
		chatServiceDB:      chatServiceDB,
		logger:             logger,
	}
}

func (cms *CacheManagementService) CreateContentCache(ctx context.Context, userID uuid.UUID, sessionID string, priceTier, aggregatedContent string) (string, time.Time, error) {
	cms.logger.Info().Str("userID", userID.String()).Str("sessionID", sessionID).Str("priceTier", priceTier).Msg("Creating content cache")

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
		cms.logger.Error().Err(err).Msg("Failed to create cached content")
		return "", time.Time{}, fmt.Errorf("failed to create cached content: %v", err)
	}

	tokenCount := cachedContent.UsageMetadata.TotalTokenCount
	cacheExpiryTime := cachedContent.CreateTime.Add(cms.expirationTime)
	cacheName := cachedContent.Name

	err = cms.cacheServiceDB.CreateCacheDB(userID, sessionID, cacheName, priceTier, tokenCount, cachedContent.CreateTime)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to save cache data to database")
		return "", time.Time{}, fmt.Errorf("failed to save cache data: %v", err)
	}

	cms.logger.Info().Str("cacheName", cacheName).Time("expiryTime", cacheExpiryTime).Msg("Content cache created successfully")
	return cacheName, cacheExpiryTime, nil
}

func (cms *CacheManagementService) ExtendCacheLifetime(ctx context.Context, cachedContentName string, newExpirationTime time.Time) error {
	cms.logger.Info().Str("cachedContentName", cachedContentName).Time("newExpirationTime", newExpirationTime).Msg("Extending cache lifetime")

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
		cms.logger.Error().Err(err).Msg("Failed to update cached content expiration")
		return fmt.Errorf("failed to update cached content expiration: %v", err)
	}

	cms.logger.Info().Msg("Cache lifetime extended successfully")
	return nil
}

func (cms *CacheManagementService) DeleteCache(ctx context.Context, userID uuid.UUID, sessionID string, cachedContentName string) error {
	cms.logger.Info().Str("userID", userID.String()).Str("sessionID", sessionID).Str("cachedContentName", cachedContentName).Msg("Deleting cache")

	err := cms.genAIClient.DeleteCachedContent(ctx, cachedContentName)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to delete cached content")
		return fmt.Errorf("failed to delete cached content: %v", err)
	}

	err = cms.RecordCacheTokenUsage(ctx, userID, sessionID)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to record cache token usage")
		return fmt.Errorf("failed to record cache token usage: %v", err)
	}

	cms.logger.Info().Msg("Cache deleted successfully")
	return nil
}

func (cms *CacheManagementService) RecordCacheTokenUsage(ctx context.Context, userID uuid.UUID, sessionID string) error {
	cms.logger.Info().Str("userID", userID.String()).Str("sessionID", sessionID).Msg("Recording cache token usage")

	cache, err := cms.cacheServiceDB.GetCacheDB(sessionID)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to get cache from database")
		return fmt.Errorf("failed to get cache: %v", err)
	}

	terminationTime := time.Now()
	err = cms.cacheServiceDB.UpdateCacheTerminationTimeDB(sessionID, terminationTime)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to update cache termination time")
		return fmt.Errorf("failed to update cache termination time: %v", err)
	}

	duration := terminationTime.Sub(cache.CreationTime)
	durationSeconds := duration.Seconds()
	tokenHours := float64(cache.TotalTokenCount) * durationSeconds / (3600 * 1_000_000)

	err = cms.chatServiceDB.UpdateChatMetrics(sessionID, durationSeconds, cache.TotalTokenCount, cache.PriceTier, tokenHours, terminationTime)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to update chat metrics")
		return fmt.Errorf("failed to update chat metrics: %v", err)
	}

	err = cms.LogCacheUsage(ctx, userID, cache.PriceTier, tokenHours, durationSeconds, cache.TotalTokenCount)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to log final cache usage")
		return fmt.Errorf("failed to log final cache usage: %v", err)
	}

	cms.logger.Info().Msg("Cache token usage recorded successfully")
	return nil
}

func (cms *CacheManagementService) GetGenerativeModel(ctx context.Context, cachedContentName string) (*genai.GenerativeModel, error) {
	cms.logger.Info().Str("cachedContentName", cachedContentName).Msg("Getting generative model")

	cachedContent, err := cms.genAIClient.GetCachedContent(ctx, cachedContentName)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to get cached content")
		return nil, fmt.Errorf("failed to get cached content: %v", err)
	}

	model := cms.genAIClient.GenerativeModelFromCachedContent(cachedContent)
	cms.logger.Info().Msg("Generative model retrieved successfully")
	return model, nil
}

func (cms *CacheManagementService) UpdateAllowedCacheUsage(ctx context.Context, userID uuid.UUID, priceTier string, additionalTokens float64) error {
	cms.logger.Info().Str("userID", userID.String()).Str("priceTier", priceTier).Float64("additionalTokens", additionalTokens).Msg("Updating allowed cache usage")

	budget, err := cms.cacheServiceDB.GetTierTokenBudgetDB(userID, priceTier)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			cms.logger.Info().Msg("Creating new tier token budget")
			budget = &models.TierTokenBudget{
				UserID:           userID,
				PriceTier:        priceTier,
				TokenHoursBought: additionalTokens,
				TokenHoursUsed:   0,
			}
			return cms.cacheServiceDB.CreateTierTokenBudgetDB(budget)
		}
		cms.logger.Error().Err(err).Msg("Failed to get tier token budget")
		return fmt.Errorf("failed to get tier token budget: %v", err)
	}

	budget.TokenHoursBought += additionalTokens
	err = cms.cacheServiceDB.UpdateTierTokenBudgetDB(budget)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to update tier token budget")
		return err
	}

	cms.logger.Info().Float64("newTokenHoursBought", budget.TokenHoursBought).Msg("Allowed cache usage updated successfully")
	return nil
}

func (cms *CacheManagementService) LogCacheUsage(ctx context.Context, userID uuid.UUID, priceTier string, tokenHoursUsed float64, chatDuration float64, tokenCountUsed int32) error {
	cms.logger.Info().Str("userID", userID.String()).Str("priceTier", priceTier).Float64("tokenHoursUsed", tokenHoursUsed).Float64("chatDuration", chatDuration).Int32("tokenCountUsed", tokenCountUsed).Msg("Logging cache usage")

	budget, err := cms.cacheServiceDB.GetTierTokenBudgetDB(userID, priceTier)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to get tier token budget")
		return fmt.Errorf("failed to get tier token budget: %v", err)
	}

	budget.TokenHoursUsed += tokenHoursUsed
	if budget.TokenHoursUsed >= budget.TokenHoursBought {
		budget.TokenHoursUsed = budget.TokenHoursBought
		cms.logger.Info().Msg("Token hours used has reached or exceeded the bought limit")
	}

	err = cms.cacheServiceDB.UpdateTierTokenBudgetDB(budget)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to update tier token budget")
		return err
	}

	cms.logger.Info().Float64("newTokenHoursUsed", budget.TokenHoursUsed).Msg("Cache usage logged successfully")
	return nil
}

func (cms *CacheManagementService) GetNetTokensByTier(ctx context.Context, userID uuid.UUID) (float64, float64, error) {
	cms.logger.Info().Str("userID", userID.String()).Msg("Getting net tokens by tier")

	budgets, err := cms.cacheServiceDB.GetAllTierTokenBudgetsDB(userID)
	if err != nil {
		cms.logger.Error().Err(err).Msg("Failed to get tier token budgets")
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

	cms.logger.Info().Float64("baseNetTokens", baseNetTokens).Float64("proNetTokens", proNetTokens).Msg("Net tokens retrieved successfully")
	return baseNetTokens, proNetTokens, nil
}
