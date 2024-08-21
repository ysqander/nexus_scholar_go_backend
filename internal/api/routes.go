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
	"nexus_scholar_go_backend/internal/broker"
	"nexus_scholar_go_backend/internal/errors"
	"nexus_scholar_go_backend/internal/models"
	"nexus_scholar_go_backend/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stripe/stripe-go/v79"
	"google.golang.org/api/iterator"
)

func SetupRoutes(r *gin.Engine, researchChatService *services.ResearchChatService, chatService services.ChatServiceDB, stripeService *services.StripeService, cacheManagementService *services.CacheManagementService, userService *services.UserService, messageBroker *broker.Broker, log zerolog.Logger) {
	api := r.Group("/api")
	{
		api.GET("/papers/:arxiv_id", auth.AuthMiddleware(userService), getPaper(log))
		api.GET("/papers/:arxiv_id/title", auth.AuthMiddleware(userService), getPaperTitle(log))
		api.GET("/private", auth.AuthMiddleware(userService), privateRoute)
		api.POST("/create-research-session", auth.AuthMiddleware(userService), createResearchSessionHandler(researchChatService))
		api.GET("/raw-cache", auth.AuthMiddleware(userService), getRawCacheHandler(researchChatService))
		api.POST("/chat/message", auth.AuthMiddleware(userService), sendChatMessageHandler(researchChatService))
		api.POST("/chat/terminate", auth.AuthMiddleware(userService), terminateChatSessionHandler(researchChatService))
		api.GET("/chat/history", auth.AuthMiddleware(userService), getChatHistoryHandler(researchChatService))
		api.POST("/purchase-cache-volume", auth.AuthMiddleware(userService), purchaseCacheVolume(stripeService))
		api.GET("/cache-usage", auth.AuthMiddleware(userService), getCacheUsageHandler(cacheManagementService, chatService, log))
		api.POST("/stripe/webhook", stripeWebhookHandler(stripeService, cacheManagementService, messageBroker))
		api.POST("/stripe/webhook_clitest", stripeWebhookHandler_clitest(stripeService, cacheManagementService, messageBroker))
	}
}

func getPaper(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		arxivID := c.Param("arxiv_id")
		if arxivID == "" {
			errors.HandleError(c, errors.New400Error("ArXiv ID is required"))
			return
		}

		paperLoader := services.NewPaperLoader(log)
		result, err := paperLoader.ProcessPaper(arxivID)
		if err != nil {
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to process paper: %v", err)))
			return
		}

		c.JSON(http.StatusOK, result)
	}
}

func getPaperTitle(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		arxivID := c.Param("arxiv_id")
		if arxivID == "" {
			errors.HandleError(c, errors.New400Error("ArXiv ID is required"))
			return
		}
		parentArxivID := c.Query("parent_arxiv_id")

		paperLoader := services.NewPaperLoader(log)
		metadata, err := paperLoader.GetPaperMetadata(arxivID)
		if err != nil {
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to get paper metadata: %v", err)))
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
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to save reference: %v", err)))
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"title": metadata["title"],
		})
	}
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
		// Log request details
		log.Info().Msgf("Request Method: %s", c.Request.Method)
		log.Info().Msgf("Request Headers: %v", c.Request.Header)
		log.Info().Msgf("Request Content-Type: %s", c.ContentType())

		// Handle OPTIONS request
		if c.Request.Method == "OPTIONS" {
			c.Header("Access-Control-Allow-Origin", c.GetHeader("Origin"))
			c.Header("Access-Control-Allow-Methods", "POST, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
			c.Header("Access-Control-Max-Age", "86400")
			c.Status(http.StatusNoContent)
			return
		}

		priceTier := c.PostForm("price_tier")
		if priceTier != "base" && priceTier != "pro" {
			errors.HandleError(c, errors.New400Error("Invalid price_tier. Must be 'base' or 'pro'."))
			return
		}

		// Get arXiv IDs
		arxivIDsJSON := c.PostForm("arxiv_ids")
		var arxivIDs []string
		if err := json.Unmarshal([]byte(arxivIDsJSON), &arxivIDs); err != nil {
			errors.HandleError(c, errors.New400Error("Invalid arXiv IDs format"))
			return
		}

		// Create a temporary directory to store uploaded files
		tempDir, err := os.MkdirTemp("", "user_pdfs_")
		if err != nil {
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to create temporary directory: %v", err)))
			return
		}
		defer os.RemoveAll(tempDir) // Clean up the temporary directory when done

		// Save uploaded files and collect their paths
		var pdfPaths []string
		form, err := c.MultipartForm()
		if err != nil {
			errors.HandleError(c, errors.New400Error("Failed to parse multipart form"))
			return
		}
		files := form.File["pdfs"]
		for _, fileHeader := range files {
			filename := filepath.Join(tempDir, fileHeader.Filename)
			if err := c.SaveUploadedFile(fileHeader, filename); err != nil {
				errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to save file %s: %v", fileHeader.Filename, err)))
				return
			}
			pdfPaths = append(pdfPaths, filename)
		}

		sessionID, cachedContentName, err := researchChatService.StartResearchSession(c, arxivIDs, pdfPaths, priceTier)
		if err != nil {
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to start research session: %v", err)))
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
			errors.HandleError(c, errors.New400Error(err.Error()))
			return
		}

		responseIterator, err := researchChatService.SendMessage(c.Request.Context(), request.SessionID, request.Message)
		if err != nil {
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to send message: %v", err)))
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
			errors.HandleError(c, errors.New400Error(err.Error()))
			return
		}

		err := researchChatService.EndResearchSession(c.Request.Context(), request.SessionID)
		if err != nil {
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to terminate research session: %v", err)))
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Research session terminated successfully"})
	}
}

func getChatHistoryHandler(researchChatService *services.ResearchChatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, exists := c.Get("user")
		if !exists {
			errors.HandleError(c, errors.New401Error())
			return
		}

		userModel, ok := user.(*models.User)
		if !ok {
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to cast user to *models.User")))
			return
		}

		chats, err := researchChatService.GetUserChatHistory(userModel.ID)
		if err != nil {
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to retrieve chat history: %v", err)))
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
				"termination_time": chat.TerminationTime.Format(time.RFC3339),
			})
		}

		c.JSON(http.StatusOK, gin.H{"chat_history": chatHistory})
	}
}

func getRawCacheHandler(researchChatService *services.ResearchChatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID := c.Query("session_id")
		if sessionID == "" {
			errors.HandleError(c, errors.New400Error("session_id is required"))
			return
		}

		content, err := researchChatService.GetRawTextCache(c.Request.Context(), sessionID)
		if err != nil {
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to get raw cache: %v", err)))
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
			errors.HandleError(c, errors.New400Error(err.Error()))
			return
		}

		// Convert TokenHours to float64
		tokenHours, err := strconv.ParseFloat(request.TokenHours, 64)
		if err != nil {
			errors.HandleError(c, errors.New400Error("Invalid token_hours value"))
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
			errors.HandleError(c, errors.New400Error("Invalid price tier"))
			return
		}

		session, err := stripeService.CreateCheckoutSession(userModel.ID.String(), priceID, tokenHours, priceTier)
		if err != nil {
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to create checkout session: %v", err)))
			return
		}

		c.JSON(http.StatusOK, gin.H{"session_id": session.ID})
	}
}

func stripeWebhookHandler(stripeService *services.StripeService, cacheManagementService *services.CacheManagementService, messageBroker *broker.Broker) gin.HandlerFunc {
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

			err = processSuccessfulCheckoutSession(session, cacheManagementService, messageBroker)
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

func processSuccessfulCheckoutSession(session stripe.CheckoutSession, cacheManagementService *services.CacheManagementService, messageBroker *broker.Broker) error {
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

	messageBroker.Publish("credit_update_"+userID.String(), "Credit updated for user "+userID.String())

	return nil
}

// Stripe Webook versions that match the CLI API version
func stripeWebhookHandler_clitest(stripeService *services.StripeService, cacheManagementService *services.CacheManagementService, messageBroker *broker.Broker) gin.HandlerFunc {
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

			err = processSuccessfulCheckoutSession(session, cacheManagementService, messageBroker)
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

func getCacheUsageHandler(cacheManagementService *services.CacheManagementService, chatService services.ChatServiceDB, log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, exists := c.Get("user")
		if !exists {
			log.Error().Msg("User not found")
			errors.HandleError(c, errors.New401Error())
			return
		}

		userModel, ok := user.(*models.User)
		if !ok {
			log.Error().Msg("Failed to cast user to *models.User")
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to cast user to *models.User")))
			return
		}

		baseTokens, proTokens, err := cacheManagementService.GetNetTokensByTier(c.Request.Context(), userModel.ID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get net tokens")
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to get net tokens: %v", err)))
			return
		}

		// Get historical chats
		historicalChatMetrics, err := chatService.GetHistoricalChatMetricsByUserID(userModel.ID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get historical chats")
			errors.HandleError(c, errors.LogAndReturn500(fmt.Errorf("failed to get historical chats: %v", err)))
			return
		}

		// Organize chat history by month and price tier
		chatHistoryByMonth := make(map[string]map[string][]gin.H)

		for _, chat := range historicalChatMetrics {
			// Use CreatedAt if TerminationTime is zero
			chatTime := chat.TerminationTime
			if chatTime.IsZero() {
				chatTime = chat.CreatedAt
			}

			monthKey := chatTime.Format("2006-01")

			if _, exists := chatHistoryByMonth[monthKey]; !exists {
				chatHistoryByMonth[monthKey] = make(map[string][]gin.H)
			}

			chatData := gin.H{
				"session_id":       chat.SessionID,
				"tokens_used":      chat.TokenCountUsed,
				"token_hours_used": chat.TokenHoursUsed,
				"duration":         chat.ChatDuration,
				"termination_time": chatTime.Format(time.RFC3339),
			}

			chatHistoryByMonth[monthKey][chat.PriceTier] = append(chatHistoryByMonth[monthKey][chat.PriceTier], chatData)
		}

		log.Info().Msgf("Base tokens: %f, Pro tokens: %f", baseTokens, proTokens)

		c.JSON(http.StatusOK, gin.H{
			"base_net_tokens": baseTokens,
			"pro_net_tokens":  proTokens,
			"chat_history":    chatHistoryByMonth,
			"debug_info": gin.H{
				"chat_count":      len(historicalChatMetrics),
				"first_chat_time": historicalChatMetrics[0].TerminationTime.Format(time.RFC3339),
				"last_chat_time":  historicalChatMetrics[len(historicalChatMetrics)-1].TerminationTime.Format(time.RFC3339),
			},
		})
	}
}
