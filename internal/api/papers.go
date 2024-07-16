package api

import (
	"io"
	"log"
	"net/http"
	"time"

	"nexus_scholar_go_backend/internal/auth"
	"nexus_scholar_go_backend/internal/services"

	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
)

func SetupRoutes(r *gin.Engine, cacheService *services.CacheService) {
	api := r.Group("/api")
	{
		api.GET("/papers/:arxiv_id", auth.AuthMiddleware(), getPaper)
		api.GET("/papers/:arxiv_id/title", auth.AuthMiddleware(), getPaperTitle)
		api.GET("/private", auth.AuthMiddleware(), privateRoute)
		api.POST("/create-cache", auth.AuthMiddleware(), createCacheHandler(cacheService))
		api.DELETE("/cache/:cacheId", auth.AuthMiddleware(), deleteCacheHandler(cacheService))
		api.POST("/chat/start", auth.AuthMiddleware(), startChatHandler(cacheService))
		api.POST("/chat/message", auth.AuthMiddleware(), sendChatMessageHandler(cacheService))
		api.POST("/chat/stream", auth.AuthMiddleware(), streamChatMessageHandler(cacheService))
		api.POST("/chat/terminate", auth.AuthMiddleware(), terminateChatSessionHandler(cacheService))
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
		var request struct {
			ArxivIDs []string `json:"arxiv_ids" binding:"required"`
			UserPDFs []string `json:"user_pdfs"`
		}

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		cacheExpirationTTL := 10 * time.Minute
		cachedContentName, err := cacheService.CreateContentCache(c.Request.Context(), request.ArxivIDs, request.UserPDFs, cacheExpirationTTL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"cached_content_name": cachedContentName})
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

func startChatHandler(cacheService *services.CacheService) gin.HandlerFunc {
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

func sendChatMessageHandler(cacheService *services.CacheService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			SessionID string `json:"session_id" binding:"required"`
			Message   string `json:"message" binding:"required"`
		}

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		response, err := cacheService.SendChatMessage(c.Request.Context(), request.SessionID, request.Message)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Assuming the response structure is the same as before
		c.JSON(http.StatusOK, gin.H{"response": response.Candidates[0].Content.Parts[0]})
	}
}

func streamChatMessageHandler(cacheService *services.CacheService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			SessionID string `json:"session_id" binding:"required"`
			Message   string `json:"message" binding:"required"`
		}

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		responseIterator, err := cacheService.StreamChatMessage(c.Request.Context(), request.SessionID, request.Message)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.Stream(func(w io.Writer) bool {
			response, err := responseIterator.Next()
			if err == iterator.Done {
				return false
			}
			if err != nil {
				log.Printf("Error streaming response: %v", err)
				return false
			}

			c.SSEvent("message", response.Candidates[0].Content.Parts[0])
			return true
		})
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
