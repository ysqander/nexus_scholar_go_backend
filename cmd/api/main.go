package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"nexus_scholar_go_backend/internal/api"
	"nexus_scholar_go_backend/internal/auth"
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/services"
	"nexus_scholar_go_backend/internal/wsocket"

	"github.com/gorilla/websocket"

	"cloud.google.com/go/storage"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"github.com/joho/godotenv"
	"google.golang.org/api/option"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	genai_apiKey := os.Getenv("GOOGLE_AI_STUDIO_API_KEY")
	if genai_apiKey == "" {
		log.Fatal("GOOGLE_AI_STUDIO_API_KEY is not set in the environment")
	}

	ctx := context.Background()

	credentialsFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if credentialsFile == "" {
		log.Fatal("GOOGLE_APPLICATION_CREDENTIALS environment variable is not set")
	}

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		log.Fatal("GOOGLE_CLOUD_PROJECT environment variable is not set")
	}

	database.InitDB()

	genaiClient, err := genai.NewClient(ctx, option.WithAPIKey(genai_apiKey))
	if err != nil {
		log.Fatalf("Failed to create GenAI client: %v", err)
	}
	defer genaiClient.Close()

	// Initialize Storage client
	storageClient, err := storage.NewClient(ctx, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		log.Fatalf("Failed to create Storage client: %v", err)
	}
	defer storageClient.Close()

	cacheService, err := services.NewCacheService(ctx, genaiClient, storageClient, projectID)
	if err != nil {
		log.Fatalf("Failed to create CacheService: %v", err)
	}

	r := gin.Default()

	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		allowedOrigins = "http://localhost:5173" // Default to your local frontend
	}

	// CORS middleware configuration
	r.Use(cors.New(cors.Config{
		AllowOrigins:     strings.Split(allowedOrigins, ","),
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// WebSocket upgrader
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // TODO: Implement a more secure check in production
		},
	}

	// Create WebSocket handler
	wsHandler := wsocket.NewHandler(cacheService, upgrader)

	api.SetupRoutes(r, cacheService)
	auth.SetupRoutes(r)

	// Add WebSocket route
	r.GET("/ws", auth.AuthMiddleware(), func(c *gin.Context) {
		user, _ := c.Get("user")
		wsHandler.HandleWebSocket(c.Writer, c.Request, user)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	log.Printf("Server starting on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
