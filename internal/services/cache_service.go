package services

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
	"google.golang.org/api/googleapi"
)

type ChatSessionInfo struct {
	Session           *genai.ChatSession
	LastAccessed      time.Time
	CachedContentName string
}

type CacheService struct {
	genaiClient    *genai.Client
	storageClient  *storage.Client
	arxivBaseURL   string
	projectID      string
	location       string
	bucketName     string
	sessions       sync.Map
	expirationTime time.Duration
	sessionsMutex  sync.RWMutex
}

func NewCacheService(ctx context.Context, genaiClient *genai.Client, storageClient *storage.Client, projectID string) (*CacheService, error) {
	const location = "US-CENTRAL1"
	bucketName := "nexus-scholar-cached-pdfs"

	cs := &CacheService{
		genaiClient:    genaiClient,
		storageClient:  storageClient,
		arxivBaseURL:   "https://arxiv.org/pdf/",
		projectID:      projectID,
		location:       location,
		bucketName:     bucketName,
		expirationTime: 10 * time.Minute,
	}

	// Check if bucket exists, create if it doesn't
	if err := cs.ensureBucketExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure bucket exists: %v", err)
	}

	go cs.periodicCleanup()
	return cs, nil
}

func (s *CacheService) ensureBucketExists(ctx context.Context) error {
	bucket := s.storageClient.Bucket(s.bucketName)
	attrs, err := bucket.Attrs(ctx)
	if err == storage.ErrBucketNotExist {
		log.Printf("Bucket %s does not exist. Creating in %s...", s.bucketName, s.location)
		bucketAttrs := &storage.BucketAttrs{
			Location: s.location,
		}
		if err := bucket.Create(ctx, s.projectID, bucketAttrs); err != nil {
			return fmt.Errorf("failed to create bucket: %v", err)
		}
		log.Printf("Bucket %s created successfully in %s", s.bucketName, s.location)
	} else if err != nil {
		return fmt.Errorf("failed to get bucket attributes: %v", err)
	} else {
		log.Printf("Bucket %s already exists in %s", s.bucketName, attrs.Location)
		if attrs.Location != s.location {
			log.Printf("Warning: Existing bucket is not in %s. It's in %s", s.location, attrs.Location)
		}
	}
	return nil
}

func (s *CacheService) CreateContentCache(ctx context.Context, arxivIDs []string, userPDFs []string, cacheExpirationTTL time.Duration) (string, error) {
	log.Println("Starting CreateContentCache")

	// 1. Process documents and aggregate content
	aggregatedContent, err := s.aggregateDocuments(ctx, arxivIDs, userPDFs)
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

func (s *CacheService) aggregateDocuments(ctx context.Context, arxivIDs []string, userPDFs []string) (string, error) {
	var aggregatedContent strings.Builder

	// Process arXiv papers
	for _, id := range arxivIDs {
		content, err := s.processArXivPaper(ctx, id)
		if err != nil {
			return "", fmt.Errorf("failed to process arXiv paper %s: %v", id, err)
		}
		aggregatedContent.WriteString(fmt.Sprintf("<Document>\n<title>arXiv:%s</title>\n%s\n</Document>\n", id, content))
	}

	// Process user-provided PDFs
	for i, pdfPath := range userPDFs {
		content, err := s.processPDF(pdfPath)
		if err != nil {
			return "", fmt.Errorf("failed to process PDF %s: %v", pdfPath, err)
		}
		aggregatedContent.WriteString(fmt.Sprintf("<Document>\n<title>User PDF %d</title>\n%s\n</Document>\n", i+1, content))
	}

	return aggregatedContent.String(), nil
}

func (s *CacheService) processArXivPaper(ctx context.Context, arxivID string) (string, error) {
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

func (s *CacheService) processPDF(pdfPath string) (string, error) {
	return s.extractTextFromPDF(pdfPath)
}

func (s *CacheService) extractTextFromPDF(pdfPath string) (string, error) {
	f, r, err := pdf.Open(pdfPath)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %v", err)
	}
	defer f.Close()

	var content string
	totalPage := r.NumPage()

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}

		text, err := p.GetPlainText(nil)
		if err != nil {
			// Instead of returning immediately, we'll continue to the next page
			continue
		}
		content += text
	}

	if content == "" {
		return "", fmt.Errorf("no text content extracted from PDF")
	}

	return content, nil
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
	})

	return sessionID, nil
}

// SendChatMessage sends a message to the chat session and returns the response
func (s *CacheService) SendChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponse, error) {
	log.Printf("SendChatMessage called with sessionID: %s and message: %s", sessionID, message)

	sessionInfo, exists := s.getAndUpdateSession(sessionID)
	if !exists {
		log.Printf("Chat session not found for sessionID: %s", sessionID)
		return nil, fmt.Errorf("chat session not found")
	}

	response, err := sessionInfo.Session.SendMessage(ctx, genai.Text(message))
	if err != nil {
		log.Printf("Error sending message in sessionID: %s, error: %v", sessionID, err)
		if gerr, ok := err.(*googleapi.Error); ok {
			log.Printf("Google API Error - Code: %d, Message: %s, Details: %v", gerr.Code, gerr.Message, gerr.Details)
		}
		return nil, err
	}

	log.Printf("Message sent successfully in sessionID: %s", sessionID)
	return response, nil
}

// StreamChatMessage sends a message to the chat session and streams the response
func (s *CacheService) StreamChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponseIterator, error) {
	sessionInfo, exists := s.getAndUpdateSession(sessionID)
	if !exists {
		return nil, fmt.Errorf("chat session not found")
	}

	return sessionInfo.Session.SendMessageStream(ctx, genai.Text(message)), nil
}

func (s *CacheService) getAndUpdateSession(sessionID string) (ChatSessionInfo, bool) {
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

func (s *CacheService) periodicCleanup() {
	ticker := time.NewTicker(15 * time.Minute)
	for range ticker.C {
		s.cleanupExpiredSessions()
	}
}

func (s *CacheService) cleanupExpiredSessions() {
	now := time.Now()
	s.sessions.Range(func(key, value interface{}) bool {
		sessionInfo := value.(ChatSessionInfo)
		if now.Sub(sessionInfo.LastAccessed) > s.expirationTime {
			log.Printf("Removing expired session: %v", key)
			s.sessions.Delete(key)
		}
		return true
	})
}

func (s *CacheService) TestGeminiAPIConnection(ctx context.Context) error {
	model := s.genaiClient.GenerativeModel("gemini-1.5-pro-001")
	_, err := model.GenerateContent(ctx, genai.Text("Hello, World!"))
	if err != nil {
		return fmt.Errorf("failed to test Gemini API connection: %v", err)
	}
	return nil
}

// TerminateSession ends the chat session and cleans up resources
func (s *CacheService) TerminateSession(ctx context.Context, sessionID string) error {
	value, exists := s.sessions.LoadAndDelete(sessionID)
	if !exists {
		// Session doesn't exist, but we don't consider this an error
		return nil
	}

	sessionInfo := value.(ChatSessionInfo)

	// Delete the cached content
	log.Printf("Deleting cached content for session %s: %s", sessionID, sessionInfo.CachedContentName)
	err := s.genaiClient.DeleteCachedContent(ctx, sessionInfo.CachedContentName)
	if err != nil {
		// Log the error but don't return it
		log.Printf("Failed to delete cached content for session %s: %v", sessionID, err)
	}

	return nil
}
