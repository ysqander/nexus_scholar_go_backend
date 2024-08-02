package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"nexus_scholar_go_backend/internal/auth"
	"nexus_scholar_go_backend/internal/models"
	"nexus_scholar_go_backend/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/iterator"
)

func SetupRoutes(r *gin.Engine, researchChatService *services.ResearchChatService, chatService services.ChatServiceDB) {
	api := r.Group("/api")
	{
		api.GET("/papers/:arxiv_id", auth.AuthMiddleware(), getPaper)
		api.GET("/papers/:arxiv_id/title", auth.AuthMiddleware(), getPaperTitle)
		api.GET("/private", auth.AuthMiddleware(), privateRoute)
		api.POST("/create-research-session", auth.AuthMiddleware(), createResearchSessionHandler(researchChatService))
		api.POST("/chat/message", auth.AuthMiddleware(), sendChatMessageHandler(researchChatService))
		api.POST("/chat/terminate", auth.AuthMiddleware(), terminateChatSessionHandler(researchChatService))
		api.GET("/chat/history", auth.AuthMiddleware(), getChatHistoryHandler(researchChatService))
	}
}

func getPaper(c *gin.Context) {
	arxivID := c.Param("arxiv_id")

	paperLoader := services.NewPaperLoader()
	result, err := paperLoader.ProcessPaper(arxivID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func getPaperTitle(c *gin.Context) {
	arxivID := c.Param("arxiv_id")

	paperLoader := services.NewPaperLoader()
	metadata, err := paperLoader.GetPaperMetadata(arxivID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"title": metadata["title"]})
}

func privateRoute(c *gin.Context) {
	user, _ := c.Get("user")
	c.JSON(http.StatusOK, gin.H{
		"message": "This is a private route",
		"user":    user,
	})
}

func createResearchSessionHandler(researchChatService *services.ResearchChatService) gin.HandlerFunc {
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

		sessionID, cachedContentName, err := researchChatService.StartResearchSession(c, arxivIDs, pdfPaths)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"cached_content_name": cachedContentName,
			"session_id":          sessionID,
		})
	}
}

func sendChatMessageHandler(researchChatService *services.ResearchChatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			SessionID string `json:"session_id" binding:"required"`
			Message   string `json:"message" binding:"required"`
		}

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		responseIterator, err := researchChatService.SendMessage(c.Request.Context(), request.SessionID, request.Message)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Stream the response back to the client
		c.Stream(func(w io.Writer) bool {
			response, err := responseIterator.Next()
			if err == iterator.Done {
				return false
			}
			if err != nil {
				c.SSEvent("error", err.Error())
				return false
			}

			if len(response.Candidates) > 0 && len(response.Candidates[0].Content.Parts) > 0 {
				content := response.Candidates[0].Content.Parts[0].(genai.Text)
				c.SSEvent("message", string(content))

				// Save AI response
				if err := researchChatService.SaveAIResponse(request.SessionID, string(content)); err != nil {
					c.SSEvent("error", fmt.Sprintf("Failed to save AI response: %v", err))
					return false
				}
			}

			return true
		})
	}
}

func terminateChatSessionHandler(researchChatService *services.ResearchChatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			SessionID string `json:"session_id" binding:"required"`
		}

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		err := researchChatService.EndResearchSession(c.Request.Context(), request.SessionID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "An unexpected error occurred"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Research session terminated successfully"})
	}
}

func getChatHistoryHandler(researchChatService *services.ResearchChatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, exists := c.Get("user")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
			return
		}

		userModel, ok := user.(*models.User)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cast user to *models.User"})
			return
		}

		chats, err := researchChatService.GetUserChatHistory(userModel.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to retrieve chat history: %v", err)})
			return
		}

		// Process chats to return a more user-friendly format
		var chatHistory []gin.H
		for _, chat := range chats {
			messages := make([]gin.H, len(chat.Messages))
			for i, msg := range chat.Messages {
				messages[i] = gin.H{
					"type":      msg.Type,
					"content":   msg.Content,
					"timestamp": msg.Timestamp.Format(time.RFC3339),
				}
			}

			chatHistory = append(chatHistory, gin.H{
				"session_id": chat.SessionID,
				"messages":   messages,
				"created_at": chat.CreatedAt.Format(time.RFC3339),
			})
		}

		c.JSON(http.StatusOK, gin.H{"chat_history": chatHistory})
	}
}
