package models

import (
	"gorm.io/gorm"
)

type Reference struct {
	gorm.Model
	ArxivID            uint `gorm:"index"`
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
