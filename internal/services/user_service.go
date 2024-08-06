package services

import (
	"context"
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	db                     *gorm.DB
	cacheManagementService *CacheManagementService
)

func InitUserService(database *gorm.DB, cms *CacheManagementService) {
	db = database
	cacheManagementService = cms
}

func CreateOrUpdateUser(ctx context.Context, auth0ID, email, name, nickname string) (*models.User, error) {
	var user models.User
	result := db.Where("auth0_id = ?", auth0ID).First(&user)

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			// Create new user
			user = models.User{
				Auth0ID:  auth0ID,
				Email:    email,
				Name:     name,
				Nickname: nickname,
			}
			if err := db.Create(&user).Error; err != nil {
				return nil, err
			}

			// Initialize cache usage for new user
			if err := initializeCacheUsage(ctx, user.ID); err != nil {
				return nil, err
			}
		} else {
			return nil, result.Error
		}
	} else {
		// Update existing user
		user.Email = email
		user.Name = name
		user.Nickname = nickname
		if err := db.Save(&user).Error; err != nil {
			return nil, err
		}
	}

	return &user, nil
}

func initializeCacheUsage(ctx context.Context, userID uuid.UUID) error {
	// Initialize with 2 pro token hours and 5 base token hours
	err := cacheManagementService.UpdateAllowedCacheUsage(ctx, userID, "pro", 2)
	if err != nil {
		return err
	}

	err = cacheManagementService.UpdateAllowedCacheUsage(ctx, userID, "base", 5)
	if err != nil {
		return err
	}

	return nil
}

func GetUserByAuth0ID(auth0ID string) (*models.User, error) {
	var user models.User
	result := database.DB.Where("auth0_id = ?", auth0ID).First(&user)
	if result.Error != nil {
		return nil, result.Error
	}
	return &user, nil
}
