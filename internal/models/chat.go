package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Chat struct {
	gorm.Model
	UserID    uuid.UUID `gorm:"type:uuid;index"`
	SessionID string    `gorm:"index;unique"`
	History   []byte    // JSON-encoded chat history
}
