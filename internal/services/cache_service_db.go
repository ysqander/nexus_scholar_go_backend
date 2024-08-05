package services

import (
	"nexus_scholar_go_backend/internal/models"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type CacheServiceDB interface {
	CreateCacheDB(userID uuid.UUID, sessionID, cacheName string, tokenCount int32, creationTime time.Time) error
	GetCacheDB(sessionID string) (*models.Cache, error)
	UpdateCacheTokenCountDB(sessionID string, tokenCount int32) error
	UpdateCacheTerminationTimeDB(sessionID string, terminationTime time.Time) error
	DeleteCacheDB(sessionID string) error
	CreateCacheUsageDB(usage *models.CacheUsage) error
	GetCacheUsageDB(userID uuid.UUID) (*models.CacheUsage, error)
	UpdateCacheUsageDB(usage *models.CacheUsage) error
	DeleteCacheUsageDB(userID uuid.UUID) error
}

type DefaultCacheService struct {
	db *gorm.DB
}

func NewCacheServiceDB(db *gorm.DB) CacheServiceDB {
	return &DefaultCacheService{db: db}
}

func (s *DefaultCacheService) CreateCacheDB(userID uuid.UUID, sessionID, cacheName string, tokenCount int32, creationTime time.Time) error {
	cache := &models.Cache{
		UserID:          userID,
		SessionID:       sessionID,
		CacheName:       cacheName,
		TotalTokenCount: tokenCount,
		CreationTime:    creationTime,
	}
	return s.db.Create(cache).Error
}

func (s *DefaultCacheService) GetCacheDB(sessionID string) (*models.Cache, error) {
	var cache models.Cache
	err := s.db.Where("session_id = ?", sessionID).First(&cache).Error
	if err != nil {
		return nil, err
	}
	return &cache, nil
}

func (s *DefaultCacheService) UpdateCacheTokenCountDB(sessionID string, tokenCount int32) error {
	return s.db.Model(&models.Cache{}).Where("session_id = ?", sessionID).
		Update("total_token_count", tokenCount).Error
}

func (s *DefaultCacheService) UpdateCacheTerminationTimeDB(sessionID string, terminationTime time.Time) error {
	return s.db.Model(&models.Cache{}).Where("session_id = ?", sessionID).
		Update("termination_time", terminationTime).Error
}

func (s *DefaultCacheService) DeleteCacheDB(sessionID string) error {
	return s.db.Where("session_id = ?", sessionID).Delete(&models.Cache{}).Error
}

func (s *DefaultCacheService) CreateCacheUsageDB(usage *models.CacheUsage) error {
	return s.db.Create(usage).Error
}

func (s *DefaultCacheService) GetCacheUsageDB(userID uuid.UUID) (*models.CacheUsage, error) {
	var usage models.CacheUsage
	err := s.db.Where("user_id = ?", userID).First(&usage).Error
	if err != nil {
		return nil, err
	}
	return &usage, nil
}

func (s *DefaultCacheService) UpdateCacheUsageDB(usage *models.CacheUsage) error {
	return s.db.Save(usage).Error
}

func (s *DefaultCacheService) DeleteCacheUsageDB(userID uuid.UUID) error {
	return s.db.Where("user_id = ?", userID).Delete(&models.CacheUsage{}).Error
}
