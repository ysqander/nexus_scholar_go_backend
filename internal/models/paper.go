package models

import "gorm.io/gorm"

type Paper struct {
	gorm.Model
	Title      string
	Authors    string
	Abstract   string
	ArxivID    string `gorm:"unique"`
	URL        string
	References []Reference
}
