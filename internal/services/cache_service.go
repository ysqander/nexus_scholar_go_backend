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

	"nexus_scholar_go_backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
	"gorm.io/gorm"
)

type ContentCreator interface {
	CreateCachedContent(ctx context.Context, cc *genai.CachedContent) (*genai.CachedContent, error)
}

type ContentRetriever interface {
	GetCachedContent(ctx context.Context, name string) (*genai.CachedContent, error)
}

type ContentDeleter interface {
	DeleteCachedContent(ctx context.Context, name string) error
}

type ContentUpdater interface {
	UpdateCachedContent(ctx context.Context, cc *genai.CachedContent, update *genai.CachedContentToUpdate) (*genai.CachedContent, error)
}

type ModelGenerator interface {
	GenerativeModelFromCachedContent(cc *genai.CachedContent) *genai.GenerativeModel
}

type GenAIClient interface {
	ContentCreator
	ContentRetriever
	ContentDeleter
	ContentUpdater
	ModelGenerator
}

type DBOperations interface {
	Where(query interface{}, args ...interface{}) *gorm.DB
	Assign(attrs ...interface{}) *gorm.DB
	FirstOrCreate(dest interface{}, conds ...interface{}) *gorm.DB
	Create(value interface{}) *gorm.DB
}

// Functional option type
type CacheServiceOption func(*CacheService)

// Functional options
func WithExpirationTime(d time.Duration) CacheServiceOption {
	return func(cs *CacheService) {
		cs.expirationTime = d
	}
}

func WithHeartbeatTimeout(d time.Duration) CacheServiceOption {
	return func(cs *CacheService) {
		cs.heartbeatTimeout = d
	}
}

func WithSessionTimeout(d time.Duration) CacheServiceOption {
	return func(cs *CacheService) {
		cs.sessionTimeout = d
	}
}

func WithCacheExtendPeriod(d time.Duration) CacheServiceOption {
	return func(cs *CacheService) {
		cs.cacheExtendPeriod = d
	}
}

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
	genaiClient       GenAIClient
	chatService       ChatService
	db                DBOperations
	arxivBaseURL      string
	projectID         string
	sessions          sync.Map
	expirationTime    time.Duration
	sessionsMutex     sync.RWMutex
	heartbeatTimeout  time.Duration
	sessionTimeout    time.Duration
	cacheExtendPeriod time.Duration
}

func NewCacheService(genaiClient GenAIClient, db DBOperations, chatService ChatService, projectID string, options ...CacheServiceOption) *CacheService {
	cs := &CacheService{
		genaiClient:       genaiClient,
		chatService:       chatService,
		db:                db,
		arxivBaseURL:      "https://arxiv.org/pdf/",
		projectID:         projectID,
		expirationTime:    10 * time.Minute,
		heartbeatTimeout:  1 * time.Minute,  // Timeout after 1 minute of no heartbeats
		sessionTimeout:    10 * time.Minute, // Timeout after 10 minutes of no activity
		cacheExtendPeriod: 5 * time.Minute,  // Extend cache every 5 minutes of activity
	}

	for _, option := range options {
		option(cs)
	}

	go cs.periodicCleanup()
	return cs
}

func (s *CacheService) CreateContentCache(ctx context.Context, arxivIDs []string, userPDFs []string, cacheExpirationTTL time.Duration) (string, error) {

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

	cachedContent, err := s.genaiClient.CreateCachedContent(ctx, cc)
	if err != nil {
		return "", fmt.Errorf("failed to create cached content: %v", err)
	}

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
		if pdfPath == "" {
			continue
		}
		content, err := s.processUserPDF(pdfPath)
		if err != nil {
			return "", fmt.Errorf("failed to process PDF %s: %v", pdfPath, err)
		}
		aggregatedContent.WriteString(fmt.Sprintf("<Document>\n<title>User PDF %d</title>\n%s\n</Document>\n", i+1, content))
	}

	return aggregatedContent.String(), nil
}

func (s *CacheService) processArXivPaper(arxivID string) (string, error) {

	// Download the PDF from arXiv
	pdfURL := fmt.Sprintf("%s%s.pdf", s.arxivBaseURL, arxivID)
	resp, err := http.Get(pdfURL)
	if err != nil {
		return "", fmt.Errorf("failed to download arXiv paper: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code when downloading arXiv paper: %d", resp.StatusCode)
	}

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

func (s *CacheService) processUserPDF(pdfPath string) (string, error) {
	return s.extractTextFromPDF(pdfPath)
}

func (s *CacheService) extractTextFromPDF(pdfPath string) (string, error) {
	f, r, err := pdf.Open(pdfPath)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %v", err)
	}
	defer f.Close()

	var content strings.Builder
	totalPage := r.NumPage()

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}

		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		content.WriteString(text)
		content.WriteString("\n\n") // Add separation between pages
	}

	if content.Len() == 0 {
		return "", fmt.Errorf("no text content extracted from PDF")
	}

	return content.String(), nil
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
func (s *CacheService) StartChatSession(c *gin.Context, cachedContentName string) (string, error) {
	cachedContent, err := s.genaiClient.GetCachedContent(c.Request.Context(), cachedContentName)
	if err != nil {
		return "", fmt.Errorf("failed to get cached content: %v", err)
	}
	model := s.genaiClient.GenerativeModelFromCachedContent(cachedContent)

	session := model.StartChat()
	sessionID := uuid.New().String()

	// Get the user from the Gin context
	user, exists := c.Get("user")
	if !exists {
		return "", fmt.Errorf("user not found in context")
	}

	userModel, ok := user.(*models.User)
	if !ok {
		return "", fmt.Errorf("invalid user type in context")
	}

	// Save the chat to the database
	if err := s.chatService.SaveChat(userModel.ID, sessionID); err != nil {
		return "", fmt.Errorf("failed to save chat: %v", err)
	}

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
			// Handle error if needed
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

	sessionInfo, exists := s.getAndUpdateSession(sessionID)
	if !exists {
		return nil, fmt.Errorf("chat session not found")
	}

	// Add formatting instruction to the message
	formattedMessage := fmt.Sprintf("%s\n\n"+
		"Format your answer in markdown with easily readable paragraphs. ",
		message)

	responseIterator := sessionInfo.Session.SendMessageStream(ctx, genai.Text(formattedMessage))

	return responseIterator, nil
}

func (s *CacheService) getAndUpdateSession(sessionID string) (ChatSessionInfo, bool) {
	s.sessionsMutex.Lock()
	defer s.sessionsMutex.Unlock()

	sessionInterface, ok := s.sessions.Load(sessionID)
	if !ok {
		return ChatSessionInfo{}, false
	}

	sessionInfo := sessionInterface.(ChatSessionInfo)

	sessionInfo.LastAccessed = time.Now()

	s.sessions.Store(sessionID, sessionInfo)

	return sessionInfo, true
}

func (s *CacheService) UpdateSessionChatHistory(sessionID, chatType, content string) error {
	s.sessionsMutex.Lock()
	defer s.sessionsMutex.Unlock()

	// Check if session exists
	_, exists := s.sessions.Load(sessionID)
	if !exists {
		return fmt.Errorf("session not found")
	}

	// Save the new message
	if err := s.chatService.SaveMessage(sessionID, chatType, content); err != nil {
		return fmt.Errorf("failed to save message: %v", err)
	}

	if sessionInterface, ok := s.sessions.Load(sessionID); ok {
		sessionInfo := sessionInterface.(ChatSessionInfo)
		sessionInfo.ChatHistory = append(sessionInfo.ChatHistory, ChatMessage{
			Type:      chatType,
			Content:   content,
			Timestamp: time.Now(),
		})
		s.sessions.Store(sessionID, sessionInfo)
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
		return nil
	}

	sessionInfo := sessionInterface.(ChatSessionInfo)
	s.sessions.Delete(sessionID)

	// Check if the cached content still exists before attempting to delete it
	_, err := s.genaiClient.GetCachedContent(ctx, sessionInfo.CachedContentName)
	if err != nil {
		// If the cached content doesn't exist, log it and return
		return nil
	}

	// Delete the cached content
	err = s.genaiClient.DeleteCachedContent(ctx, sessionInfo.CachedContentName)
	if err != nil {
		log.Printf("Failed to delete cached content: %v", err)
	}

	return nil
}
