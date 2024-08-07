package services

import (
	"context"
	"log"
	"nexus_scholar_go_backend/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type UserService struct {
	db                     *gorm.DB
	cacheManagementService *CacheManagementService
}

func NewUserService(db *gorm.DB, cms *CacheManagementService) *UserService {
	return &UserService{
		db:                     db,
		cacheManagementService: cms,
	}
}

func (s *UserService) CreateOrUpdateUser(ctx context.Context, auth0ID, email, name, nickname string) (*models.User, error) {
	var user models.User
	result := s.db.Where("auth0_id = ?", auth0ID).First(&user)

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			// Create new user
			user = models.User{
				Auth0ID:  auth0ID,
				Email:    email,
				Name:     name,
				Nickname: nickname,
			}
			if err := s.db.Create(&user).Error; err != nil {
				return nil, err
			}
			// Initialize cache usage for new user
			if err := s.initializeCacheAllocation(ctx, user.ID); err != nil {
				return nil, err
			}
		} else {
			return nil, result.Error
		}
	} else {
		// Update existing user if needed
		if user.Email != email || user.Name != name || user.Nickname != nickname {
			user.Email = email
			user.Name = name
			user.Nickname = nickname
			if err := s.db.Save(&user).Error; err != nil {
				return nil, err
			}
		}
	}

	return &user, nil
}

func (s *UserService) initializeCacheAllocation(ctx context.Context, userID uuid.UUID) error {

	// Initialize with 2 pro token hours
	err := s.cacheManagementService.UpdateAllowedCacheUsage(ctx, userID, "pro", 2)
	if err != nil {
		log.Printf("Failed to initialize pro cache usage for user %s: %v", userID, err)
		return err
	}
	log.Printf("Initialized pro cache usage for user %s", userID)

	// Initialize with 5 base token hours
	err = s.cacheManagementService.UpdateAllowedCacheUsage(ctx, userID, "base", 5)
	if err != nil {
		log.Printf("Failed to initialize base cache usage for user %s: %v", userID, err)
		return err
	}
	log.Printf("Initialized base cache usage for user %s", userID)

	return nil
}

func (s *UserService) GetUserByAuth0ID(auth0ID string) (*models.User, error) {
	var user models.User
	result := s.db.Where("auth0_id = ?", auth0ID).First(&user)
	if result.Error != nil {
		return nil, result.Error
	}
	return &user, nil
}
