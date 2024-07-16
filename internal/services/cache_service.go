package services

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"cloud.google.com/go/vertexai/genai"
	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
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
	bucketName     string
	sessions       sync.Map
	expirationTime time.Duration
}

func NewCacheService(genaiClient *genai.Client, storageClient *storage.Client, bucketName string) *CacheService {
	cs := &CacheService{
		genaiClient:    genaiClient,
		storageClient:  storageClient,
		arxivBaseURL:   "https://arxiv.org/pdf/",
		bucketName:     bucketName,
		expirationTime: 300 * time.Minute,
	}
	go cs.periodicCleanup()
	return cs
}

func (s *CacheService) CreateContentCache(ctx context.Context, arxivIDs []string, userPDFs []string, cacheExpirationTTL time.Duration) (string, error) {
	// 1. Process PDFs and extract text content
	contents, err := s.processDocuments(ctx, arxivIDs, userPDFs)
	if err != nil {
		return "", fmt.Errorf("failed to process documents: %v", err)
	}

	// 2. Upload content to Google Cloud Storage
	gcsURIs, err := s.uploadToGCS(ctx, contents)
	if err != nil {
		return "", fmt.Errorf("failed to upload to GCS: %v", err)
	}

	// 3. Create cached content
	cachedContent, err := s.createCachedContent(ctx, gcsURIs, cacheExpirationTTL)
	if err != nil {
		return "", fmt.Errorf("failed to create cached content: %v", err)
	}

	return cachedContent.Name, nil
}

func (s *CacheService) processDocuments(ctx context.Context, arxivIDs []string, userPDFs []string) ([]string, error) {
	var contents []string

	// Process arXiv papers
	for _, id := range arxivIDs {
		content, err := s.processArXivPaper(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("failed to process arXiv paper %s: %v", id, err)
		}
		contents = append(contents, content)
	}

	// Process user-provided PDFs
	for _, pdfPath := range userPDFs {
		content, err := s.processPDF(pdfPath)
		if err != nil {
			return nil, fmt.Errorf("failed to process PDF %s: %v", pdfPath, err)
		}
		contents = append(contents, content)
	}

	return contents, nil
}

func (s *CacheService) processArXivPaper(ctx context.Context, arxivID string) (string, error) {
	// Download the PDF from arXiv
	pdfURL := fmt.Sprintf("%s%s.pdf", s.arxivBaseURL, arxivID)
	resp, err := http.Get(pdfURL)
	if err != nil {
		return "", fmt.Errorf("failed to download arXiv paper: %v", err)
	}
	defer resp.Body.Close()

	// Create a temporary file to store the PDF
	tempFile, err := os.CreateTemp("", "arxiv-*.pdf")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Save the PDF content to the temporary file
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to save PDF content: %v", err)
	}

	// Process the PDF file
	content, err := s.extractTextFromPDF(tempFile.Name())
	if err != nil {
		return "", fmt.Errorf("failed to extract text from PDF: %v", err)
	}

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
			return "", fmt.Errorf("failed to get text from page %d: %v", pageIndex, err)
		}
		content += text
	}

	return content, nil
}

// generateUniqueFilename creates a unique filename using timestamp and UUID
func generateUniqueFilename() string {
	timestamp := time.Now().UnixNano()
	uniqueID := uuid.New().String()
	return fmt.Sprintf("%d-%s.txt", timestamp, uniqueID)
}

func (s *CacheService) uploadToGCS(ctx context.Context, contents []string) ([]string, error) {
	var gcsURIs []string
	for _, content := range contents {
		obj := s.storageClient.Bucket(s.bucketName).Object(generateUniqueFilename())
		writer := obj.NewWriter(ctx)
		// Write content to GCS
		if _, err := writer.Write([]byte(content)); err != nil {
			return nil, fmt.Errorf("failed to write to GCS: %v", err)
		}
		if err := writer.Close(); err != nil {
			return nil, fmt.Errorf("failed to close GCS writer: %v", err)
		}
		gcsURIs = append(gcsURIs, fmt.Sprintf("gs://%s/%s", s.bucketName, obj.ObjectName()))
	}
	return gcsURIs, nil
}

func (s *CacheService) createCachedContent(ctx context.Context, gcsURIs []string, expirationTTL time.Duration) (*genai.CachedContent, error) {
	cc := &genai.CachedContent{
		Model: "gemini-1.5-pro-001", // Specify the Gemini model you want to use
		Expiration: genai.ExpireTimeOrTTL{
			TTL: expirationTTL, // Set TTL to 10 minutes
		},
	}

	// Create a single Content object with all GCS URIs
	content := &genai.Content{
		Parts: make([]genai.Part, len(gcsURIs)),
	}

	for i, uri := range gcsURIs {
		content.Parts[i] = genai.FileData{
			MIMEType: "text/plain",
			FileURI:  uri,
		}
	}

	cc.Contents = []*genai.Content{content}

	return s.genaiClient.CreateCachedContent(ctx, cc)
}

// DeleteCache deletes the cached content with the given cache name
func (s *CacheService) DeleteCache(ctx context.Context, cacheName string) error {
	err := s.genaiClient.DeleteCachedContent(ctx, cacheName)
	if err != nil {
		return fmt.Errorf("failed to delete cached content: %v", err)
	}
	return nil
}

// StartChatSession creates a new chat session using the cached content
func (s *CacheService) StartChatSession(ctx context.Context, cachedContentName string) (string, error) {
	model := s.genaiClient.GenerativeModelFromCachedContent(&genai.CachedContent{Name: cachedContentName})
	session := model.StartChat()
	sessionID := uuid.New().String()

	s.sessions.Store(sessionID, ChatSessionInfo{
		Session:           session,
		LastAccessed:      time.Now(),
		CachedContentName: cachedContentName,
	})

	return sessionID, nil
}

// SendChatMessage sends a message to the chat session and returns the response
func (s *CacheService) SendChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponse, error) {
	sessionInfo, exists := s.getAndUpdateSession(sessionID)
	if !exists {
		return nil, fmt.Errorf("chat session not found")
	}

	return sessionInfo.Session.SendMessage(ctx, genai.Text(message))
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
	if sessionInterface, ok := s.sessions.Load(sessionID); ok {
		sessionInfo := sessionInterface.(ChatSessionInfo)
		sessionInfo.LastAccessed = time.Now()
		s.sessions.Store(sessionID, sessionInfo)
		return sessionInfo, true
	}
	return ChatSessionInfo{}, false
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
			s.sessions.Delete(key)
		}
		return true
	})
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
	err := s.genaiClient.DeleteCachedContent(ctx, sessionInfo.CachedContentName)
	if err != nil {
		// Log the error but don't return it
		log.Printf("Failed to delete cached content for session %s: %v", sessionID, err)
	}

	return nil
}
