package services

import (
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"
	"strings"
)

// CreateOrUpdatePaper creates a new paper or updates an existing one in the database
func CreateOrUpdatePaper(paperData map[string]interface{}) (*models.Paper, error) {
	arxivID := paperData["arxiv_id"].(string)

	var paper models.Paper
	result := database.DB.Where("arxiv_id = ?", arxivID).First(&paper)

	if result.Error != nil {
		// Paper doesn't exist, create a new one
		paper = models.Paper{
			Title:    paperData["title"].(string),
			Authors:  strings.Join(paperData["authors"].([]string), ", "),
			Abstract: paperData["abstract"].(string),
			ArxivID:  arxivID,
			URL:      paperData["pdf_url"].(string),
		}
		if err := database.DB.Create(&paper).Error; err != nil {
			return nil, err
		}
	} else {
		// Paper exists, update it
		paper.Title = paperData["title"].(string)
		paper.Authors = strings.Join(paperData["authors"].([]string), ", ")
		paper.Abstract = paperData["abstract"].(string)
		paper.URL = paperData["pdf_url"].(string)
		if err := database.DB.Save(&paper).Error; err != nil {
			return nil, err
		}
	}

	return &paper, nil
}

// GetPaperByID retrieves a paper from the database by its ID
func GetPaperByID(id uint) (*models.Paper, error) {
	var paper models.Paper
	result := database.DB.First(&paper, id)
	if result.Error != nil {
		return nil, result.Error
	}
	return &paper, nil
}

// DeletePaper deletes a paper from the database
func DeletePaper(id uint) error {
	result := database.DB.Delete(&models.Paper{}, id)
	return result.Error
}
