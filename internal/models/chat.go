package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Chat struct {
	gorm.Model
	UserID          uuid.UUID `gorm:"type:uuid;index"`
	SessionID       string    `gorm:"index;unique"`
	Messages        []Message
	ChatDuration    float64 `gorm:"type:float"` // in seconds
	TerminationTime time.Time
	TokenCountUsed  int32
	PriceTier       string
	TokenHoursUsed  float64
}

type Message struct {
	gorm.Model
	ChatID    uint   `gorm:"index"` // Foreign key to Chat
	Chat      Chat   `gorm:"foreignKey:ChatID"`
	Type      string `gorm:"type:varchar(20)"`
	Content   string `gorm:"type:text"`
	Timestamp time.Time
}
