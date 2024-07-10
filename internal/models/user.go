package models

import "gorm.io/gorm"

type User struct {
	gorm.Model
	Auth0ID  string `gorm:"unique;not null"`
	Email    string `gorm:"unique;not null"`
	Name     string
	Nickname string
	// Add any other fields you want to store
}
