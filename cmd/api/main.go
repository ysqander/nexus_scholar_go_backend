package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"nexus_scholar_go_backend/cmd/api/config"
	"nexus_scholar_go_backend/internal/api"
	"nexus_scholar_go_backend/internal/auth"
	"nexus_scholar_go_backend/internal/broker"
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"
	"nexus_scholar_go_backend/internal/services"
	"nexus_scholar_go_backend/internal/wsocket"

	"cloud.google.com/go/storage"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"github.com/joho/godotenv"
	"google.golang.org/api/option"
)

func main() {
	// Initialize zerolog
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log := zerolog.New(os.Stdout).With().Timestamp().Logger()

	if os.Getenv("GO_ENV") == "production" {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		gin.SetMode(gin.ReleaseMode)
	} else {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		log = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	}

	if err := godotenv.Load(); err != nil {
		log.Warn().Err(err).Msg("No .env file found")
	}

	messageBroker := broker.NewBroker()
	genai_apiKey := os.Getenv("GOOGLE_AI_STUDIO_API_KEY")
	if genai_apiKey == "" {
		log.Fatal().Msg("GOOGLE_AI_STUDIO_API_KEY is not set in the environment")
	}

	ctx := context.Background()

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		log.Fatal().Msg("GOOGLE_CLOUD_PROJECT environment variable is not set")
	}

	if err := database.InitDB(); err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize database")
	}

	// Initialize external services clients
	stripePublicKey := os.Getenv("STRIPE_PUBLIC_KEY")
	stripeSecretKey := os.Getenv("STRIPE_SECRET_KEY")
	stripeService := services.NewStripeService(stripePublicKey, stripeSecretKey, log)

	genaiClient, err := genai.NewClient(ctx, option.WithAPIKey(genai_apiKey))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create GenAI client")
	}
	defer genaiClient.Close()

	// Initial parameters for services
	cfg := config.NewConfig()
	arxivBaseURL := "https://arxiv.org/pdf/"

	gcsBucketName := os.Getenv("GCS_BUCKET_NAME")
	if gcsBucketName == "" {
		log.Fatal().Msg("GCS_BUCKET_NAME environment variable is not set")
	}

	// Initialize Internal services
	chatServiceDB := services.NewChatServiceDB(database.DB)
	cacheServiceDB := services.NewCacheServiceDB(database.DB)
	contentAggregationService := services.NewContentAggregationService(arxivBaseURL, log)
	cacheManagementService := services.NewCacheManagementService(
		genaiClient,
		contentAggregationService,
		cfg.CacheExpirationTime,
		cacheServiceDB,
		chatServiceDB,
		log,
	)

	userService := services.NewUserService(database.DB, cacheManagementService, log)

	chatSessionService := services.NewChatSessionService(
		genaiClient,
		chatServiceDB,
		cacheServiceDB,
		cacheManagementService,
		cfg,
		log,
	)

	// Initialize GCS service
	gcsService, err := services.NewGCSService(ctx, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create GCS service")
	}

	// Check and create bucket if it doesn't exist
	if err := checkAndCreateBucket(ctx, gcsBucketName, gcsService.Client); err != nil {
		log.Fatal().Err(err).Msg("Failed to check/create GCS bucket")
	}

	researchChatService := services.NewResearchChatService(
		contentAggregationService,
		cacheManagementService,
		chatSessionService,
		chatServiceDB,
		cacheServiceDB,
		cfg.CacheExpirationTime,
		gcsService,
		gcsBucketName,
		log,
	)

	// Initialize Gin router
	r := gin.New()

	// Use custom recovery middleware
	r.Use(customRecoveryMiddleware())

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

	// Global OPTIONS handler
	r.OPTIONS("/*path", func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", c.GetHeader("Origin"))
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
		c.Header("Access-Control-Max-Age", "86400") // 24 hours
		c.Status(http.StatusOK)
	})

	// WebSocket upgrader
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // TODO: Implement a more secure check in production
		},
	}

	// Create WebSocket handler
	wsHandler := wsocket.NewHandler(researchChatService, upgrader, cfg.SessionCheckInterval, cfg.SessionMemoryTimeout, log)

	r.Use(loggingMiddleware(log))
	r.Use(setSessionID())
	r.Use(func(c *gin.Context) {
		log.Debug().Msgf("Incoming request: %s %s", c.Request.Method, c.Request.URL.Path)
		log.Debug().Msgf("Headers: %v", c.Request.Header)
		c.Next()
	})

	api.SetupRoutes(r, researchChatService, chatServiceDB, stripeService, cacheManagementService, userService, messageBroker, log)
	auth.SetupRoutes(r, userService)

	// Add WebSocket route
	r.GET("/ws", auth.AuthMiddleware(userService), func(c *gin.Context) {
		user, _ := c.Get("user")
		wsHandler.HandleWebSocket(c.Writer, c.Request, user, messageBroker)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	address := fmt.Sprintf(":%s", port)
	log.Info().Msgf("Server starting on %s", address)

	server := &http.Server{
		Addr:    address,
		Handler: r,
	}

	log.Info().Msgf("Starting server on %s", address)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal().Err(err).Msg("Server error occurred")
	}
}

func checkAndCreateBucket(ctx context.Context, bucketName string, client *storage.Client) error {
	log := zerolog.Ctx(ctx)
	bucket := client.Bucket(bucketName)
	_, err := bucket.Attrs(ctx)
	if err == storage.ErrBucketNotExist {
		log.Info().Msgf("Bucket %s does not exist. Creating...", bucketName)
		if err := bucket.Create(ctx, os.Getenv("GOOGLE_CLOUD_PROJECT"), nil); err != nil {
			return fmt.Errorf("failed to create bucket: %w", err)
		}
		log.Info().Msgf("Bucket %s created successfully", bucketName)
	} else if err != nil {
		return fmt.Errorf("failed to get bucket attributes: %w", err)
	}

	return nil
}

func customRecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log := zerolog.Ctx(c.Request.Context())
				log.Error().
					Interface("error", err).
					Str("stack", string(debug.Stack())).
					Msg("Panic recovered")
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}

func loggingMiddleware(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Process request
		c.Next()

		// Logging after request is processed
		userID := "unknown"
		if user, exists := c.Get("user"); exists {
			if userModel, ok := user.(*models.User); ok {
				userID = userModel.ID.String()
			}
		}

		sessionID := "n/a"
		if sid, exists := c.Get("sessionID"); exists {
			sessionID = sid.(string)
		}

		// Determine log level based on status code
		var event *zerolog.Event
		switch {
		case c.Writer.Status() >= 500:
			event = log.Error()
		case c.Writer.Status() >= 400:
			event = log.Warn()
		default:
			event = log.Info()
		}

		event.
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", c.Writer.Status()).
			Str("client_ip", c.ClientIP()).
			Str("user_id", userID).
			Str("session_id", sessionID).
			Msg("HTTP Request")
	}
}

func setSessionID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if session ID is in the request (header or query param)
		sessionID := c.GetHeader("X-Session-ID")
		if sessionID == "" {
			sessionID = c.Query("session_id")
		}

		// If still not found and it's a POST request, check the body
		if sessionID == "" && c.Request.Method == "POST" {
			bodyBytes, err := c.GetRawData()
			if err == nil {
				// Reset the request body so it can be read again by later handlers
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

				// Try to parse as JSON
				var jsonBody struct {
					SessionID string `json:"session_id"`
				}
				if json.Unmarshal(bodyBytes, &jsonBody) == nil {
					sessionID = jsonBody.SessionID
				}

				// If not found in JSON, check if it's in form data
				if sessionID == "" {
					form, err := url.ParseQuery(string(bodyBytes))
					if err == nil {
						sessionID = form.Get("session_id")
					}
				}
			}
		}

		if sessionID != "" {
			c.Set("sessionID", sessionID)
		}

		c.Next()
	}
}
