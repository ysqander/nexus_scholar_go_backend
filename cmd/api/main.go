package main

import (
    "log"
    "os"

    "github.com/gin-gonic/gin"
    "github.com/joho/godotenv"
    "nexus_scholar_go_backend/internal/api"
    "nexus_scholar_go_backend/internal/auth"
)

func main() {
    if err := godotenv.Load(); err != nil {
        log.Println("No .env file found")
    }

    r := gin.Default()

    api.SetupRoutes(r)
    auth.SetupRoutes(r)

    port := os.Getenv("PORT")
    if port == "" {
        port = "3000"
    }

    r.Run(":" + port)
}
