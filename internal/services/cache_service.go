package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
	"gorm.io/gorm"
)

type ChatSessionInfo struct {
	Session           *genai.ChatSession
	LastAccessed      time.Time
	CachedContentName string
	LastHeartbeat     time.Time
	HeartbeatsMissed  int
	LastCacheExtend   time.Time
	ChatHistory       []ChatMessage
}

type ChatMessage struct {
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type CacheService struct {
	genaiClient       *genai.Client
	arxivBaseURL      string
	projectID         string
	location          string
	sessions          sync.Map
	expirationTime    time.Duration
	sessionsMutex     sync.RWMutex
	heartbeatTimeout  time.Duration
	sessionTimeout    time.Duration
	cacheExtendPeriod time.Duration
}

func NewCacheService(ctx context.Context, genaiClient *genai.Client, projectID string) (*CacheService, error) {
	const location = "US-CENTRAL1"

	cs := &CacheService{
		genaiClient:       genaiClient,
		arxivBaseURL:      "https://arxiv.org/pdf/",
		projectID:         projectID,
		location:          location,
		expirationTime:    10 * time.Minute,
		heartbeatTimeout:  1 * time.Minute,  // Timeout after 1 minute of no heartbeats
		sessionTimeout:    10 * time.Minute, // Timeout after 10 minutes of no activity
		cacheExtendPeriod: 5 * time.Minute,  // Extend cache every 5 minutes of activity
	}

	go cs.periodicCleanup()
	return cs, nil
}

func (s *CacheService) CreateContentCache(ctx context.Context, arxivIDs []string, userPDFs []string, cacheExpirationTTL time.Duration) (string, error) {
	log.Println("Starting CreateContentCache")

	// 1. Process documents and aggregate content
	aggregatedContent, err := s.aggregateDocuments(arxivIDs, userPDFs)
	if err != nil {
		return "", fmt.Errorf("failed to aggregate documents: %v", err)
	}

	// 2. Create cached content using the Go SDK
	model := "gemini-1.5-flash-001"
	cc := &genai.CachedContent{
		Model: model,
		Expiration: genai.ExpireTimeOrTTL{
			TTL: cacheExpirationTTL,
		},
		Contents: []*genai.Content{
			genai.NewUserContent(genai.Text(aggregatedContent)),
		},
	}

	log.Println("Creating cached content using GEMINI API")
	cachedContent, err := s.genaiClient.CreateCachedContent(ctx, cc)
	if err != nil {
		return "", fmt.Errorf("failed to create cached content: %v", err)
	}

	log.Printf("Cached content created successfully with name: %s", cachedContent.Name)
	return cachedContent.Name, nil
}

func (s *CacheService) aggregateDocuments(arxivIDs []string, userPDFs []string) (string, error) {
	var aggregatedContent strings.Builder

	// Process arXiv papers
	for _, id := range arxivIDs {
		content, err := s.processArXivPaper(id)
		if err != nil {
			return "", fmt.Errorf("failed to process arXiv paper %s: %v", id, err)
		}
		aggregatedContent.WriteString(fmt.Sprintf("<Document>\n<title>arXiv:%s</title>\n%s\n</Document>\n", id, content))
	}

	// Process user-provided PDFs
	log.Printf("Starting to process %d user-provided PDFs", len(userPDFs))
	if len(userPDFs) == 0 {
		log.Println("Warning: No user-provided PDFs to process")
	}
	for i, pdfPath := range userPDFs {
		log.Printf("Processing user PDF %d: %s", i+1, pdfPath)
		if pdfPath == "" {
			log.Printf("Warning: Empty PDF path for user PDF %d", i+1)
			continue
		}
		content, err := s.processUserPDF(pdfPath)
		if err != nil {
			log.Printf("Error processing PDF %s: %v", pdfPath, err)
			return "", fmt.Errorf("failed to process PDF %s: %v", pdfPath, err)
		}
		if content == "" {
			log.Printf("Warning: Empty content extracted from PDF %s", pdfPath)
		}
		log.Printf("Successfully processed user PDF %d. Content length: %d", i+1, len(content))
		aggregatedContent.WriteString(fmt.Sprintf("<Document>\n<title>User PDF %d</title>\n%s\n</Document>\n", i+1, content))
	}
	log.Printf("Finished processing all user-provided PDFs")

	return aggregatedContent.String(), nil
}

func (s *CacheService) processArXivPaper(arxivID string) (string, error) {
	log.Printf("Starting processArXivPaper for arXiv ID: %s", arxivID)

	// Download the PDF from arXiv
	pdfURL := fmt.Sprintf("%s%s.pdf", s.arxivBaseURL, arxivID)
	log.Printf("Downloading PDF from URL: %s", pdfURL)
	resp, err := http.Get(pdfURL)
	if err != nil {
		log.Printf("Error downloading arXiv paper: %v", err)
		return "", fmt.Errorf("failed to download arXiv paper: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Unexpected status code when downloading arXiv paper: %d", resp.StatusCode)
		return "", fmt.Errorf("unexpected status code when downloading arXiv paper: %d", resp.StatusCode)
	}

	// Create a temporary file to store the PDF
	log.Println("Creating temporary file for PDF")
	tempFile, err := os.CreateTemp("", "arxiv-*.pdf")
	if err != nil {
		log.Printf("Error creating temporary file: %v", err)
		return "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Save the PDF content to the temporary file
	log.Println("Saving PDF content to temporary file")
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		log.Printf("Error saving PDF content: %v", err)
		return "", fmt.Errorf("failed to save PDF content: %v", err)
	}

	// Process the PDF file
	log.Println("Extracting text from PDF")
	content, err := s.extractTextFromPDF(tempFile.Name())
	if err != nil {
		log.Printf("Error extracting text from PDF: %v", err)
		return "", fmt.Errorf("failed to extract text from PDF: %v", err)
	}

	log.Printf("processArXivPaper for arXiv ID %s completed successfully", arxivID)
	return content, nil
}

func (s *CacheService) processUserPDF(pdfPath string) (string, error) {
	return s.extractTextFromPDF(pdfPath)
}

func (s *CacheService) extractTextFromPDF(pdfPath string) (string, error) {
	log.Printf("Starting to extract text from PDF: %s", pdfPath)
	f, r, err := pdf.Open(pdfPath)
	if err != nil {
		log.Printf("Failed to open PDF %s: %v", pdfPath, err)
		return "", fmt.Errorf("failed to open PDF: %v", err)
	}
	defer f.Close()

	var content strings.Builder
	totalPage := r.NumPage()
	log.Printf("Total pages in PDF: %d", totalPage)

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		log.Printf("Processing page %d of %d", pageIndex, totalPage)
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			log.Printf("Page %d is null, skipping", pageIndex)
			continue
		}

		text, err := p.GetPlainText(nil)
		if err != nil {
			log.Printf("Error extracting text from page %d: %v", pageIndex, err)
			continue
		}
		log.Printf("Extracted %d characters from page %d", len(text), pageIndex)
		content.WriteString(text)
		content.WriteString("\n\n") // Add separation between pages
	}

	if content.Len() == 0 {
		log.Printf("No text content extracted from PDF %s", pdfPath)
		return "", fmt.Errorf("no text content extracted from PDF")
	}

	log.Printf("Successfully extracted %d characters from PDF %s", content.Len(), pdfPath)
	return content.String(), nil
}

// DeleteCache deletes the cached content with the given cache name
func (s *CacheService) DeleteCache(ctx context.Context, cacheName string) error {
	log.Printf("Attempting to delete cached content: %s", cacheName)
	err := s.genaiClient.DeleteCachedContent(ctx, cacheName)
	if err != nil {
		log.Printf("Error deleting cached content %s: %v", cacheName, err)
		return fmt.Errorf("failed to delete cached content: %v", err)
	}
	log.Printf("Successfully deleted cached content: %s", cacheName)
	return nil
}

// StartChatSession creates a new chat session using the cached content
func (s *CacheService) StartChatSession(ctx context.Context, cachedContentName string) (string, error) {
	log.Printf("Starting chat session with cached content name: %s", cachedContentName)
	cachedContent, err := s.genaiClient.GetCachedContent(ctx, cachedContentName)
	if err != nil {
		return "", fmt.Errorf("failed to get cached content: %v", err)
	}
	model := s.genaiClient.GenerativeModelFromCachedContent(cachedContent)

	session := model.StartChat()
	sessionID := uuid.New().String()

	s.sessionsMutex.Lock()
	defer s.sessionsMutex.Unlock()

	s.sessions.Store(sessionID, ChatSessionInfo{
		Session:           session,
		LastAccessed:      time.Now(),
		CachedContentName: cachedContentName,
		LastHeartbeat:     time.Now(),
		HeartbeatsMissed:  0,
		LastCacheExtend:   time.Now(),
		ChatHistory:       []ChatMessage{},
	})

	return sessionID, nil
}

func (s *CacheService) UpdateSessionHeartbeat(sessionID string) error {
	s.sessionsMutex.Lock()
	defer s.sessionsMutex.Unlock()

	sessionInterface, ok := s.sessions.Load(sessionID)
	if !ok {
		return fmt.Errorf("session not found")
	}

	sessionInfo := sessionInterface.(ChatSessionInfo)
	now := time.Now()
	sessionInfo.LastHeartbeat = now
	sessionInfo.HeartbeatsMissed = 0
	sessionInfo.LastAccessed = now

	// Check if it's time to extend the cache
	if now.Sub(sessionInfo.LastCacheExtend) >= s.cacheExtendPeriod {
		if err := s.extendCacheLifetime(sessionInfo.CachedContentName); err != nil {
			log.Printf("Failed to extend cache lifetime for session %s: %v", sessionID, err)
		} else {
			sessionInfo.LastCacheExtend = now
		}
	}
	s.sessions.Store(sessionID, sessionInfo)

	return nil
}

func (s *CacheService) extendCacheLifetime(cachedContentName string) error {
	ctx := context.Background()
	cachedContent := &genai.CachedContent{
		Name: cachedContentName,
	}

	newExpiration := genai.ExpireTimeOrTTL{
		TTL: s.expirationTime,
	}

	updateContent := &genai.CachedContentToUpdate{
		Expiration: &newExpiration,
	}

	_, err := s.genaiClient.UpdateCachedContent(ctx, cachedContent, updateContent)
	if err != nil {
		return fmt.Errorf("failed to update cached content expiration: %v", err)
	}

	return nil
}

// StreamChatMessage sends a message to the chat session and streams the response
func (s *CacheService) StreamChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponseIterator, error) {
	s.sessionsMutex.Lock()
	defer s.sessionsMutex.Unlock()

	sessionInfo, exists := s.getAndUpdateSession(ctx, sessionID)
	if !exists {
		return nil, fmt.Errorf("chat session not found")
	}

	// Add formatting instruction to the message
	formattedMessage := fmt.Sprintf("%s\n\n"+
		"Format your answer in markdown with easily readable paragraphs. ",
		message)

	// Append user message to chat history
	s.UpdateSessionChatHistory(sessionID, "user", message)

	// Update the session info in the map
	s.sessions.Store(sessionID, sessionInfo)

	return sessionInfo.Session.SendMessageStream(ctx, genai.Text(formattedMessage)), nil
}

func (s *CacheService) getAndUpdateSession(ctx context.Context, sessionID string) (ChatSessionInfo, bool) {
	s.sessionsMutex.Lock()
	defer s.sessionsMutex.Unlock()
	sessionInterface, ok := s.sessions.Load(sessionID)
	if !ok {
		log.Printf("Session not found for ID: %s", sessionID)
		return ChatSessionInfo{}, false
	}
	sessionInfo := sessionInterface.(ChatSessionInfo)
	log.Printf("Found session for ID: %s, last accessed: %v", sessionID, sessionInfo.LastAccessed)
	sessionInfo.LastAccessed = time.Now()
	s.sessions.Store(sessionID, sessionInfo)
	return sessionInfo, true
}

func (s *CacheService) UpdateSessionChatHistory(sessionID, chatType, content string) {
	s.sessionsMutex.Lock()
	defer s.sessionsMutex.Unlock()

	if sessionInterface, ok := s.sessions.Load(sessionID); ok {
		sessionInfo := sessionInterface.(ChatSessionInfo)
		sessionInfo.ChatHistory = append(sessionInfo.ChatHistory, ChatMessage{
			Type:      chatType,
			Content:   content,
			Timestamp: time.Now(),
		})
		s.sessions.Store(sessionID, sessionInfo)
	}
}

func (s *CacheService) SaveChatHistoryToDB(sessionID string, userID uint) error {
	s.sessionsMutex.Lock()
	defer s.sessionsMutex.Unlock()

	sessionInterface, ok := s.sessions.Load(sessionID)
	if !ok {
		return fmt.Errorf("session not found")
	}

	sessionInfo := sessionInterface.(ChatSessionInfo)
	historyJSON, err := json.Marshal(sessionInfo.ChatHistory)
	if err != nil {
		return fmt.Errorf("failed to marshal chat history: %v", err)
	}

	// Check if a chat entry already exists for this session
	var existingChat models.Chat
	result := database.DB.Where("session_id = ?", sessionID).First(&existingChat)

	if result.Error == nil {
		// Update existing chat entry
		existingChat.History = historyJSON
		result = database.DB.Save(&existingChat)
	} else if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// Create new chat entry
		newChat := models.Chat{
			UserID:    userID,
			SessionID: sessionID,
			History:   historyJSON,
		}
		result = database.DB.Create(&newChat)
	} else {
		// Unexpected error
		return fmt.Errorf("error checking for existing chat: %v", result.Error)
	}

	if result.Error != nil {
		return fmt.Errorf("failed to save chat history to database: %v", result.Error)
	}

	return nil
}

func (s *CacheService) periodicCleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	for range ticker.C {
		s.cleanupExpiredSessions(context.Background())
	}
}

func (s *CacheService) cleanupExpiredSessions(ctx context.Context) {
	now := time.Now()
	s.sessions.Range(func(key, value interface{}) bool {
		sessionID := key.(string)
		sessionInfo := value.(ChatSessionInfo)

		if now.Sub(sessionInfo.LastAccessed) > s.sessionTimeout {
			s.TerminateSession(ctx, sessionID)
			return true
		}

		if now.Sub(sessionInfo.LastHeartbeat) > s.heartbeatTimeout {
			sessionInfo.HeartbeatsMissed++
			if sessionInfo.HeartbeatsMissed >= 3 { // Terminate after 3 missed heartbeats
				s.TerminateSession(ctx, sessionID)
				return true
			}
			s.sessions.Store(sessionID, sessionInfo)
		}

		return true
	})
}

// TerminateSession ends the chat session and cleans up resources
func (s *CacheService) TerminateSession(ctx context.Context, sessionID string) error {
	s.sessionsMutex.Lock()
	defer s.sessionsMutex.Unlock()

	sessionInterface, ok := s.sessions.Load(sessionID)
	if !ok {
		// Session doesn't exist, it might have been already terminated
		log.Printf("Session %s not found, it may have already been terminated", sessionID)
		return nil
	}

	sessionInfo := sessionInterface.(ChatSessionInfo)
	s.sessions.Delete(sessionID)

	// Check if the cached content still exists before attempting to delete it
	_, err := s.genaiClient.GetCachedContent(ctx, sessionInfo.CachedContentName)
	if err != nil {
		// If the cached content doesn't exist, log it and return
		log.Printf("Cached content for session %s (%s) not found, it may have already been deleted: %v", sessionID, sessionInfo.CachedContentName, err)
		return nil
	}

	// Delete the cached content
	log.Printf("Deleting cached content for session %s: %s", sessionID, sessionInfo.CachedContentName)
	err = s.genaiClient.DeleteCachedContent(ctx, sessionInfo.CachedContentName)
	if err != nil {
		// Log the error but don't return it
		log.Printf("Failed to delete cached content for session %s: %v", sessionID, err)
	}

	return nil
}
