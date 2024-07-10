package database

import (
	"log"

	"nexus_scholar_go_backend/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() {
    var err error
    DB, err = gorm.Open(sqlite.Open("nexus_scholar.db"), &gorm.Config{})
    if err != nil {
        log.Fatal("Failed to connect to database:", err)
    }

    // Auto Migrate the schema
    err = DB.AutoMigrate(&models.User{})
    if err != nil {
        log.Fatal("Failed to auto migrate:", err)
    }
}