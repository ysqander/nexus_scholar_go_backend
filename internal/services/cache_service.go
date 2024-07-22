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

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
)

type ChatSessionInfo struct {
	Session           *genai.ChatSession
	LastAccessed      time.Time
	CachedContentName string
	LastHeartbeat     time.Time
	HeartbeatsMissed  int
}

type CacheService struct {
	genaiClient      *genai.Client
	arxivBaseURL     string
	projectID        string
	location         string
	sessions         sync.Map
	expirationTime   time.Duration
	sessionsMutex    sync.RWMutex
	heartbeatTimeout time.Duration
	sessionTimeout   time.Duration
}

func NewCacheService(ctx context.Context, genaiClient *genai.Client, projectID string) (*CacheService, error) {
	const location = "US-CENTRAL1"

	cs := &CacheService{
		genaiClient:      genaiClient,
		arxivBaseURL:     "https://arxiv.org/pdf/",
		projectID:        projectID,
		location:         location,
		expirationTime:   10 * time.Minute,
		heartbeatTimeout: 1 * time.Minute,  // Adjust as needed
		sessionTimeout:   10 * time.Minute, // Adjust as needed
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
	for i, pdfPath := range userPDFs {
		content, err := s.processPDF(pdfPath)
		if err != nil {
			return "", fmt.Errorf("failed to process PDF %s: %v", pdfPath, err)
		}
		aggregatedContent.WriteString(fmt.Sprintf("<Document>\n<title>User PDF %d</title>\n%s\n</Document>\n", i+1, content))
	}

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
		LastHeartbeat:     time.Now(),
		HeartbeatsMissed:  0,
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
	sessionInfo.LastHeartbeat = time.Now()
	sessionInfo.HeartbeatsMissed = 0
	s.sessions.Store(sessionID, sessionInfo)

	return nil
}

// StreamChatMessage sends a message to the chat session and streams the response
func (s *CacheService) StreamChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponseIterator, error) {
	sessionInfo, exists := s.getAndUpdateSession(ctx, sessionID)
	if !exists {
		return nil, fmt.Errorf("chat session not found")
	}

	return sessionInfo.Session.SendMessageStream(ctx, genai.Text(message)), nil
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
		return nil
	}

	sessionInfo := sessionInterface.(ChatSessionInfo)
	s.sessions.Delete(sessionID)

	// Delete the cached content
	log.Printf("Deleting cached content for session %s: %s", sessionID, sessionInfo.CachedContentName)
	err := s.genaiClient.DeleteCachedContent(ctx, sessionInfo.CachedContentName)
	if err != nil {
		// Log the error but don't return it
		log.Printf("Failed to delete cached content for session %s: %v", sessionID, err)
	}

	return nil
}
