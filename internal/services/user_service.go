package services

import (
	"context"
	"fmt"
	"nexus_scholar_go_backend/internal/models"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

type UserService struct {
	db                     *gorm.DB
	cacheManagementService *CacheManagementService
	logger                 zerolog.Logger
}

func NewUserService(db *gorm.DB, cms *CacheManagementService, logger zerolog.Logger) *UserService {
	return &UserService{
		db:                     db,
		cacheManagementService: cms,
		logger:                 logger,
	}
}

func (s *UserService) CreateOrUpdateUser(ctx context.Context, auth0ID, email, name, nickname string) (*models.User, error) {
	s.logger.Info().Msgf("Creating or updating user with Auth0ID: %s", auth0ID)

	var user models.User
	result := s.db.Where("auth0_id = ?", auth0ID).First(&user)

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			s.logger.Info().Msgf("User not found. Creating new user with Auth0ID: %s", auth0ID)
			// Create new user
			user = models.User{
				Auth0ID:  auth0ID,
				Email:    email,
				Name:     name,
				Nickname: nickname,
			}
			if err := s.db.Create(&user).Error; err != nil {
				s.logger.Error().Err(err).Msgf("Failed to create new user with Auth0ID: %s", auth0ID)
				return nil, fmt.Errorf("failed to create new user: %w", err)
			}
			// Initialize cache usage for new user
			if err := s.initializeCacheAllocation(ctx, user.ID); err != nil {
				s.logger.Error().Err(err).Msgf("Failed to initialize cache allocation for user with ID: %s", user.ID)
				return nil, fmt.Errorf("failed to initialize cache allocation: %w", err)
			}
		} else {
			s.logger.Error().Err(result.Error).Msgf("Error retrieving user with Auth0ID: %s", auth0ID)
			return nil, fmt.Errorf("error retrieving user: %w", result.Error)
		}
	} else {
		// Update existing user if needed
		if user.Email != email || user.Name != name || user.Nickname != nickname {
			s.logger.Info().Msgf("Updating existing user with Auth0ID: %s", auth0ID)
			user.Email = email
			user.Name = name
			user.Nickname = nickname
			if err := s.db.Save(&user).Error; err != nil {
				s.logger.Error().Err(err).Msgf("Failed to update user with Auth0ID: %s", auth0ID)
				return nil, fmt.Errorf("failed to update user: %w", err)
			}
		}
	}

	s.logger.Info().Msgf("Successfully created or updated user with Auth0ID: %s", auth0ID)
	return &user, nil
}

func (s *UserService) initializeCacheAllocation(ctx context.Context, userID uuid.UUID) error {
	s.logger.Info().Msgf("Initializing cache allocation for user with ID: %s", userID)

	// Initialize with 2 pro token hours
	err := s.cacheManagementService.UpdateAllowedCacheUsage(ctx, userID, "pro", 2)
	if err != nil {
		s.logger.Error().Err(err).Msgf("Failed to initialize pro cache usage for user %s", userID)
		return fmt.Errorf("failed to initialize pro cache usage: %w", err)
	}
	s.logger.Info().Msgf("Initialized pro cache usage for user %s", userID)

	// Initialize with 5 base token hours
	err = s.cacheManagementService.UpdateAllowedCacheUsage(ctx, userID, "base", 5)
	if err != nil {
		s.logger.Error().Err(err).Msgf("Failed to initialize base cache usage for user %s", userID)
		return fmt.Errorf("failed to initialize base cache usage: %w", err)
	}
	s.logger.Info().Msgf("Initialized base cache usage for user %s", userID)

	return nil
}

func (s *UserService) GetUserByAuth0ID(auth0ID string) (*models.User, error) {
	s.logger.Info().Msgf("Retrieving user with Auth0ID: %s", auth0ID)

	var user models.User
	result := s.db.Where("auth0_id = ?", auth0ID).First(&user)
	if result.Error != nil {
		s.logger.Error().Err(result.Error).Msgf("Failed to retrieve user with Auth0ID: %s", auth0ID)
		return nil, fmt.Errorf("failed to retrieve user: %w", result.Error)
	}

	s.logger.Info().Msgf("Successfully retrieved user with Auth0ID: %s", auth0ID)
	return &user, nil
}
