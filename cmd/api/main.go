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

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		log.Fatal("GOOGLE_CLOUD_PROJECT environment variable is not set")
	}

	database.InitDB()

	// Initialize external services clients
	stripePublicKey := os.Getenv("STRIPE_PUBLIC_KEY")
	stripeSecretKey := os.Getenv("STRIPE_SECRET_KEY")
	stripeService := services.NewStripeService(stripePublicKey, stripeSecretKey)

	genaiClient, err := genai.NewClient(ctx, option.WithAPIKey(genai_apiKey))
	if err != nil {
		log.Fatalf("Failed to create GenAI client: %v", err)
	}
	defer genaiClient.Close()

	// Initial paramters for services
	arxivBaseURL := "https://arxiv.org/pdf/"
	cacheExpirationTime := 10 * time.Minute
	cacheExtendPeriod := 5 * time.Minute
	heartbeatTimeout := 1 * time.Minute
	sessionTimeout := 10 * time.Minute
	gcsBucketName := os.Getenv("GCS_BUCKET_NAME")
	if gcsBucketName == "" {
		log.Fatal("GCS_BUCKET_NAME environment variable is not set")
	}

	// Initialize Internal services
	chatServiceDB := services.NewChatServiceDB(database.DB)
	cacheServiceDB := services.NewCacheServiceDB(database.DB)
	contentAggregationService := services.NewContentAggregationService(arxivBaseURL)
	cacheManagementService := services.NewCacheManagementService(
		genaiClient,
		contentAggregationService,
		cacheExpirationTime,
		cacheExtendPeriod,
		cacheServiceDB,
	)

	userService := services.NewUserService(database.DB, cacheManagementService)

	chatSessionService := services.NewChatSessionService(
		genaiClient,
		chatServiceDB,
		cacheManagementService,
		heartbeatTimeout,
		sessionTimeout,
	)
	// Initialize GCS service
	gcsService, err := services.NewGCSService(ctx)
	if err != nil {
		log.Fatalf("Failed to create GCS service: %v", err)
	}

	researchChatService := services.NewResearchChatService(
		contentAggregationService,
		cacheManagementService,
		chatSessionService,
		chatServiceDB,
		cacheExpirationTime,
		gcsService,
		gcsBucketName,
	)

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
	wsHandler := wsocket.NewHandler(researchChatService, upgrader)

	api.SetupRoutes(r, researchChatService, chatServiceDB, stripeService, cacheManagementService, userService)
	auth.SetupRoutes(r, userService)

	// Add WebSocket route
	r.GET("/ws", auth.AuthMiddleware(userService), func(c *gin.Context) {
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
