package services

import (
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"
)

// CreateReference creates a new reference in the database
func CreateReference(reference *models.Reference) error {
	result := database.DB.Create(reference)
	return result.Error
}

// GetReferenceByID retrieves a reference from the database by its ID
func GetReferenceByID(id uint) (*models.Reference, error) {
	var reference models.Reference
	result := database.DB.First(&reference, id)
	if result.Error != nil {
		return nil, result.Error
	}
	return &reference, nil
}

// GetReferencesByPaperID retrieves all references for a given paper ID
func GetReferencesByPaperID(paperID uint) ([]models.Reference, error) {
	var references []models.Reference
	result := database.DB.Where("paper_id = ?", paperID).Find(&references)
	if result.Error != nil {
		return nil, result.Error
	}
	return references, nil
}

// UpdateReference updates an existing reference in the database
func UpdateReference(reference *models.Reference) error {
	result := database.DB.Save(reference)
	return result.Error
}

// DeleteReference deletes a reference from the database
func DeleteReference(id uint) error {
	result := database.DB.Delete(&models.Reference{}, id)
	return result.Error
}

// DeleteReferencesByPaperID deletes all references associated with a paper
func DeleteReferencesByPaperID(paperID uint) error {
	result := database.DB.Where("paper_id = ?", paperID).Delete(&models.Reference{})
	return result.Error
}
