package database

import (
	"fmt"
	"log"
	"os"

	"nexus_scholar_go_backend/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() {

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=UTC",
		os.Getenv("DB_HOST"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
		os.Getenv("DB_PORT"),
	)

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	// Auto Migrate the schema
	err = DB.AutoMigrate(&models.User{}, &models.Paper{}, &models.PaperReference{}, &models.Chat{}, &models.Message{}, &models.Cache{}, &models.TierTokenBudget{})
	if err != nil {
		log.Fatal("Failed to auto migrate:", err)
	}

}
