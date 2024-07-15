package api

import (
	"log"
	"net/http"

	"nexus_scholar_go_backend/internal/auth"
	"nexus_scholar_go_backend/internal/services"

	"cloud.google.com/go/storage"
	"cloud.google.com/go/vertexai/genai"
	"github.com/gin-gonic/gin"
)

func SetupRoutes(r *gin.Engine) {
	api := r.Group("/api")
	{
		api.GET("/papers/:arxiv_id", auth.AuthMiddleware(), getPaper)
		api.GET("/papers/:arxiv_id/title", auth.AuthMiddleware(), getPaperTitle)
		api.GET("/private", auth.AuthMiddleware(), privateRoute)
		api.POST("/create-cache", auth.AuthMiddleware(), createCache)
		api.POST("/chat", auth.AuthMiddleware(), chat)
		api.DELETE("/cache/:cacheId", auth.AuthMiddleware(), deleteCache)
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

func createCache(c *gin.Context) {
	var request struct {
		ArxivIDs []string `json:"arxiv_ids" binding:"required"`
		UserPDFs []string `json:"user_pdfs"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Initialize GenAI client
	genaiClient, err := genai.NewClient(c.Request.Context(), "nexus-scholar", "europe-west3")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create GenAI client"})
		return
	}
	defer genaiClient.Close()

	// Initialize Storage client
	storageClient, err := storage.NewClient(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create Storage client"})
		return
	}
	defer storageClient.Close()

	cacheService := services.NewCacheService(genaiClient, storageClient)

	cachedContentName, err := cacheService.CreateContentCache(c.Request.Context(), request.ArxivIDs, request.UserPDFs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"cached_content_name": cachedContentName})
}

func chat(c *gin.Context) {
	// TODO: Implement chat functionality
	c.JSON(http.StatusNotImplemented, gin.H{"error": "Chat functionality not implemented yet"})
}

func deleteCache(c *gin.Context) {
	// TODO: Implement cache deletion
	c.JSON(http.StatusNotImplemented, gin.H{"error": "Cache deletion not implemented yet"})
}
