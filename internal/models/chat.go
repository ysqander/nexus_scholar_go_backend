package models

import (
	"gorm.io/gorm"
)

type Chat struct {
	gorm.Model
	UserID    uint   `gorm:"index"`
	SessionID string `gorm:"index;unique"`
	History   []byte // JSON-encoded chat history
}
