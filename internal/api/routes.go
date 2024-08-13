package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"nexus_scholar_go_backend/internal/auth"
	"nexus_scholar_go_backend/internal/models"
	"nexus_scholar_go_backend/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v79"
	"google.golang.org/api/iterator"
)

func SetupRoutes(r *gin.Engine, researchChatService *services.ResearchChatService, chatService services.ChatServiceDB, stripeService *services.StripeService, cacheManagementService *services.CacheManagementService, userService *services.UserService) {
	api := r.Group("/api")
	{
		api.GET("/papers/:arxiv_id", auth.AuthMiddleware(userService), getPaper)
		api.GET("/papers/:arxiv_id/title", auth.AuthMiddleware(userService), getPaperTitle)
		api.GET("/private", auth.AuthMiddleware(userService), privateRoute)
		api.POST("/create-research-session", auth.AuthMiddleware(userService), createResearchSessionHandler(researchChatService))
		api.GET("/raw-cache", auth.AuthMiddleware(userService), getRawCacheHandler(researchChatService))
		api.POST("/chat/message", auth.AuthMiddleware(userService), sendChatMessageHandler(researchChatService))
		api.POST("/chat/terminate", auth.AuthMiddleware(userService), terminateChatSessionHandler(researchChatService))
		api.GET("/chat/history", auth.AuthMiddleware(userService), getChatHistoryHandler(researchChatService))
		api.POST("/purchase-cache-volume", auth.AuthMiddleware(userService), purchaseCacheVolume(stripeService))
		api.GET("/cache-usage", auth.AuthMiddleware(userService), getCacheUsageHandler(cacheManagementService))
		api.POST("/stripe/webhook", stripeWebhookHandler(stripeService, cacheManagementService))
		api.POST("/stripe/webhook_clitest", stripeWebhookHandler_clitest(stripeService, cacheManagementService))
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
	parentArxivID := c.Query("parent_arxiv_id")

	paperLoader := services.NewPaperLoader()
	metadata, err := paperLoader.GetPaperMetadata(arxivID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create a PaperReference
	paperRef := models.PaperReference{
		ArxivID:            arxivID,
		ParentArxivID:      parentArxivID,
		Type:               "article",
		Key:                arxivID,
		Title:              metadata["title"],
		Author:             metadata["authors"],
		Year:               metadata["published_date"][:4], // Assuming the date is in YYYY-MM-DD format
		Journal:            metadata["journal"],            // This might be empty for preprints
		DOI:                metadata["doi"],                // This might be empty for preprints
		URL:                metadata["abstract_url"],       // Using the abstract URL as the main URL
		RawBibEntry:        "",                             // You might want to generate this if needed
		FormattedText:      fmt.Sprintf("%s. (%s). %s. %s", metadata["authors"], metadata["published_date"][:4], metadata["title"], metadata["journal"]),
		IsAvailableOnArxiv: true,
	}

	// If there's a DOI, add it to the formatted text
	if metadata["doi"] != "" {
		paperRef.FormattedText += fmt.Sprintf(" DOI: %s", metadata["doi"])
	}

	// Save the reference
	if err := services.CreateOrUpdateReference(&paperRef); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save reference: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"title": metadata["title"],
	})
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
		priceTier := c.PostForm("price_tier")
		if priceTier != "base" && priceTier != "pro" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid price_tier. Must be 'base' or 'pro'."})
			return
		}

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

		sessionID, cachedContentName, err := researchChatService.StartResearchSession(c, arxivIDs, pdfPaths, priceTier)
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
				"session_id":       chat.SessionID,
				"messages":         messages,
				"created_at":       chat.CreatedAt.Format(time.RFC3339),
				"chat_duration":    chat.ChatDuration,
				"token_count_used": chat.TokenCountUsed,
				"price_tier":       chat.PriceTier,
				"token_hours_used": chat.TokenHoursUsed,
			})
		}

		c.JSON(http.StatusOK, gin.H{"chat_history": chatHistory})
	}
}

func getRawCacheHandler(researchChatService *services.ResearchChatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID := c.Query("session_id")
		if sessionID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
			return
		}

		content, err := researchChatService.GetRawTextCache(c.Request.Context(), sessionID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get raw cache: %v", err)})
			return
		}

		c.JSON(http.StatusOK, gin.H{"content": content})
	}
}

func purchaseCacheVolume(stripeService *services.StripeService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request struct {
			PriceTier  string `json:"price_tier" binding:"required"`
			TokenHours string `json:"token_hours" binding:"required"`
		}

		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Convert TokenHours to float64
		tokenHours, err := strconv.ParseFloat(request.TokenHours, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid token_hours value"})
			return
		}
		priceTier := request.PriceTier
		user, _ := c.Get("user")
		userModel := user.(*models.User)

		// Calculate amount based on your pricing strategy
		var priceID string
		switch priceTier {
		case "base":
			priceID = os.Getenv("STRIPE_BASE_PRICE_ID")
		case "pro":
			priceID = os.Getenv("STRIPE_PRO_PRICE_ID")
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid price tier"})
			return
		}

		session, err := stripeService.CreateCheckoutSession(userModel.ID.String(), priceID, tokenHours, priceTier)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create checkout session"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"session_id": session.ID})
	}
}

func stripeWebhookHandler(stripeService *services.StripeService, cacheManagementService *services.CacheManagementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		const MaxBodyBytes = int64(65536)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes)

		payload, err := io.ReadAll(c.Request.Body)
		if err != nil {
			fmt.Printf("Error reading request body: %v\n", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Error reading request body"})
			return
		}

		signatureHeader := c.GetHeader("Stripe-Signature")
		event, err := stripeService.HandleWebhook(payload, signatureHeader)
		if err != nil {
			fmt.Printf("Error verifying webhook signature: %v\n", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to verify webhook signature"})
			return
		}

		switch event.Type {
		case "checkout.session.completed":
			var session stripe.CheckoutSession
			err := json.Unmarshal(event.Data.Raw, &session)
			if err != nil {
				fmt.Printf("Error parsing checkout session: %v\n", err)
				c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse checkout session"})
				return
			}

			err = processSuccessfulCheckoutSession(session, cacheManagementService)
			if err != nil {
				fmt.Printf("Error processing checkout session: %v\n", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process checkout session"})
				return
			}

		default:
			fmt.Printf("Unhandled event type: %s\n", event.Type)
		}

		c.JSON(http.StatusOK, gin.H{"received": true})
	}
}

func processSuccessfulCheckoutSession(session stripe.CheckoutSession, cacheManagementService *services.CacheManagementService) error {
	userID, err := uuid.Parse(session.ClientReferenceID)
	if err != nil {
		return fmt.Errorf("invalid user ID: %v", err)
	}

	tokenHours, err := strconv.ParseFloat(session.Metadata["token_hours"], 64)
	if err != nil {
		return fmt.Errorf("invalid token hours: %v", err)
	}

	priceTier := session.Metadata["price_tier"]

	err = cacheManagementService.UpdateAllowedCacheUsage(context.Background(), userID, priceTier, tokenHours)
	if err != nil {
		return fmt.Errorf("failed to update allowed cache usage: %v", err)
	}

	return nil
}

// Stripe Webook versions that match the CLI API version
func stripeWebhookHandler_clitest(stripeService *services.StripeService, cacheManagementService *services.CacheManagementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		const MaxBodyBytes = int64(65536)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes)

		payload, err := io.ReadAll(c.Request.Body)
		if err != nil {
			fmt.Printf("Error reading request body: %v\n", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Error reading request body"})
			return
		}

		signatureHeader := c.GetHeader("Stripe-Signature")
		event, err := stripeService.HandleWebhook_clitest(payload, signatureHeader)
		if err != nil {
			fmt.Printf("Error verifying webhook signature: %v\n", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to verify webhook signature"})
			return
		}

		switch event.Type {
		case "checkout.session.completed":
			var session stripe.CheckoutSession
			err := json.Unmarshal(event.Data.Raw, &session)
			if err != nil {
				fmt.Printf("Error parsing checkout session: %v\n", err)
				c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse checkout session"})
				return
			}

			err = processSuccessfulCheckoutSession(session, cacheManagementService)
			if err != nil {
				fmt.Printf("Error processing checkout session: %v\n", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process checkout session"})
				return
			}

		default:
			fmt.Printf("Unhandled event type: %s\n", event.Type)
		}

		c.JSON(http.StatusOK, gin.H{"received": true})
	}
}

func getCacheUsageHandler(cacheManagementService *services.CacheManagementService) gin.HandlerFunc {
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

		baseTokens, proTokens, err := cacheManagementService.GetNetTokensByTier(c.Request.Context(), userModel.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get net tokens: %v", err)})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"base_net_tokens": baseTokens,
			"pro_net_tokens":  proTokens,
		})
	}
}
