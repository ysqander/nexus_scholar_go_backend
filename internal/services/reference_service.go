package services

import (
	"errors"
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"

	"gorm.io/gorm"
)

func CreateOrUpdateReference(reference *models.PaperReference) error {
	var existingRef models.PaperReference
	result := database.DB.Where("parent_arxiv_id = ? AND key = ?", reference.ParentArxivID, reference.Key).First(&existingRef)

	if result.Error == nil {
		// Reference exists, check if any fields have changed
		if hasChanged(&existingRef, reference) {
			// Update only if there are changes
			existingRef.ArxivID = reference.ArxivID
			existingRef.Type = reference.Type
			existingRef.Title = reference.Title
			existingRef.Author = reference.Author
			existingRef.Year = reference.Year
			existingRef.Journal = reference.Journal
			existingRef.Volume = reference.Volume
			existingRef.Number = reference.Number
			existingRef.Pages = reference.Pages
			existingRef.Publisher = reference.Publisher
			existingRef.DOI = reference.DOI
			existingRef.URL = reference.URL
			existingRef.RawBibEntry = reference.RawBibEntry
			existingRef.FormattedText = reference.FormattedText
			existingRef.IsAvailableOnArxiv = reference.IsAvailableOnArxiv

			return database.DB.Save(&existingRef).Error
		}
		// No changes, do nothing
		return nil
	} else if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// Reference doesn't exist, create a new one
		return database.DB.Create(reference).Error
	} else {
		// Some other error occurred
		return result.Error
	}
}

// hasChanged checks if any fields in the new reference differ from the existing one
func hasChanged(existing, new *models.PaperReference) bool {
	return existing.ArxivID != new.ArxivID ||
		existing.Type != new.Type ||
		existing.Title != new.Title ||
		existing.Author != new.Author ||
		existing.Year != new.Year ||
		existing.Journal != new.Journal ||
		existing.Volume != new.Volume ||
		existing.Number != new.Number ||
		existing.Pages != new.Pages ||
		existing.Publisher != new.Publisher ||
		existing.DOI != new.DOI ||
		existing.URL != new.URL ||
		existing.RawBibEntry != new.RawBibEntry ||
		existing.FormattedText != new.FormattedText ||
		existing.IsAvailableOnArxiv != new.IsAvailableOnArxiv
}

// Retrieves all references for a given Arxiv paper ID
func GetReferencesByArxivID(arxivID string) ([]models.PaperReference, error) {
	var references []models.PaperReference
	result := database.DB.Where("parent_arxiv_id = ?", arxivID).Find(&references)
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

// get reference by its own arxiv id
func GetReferenceByArxivID(arxivID string) (*models.PaperReference, error) {
	var reference models.PaperReference
	result := database.DB.Where("arxiv_id = ?", arxivID).First(&reference)
	if result.Error != nil {
		return nil, result.Error
	}
	return &reference, nil
}
