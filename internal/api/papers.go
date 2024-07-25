package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"nexus_scholar_go_backend/internal/auth"
	"nexus_scholar_go_backend/internal/models"
	"nexus_scholar_go_backend/internal/services"

	"github.com/gin-gonic/gin"
)

func SetupRoutes(r *gin.Engine, cacheService *services.CacheService) {
	api := r.Group("/api")
	{
		api.GET("/papers/:arxiv_id", auth.AuthMiddleware(), getPaper)
		api.GET("/papers/:arxiv_id/title", auth.AuthMiddleware(), getPaperTitle)
		api.GET("/private", auth.AuthMiddleware(), privateRoute)
		api.POST("/create-cache", auth.AuthMiddleware(), createCacheHandler(cacheService))
		api.DELETE("/cache/:cacheId", auth.AuthMiddleware(), deleteCacheHandler(cacheService))
		api.POST("/chat/start", auth.AuthMiddleware(), startChatSessionHandler(cacheService))
		api.POST("/chat/terminate", auth.AuthMiddleware(), terminateChatSessionHandler(cacheService))
		api.GET("/chat/history", auth.AuthMiddleware(), getChatHistoryHandler())
	}
}

func getPaper(c *gin.Context) {
	arxivID := c.Param("arxiv_id")
	log.Printf("Received request for arXiv ID: %s", arxivID)

	paperLoader := services.NewPaperLoader()
	result, err := paperLoader.ProcessPaper(arxivID)
	if err != nil {
		log.Printf("Error fetching paper for arXiv ID %s: %v", arxivID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("Successfully fetched paper data for arXiv ID: %s", arxivID)
	c.JSON(http.StatusOK, result)
}
func getPaperTitle(c *gin.Context) {
	arxivID := c.Param("arxiv_id")
	log.Printf("Received request for arXiv ID title: %s", arxivID)

	paperLoader := services.NewPaperLoader()
	metadata, err := paperLoader.GetPaperMetadata(arxivID)
	if err != nil {
		log.Printf("Error fetching paper metadata for arXiv ID %s: %v", arxivID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("Successfully fetched paper title for arXiv ID: %s", arxivID)
	c.JSON(http.StatusOK, gin.H{"title": metadata["title"]})
}

func privateRoute(c *gin.Context) {
	user, _ := c.Get("user")
	c.JSON(http.StatusOK, gin.H{
		"message": "This is a private route",
		"user":    user,
	})
}

func createCacheHandler(cacheService *services.CacheService) gin.HandlerFunc {
	return func(c *gin.Context) {

		// Get arXiv IDs
		arxivIDsJSON := c.PostForm("arxiv_ids")
		var arxivIDs []string
		if err := json.Unmarshal([]byte(arxivIDsJSON), &arxivIDs); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid arXiv IDs format"})
			return
		}

		// Create a temporary directory to store uploaded files
		tempDir, err := os.MkdirTemp("", "user_pdfs_")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create temporary directory"})
			return
		}
		defer os.RemoveAll(tempDir) // Clean up the temporary directory when done

		// Save uploaded files and collect their paths
		var pdfPaths []string
		form, err := c.MultipartForm()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Failed to parse multipart form: %v", err)})
			return
		}
		files := form.File["pdfs"]
		for _, fileHeader := range files {
			filename := filepath.Join(tempDir, fileHeader.Filename)
			if err := c.SaveUploadedFile(fileHeader, filename); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save file %s: %v", fileHeader.Filename, err)})
				return
			}
			pdfPaths = append(pdfPaths, filename)
		}

		log.Printf("Received %d arXiv IDs and %d PDF files", len(arxivIDs), len(pdfPaths))

		cacheExpirationTTL := 10 * time.Minute
		cachedContentName, err := cacheService.CreateContentCache(c.Request.Context(), arxivIDs, pdfPaths, cacheExpirationTTL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Wrap the rest of the process in a defer function to ensure cache cleanup on error
		var processingErr error
		defer func() {
			if processingErr != nil {
				// An error occurred, attempt to delete the cache
				if delErr := cacheService.DeleteCache(c.Request.Context(), cachedContentName); delErr != nil {
					log.Printf("Failed to delete cache %s after error: %v", cachedContentName, delErr)
				} else {
					log.Printf("Successfully deleted cache %s after error", cachedContentName)
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": processingErr.Error()})
			}
		}()

		// Start chat session
		sessionID, err := cacheService.StartChatSession(c.Request.Context(), cachedContentName)
		if err != nil {
			processingErr = fmt.Errorf("failed to start chat session: %v", err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"cached_content_name": cachedContentName,
			"session_id":          sessionID,
		})
	}
}

func deleteCacheHandler(cacheService *services.CacheService) gin.HandlerFunc {
	return func(c *gin.Context) {
		cacheID := c.Param("cacheId")

		err := cacheService.DeleteCache(c.Request.Context(), cacheID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Cache deleted successfully"})
	}
}

func startChatSessionHandler(cacheService *services.CacheService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			CachedContentName string `json:"cached_content_name" binding:"required"`
		}

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		sessionID, err := cacheService.StartChatSession(c.Request.Context(), request.CachedContentName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"session_id": sessionID})
	}
}

func terminateChatSessionHandler(cacheService *services.CacheService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			SessionID string `json:"session_id" binding:"required"`
		}

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		err := cacheService.TerminateSession(c.Request.Context(), request.SessionID)
		if err != nil {
			// This should now only happen for unexpected errors
			c.JSON(http.StatusInternalServerError, gin.H{"error": "An unexpected error occurred"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Chat session terminated successfully"})
	}
}

func getChatHistoryHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		log.Println("getChatHistoryHandler called")

		user, exists := c.Get("user")
		if !exists {
			log.Println("User not found in context")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
			return
		}

		userModel, ok := user.(*models.User)
		if !ok {
			log.Println("Failed to cast user to *models.User")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cast user to *models.User"})
			return
		}

		log.Printf("Retrieving chat history for user ID: %d", userModel.ID.String())
		chats, err := services.GetChatsByUserID(userModel.ID)
		if err != nil {
			log.Printf("Failed to retrieve chat history for user ID %d: %v", userModel.ID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to retrieve chat history: %v", err)})
			return
		}

		// Process chats to return a more user-friendly format
		var chatHistory []gin.H
		for _, chat := range chats {
			var messages []map[string]interface{}
			err := json.Unmarshal(chat.History, &messages)
			if err != nil {
				log.Printf("Error unmarshaling chat history for session %s: %v", chat.SessionID, err)
				continue
			}

			chatHistory = append(chatHistory, gin.H{
				"session_id": chat.SessionID,
				"messages":   messages,
				"created_at": chat.CreatedAt,
			})
		}

		log.Printf("Successfully retrieved chat history for user ID: %d", userModel.ID)
		c.JSON(http.StatusOK, gin.H{"chat_history": chatHistory})
	}
}
