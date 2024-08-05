package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Cache struct {
	gorm.Model
	UserID          uuid.UUID `gorm:"type:uuid;index"`
	SessionID       string    `gorm:"index;unique"`
	CacheName       string
	PriceTier       string
	TotalTokenCount int32
	CreationTime    time.Time
	TerminationTime time.Time
}

type CacheUsage struct {
	gorm.Model
	UserID      uuid.UUID    `gorm:"type:uuid;index"`
	ModelUsages []ModelUsage `gorm:"foreignKey:CacheUsageID"`
}

type ModelUsage struct {
	gorm.Model
	CacheUsageID uint
	PriceTier    string  `gorm:"index"`
	TokensBought float64 // Total tokens bought for this model (in millions)
	TokensUsed   float64 // Cumulative tokens used for this model (in millions)
}
