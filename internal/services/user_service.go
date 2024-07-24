package services

import (
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"
)

func CreateOrUpdateUser(auth0ID, email, name, nickname string) (*models.User, error) {
	user := models.User{
		Auth0ID:  auth0ID,
		Email:    email,
		Name:     name,
		Nickname: nickname,
	}
	result := database.DB.Where(models.User{Auth0ID: auth0ID}).FirstOrCreate(&user)

	if result.Error != nil {
		return nil, result.Error
	}

	return &user, nil
}

func GetUserByAuth0ID(auth0ID string) (*models.User, error) {
	var user models.User
	result := database.DB.Where("auth0_id = ?", auth0ID).First(&user)
	if result.Error != nil {
		return nil, result.Error
	}
	return &user, nil
}
