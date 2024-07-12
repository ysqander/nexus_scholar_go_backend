package api

import (
	"log"
	"net/http"

	"nexus_scholar_go_backend/internal/auth"
	"nexus_scholar_go_backend/internal/services"

	"github.com/gin-gonic/gin"
)

func SetupRoutes(r *gin.Engine) {
	api := r.Group("/api")
	{
		api.GET("/papers/:arxiv_id", auth.AuthMiddleware(), getPaper)
		api.GET("/papers/:arxiv_id/title", auth.AuthMiddleware(), getPaperTitle)
		api.GET("/private", auth.AuthMiddleware(), privateRoute)
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
