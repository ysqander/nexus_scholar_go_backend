package models

import "gorm.io/gorm"

type Paper struct {
	gorm.Model
	Title      string
	Authors    string
	Abstract   string
	ArxivID    string `gorm:"type:varchar(20);unique"`
	URL        string
	References []PaperReference
}
