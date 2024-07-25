package services

import (
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"
)

// CreateReference creates a new reference in the database
func CreateReference(reference *models.PaperReference) error {
	result := database.DB.Create(reference)
	return result.Error
}

// Retrieves all references for a given Arxiv paper ID
func GetReferencesByArxivID(arxivID string) ([]models.PaperReference, error) {
	var references []models.PaperReference
	result := database.DB.Where("arxiv_id = ?", arxivID).Find(&references)
	if result.Error != nil {
		return nil, result.Error
	}
	return references, nil
}

// UpdateReference updates an existing reference in the database
func UpdateReference(reference *models.PaperReference) error {
	result := database.DB.Save(reference)
	return result.Error
}

// DeleteReference deletes a reference from the database
func DeleteReference(id uint) error {
	result := database.DB.Delete(&models.PaperReference{}, id)
	return result.Error
}

// DeleteReferencesByArxivID deletes all references associated with a paper
func DeleteReferencesByArxivID(arxivID string) error {
	result := database.DB.Where("arxiv_id = ?", arxivID).Delete(&models.PaperReference{})
	return result.Error
}
