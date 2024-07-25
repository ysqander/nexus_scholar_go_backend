package models

import (
	"gorm.io/gorm"
)

type PaperReference struct {
	gorm.Model
	ArxivID            string `gorm:"type:varchar(20);index"`
	Type               string
	Key                string
	Title              string
	Author             string
	Year               string
	Journal            string
	Volume             string
	Number             string
	Pages              string
	Publisher          string
	DOI                string
	URL                string
	RawBibEntry        string
	FormattedText      string
	IsAvailableOnArxiv bool
}
