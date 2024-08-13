package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"nexus_scholar_go_backend/cmd/api/config"
	"sync"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
)

// Add this new struct
type SessionStatus int

const (
	Active SessionStatus = iota
	Warning
	Expired
)

type ChatSessionInfo struct {
	Session           *genai.ChatSession
	CachedContentName string
	LastActivity      time.Time
	CacheExpiresAt    time.Time
	UserID            uuid.UUID
	WarningTime       time.Time
	mutex             *sync.RWMutex
}

type ChatMessage struct {
	Type      string
	Content   string
	Timestamp time.Time
}

type ChatSessionService struct {
	sessions      map[string]*ChatSessionInfo
	sessionsMutex sync.RWMutex
	genAIClient   GenAIClient
	chatService   ChatServiceDB
	CacheManager  CacheManager
	cfg           *config.Config
}

type SessionStatusInfo struct {
	Status        string    `json:"status"`
	ExpiryTime    time.Time `json:"expiryTime"`
	TimeRemaining int       `json:"timeRemaining"` // in seconds
}

func NewChatSessionService(
	genAIClient GenAIClient,
	chatService ChatServiceDB,
	CacheManager CacheManager,
	cfg *config.Config,
) *ChatSessionService {
	css := &ChatSessionService{
		genAIClient:  genAIClient,
		chatService:  chatService,
		CacheManager: CacheManager,
		cfg:          cfg,
		sessions:     make(map[string]*ChatSessionInfo),
	}
	go css.periodicCleanup()
	return css
}

func (css *ChatSessionService) StartChatSession(ctx context.Context, userID uuid.UUID, cachedContentName string, sessionID string, cacheExpiryTime time.Time) error {
	fmt.Printf("DEBUG: Cache expiry time: %v\n", cacheExpiryTime)
	// Get the GenerativeModel using the CacheManagementService
	model, err := css.CacheManager.GetGenerativeModel(ctx, cachedContentName)
	if err != nil {
		return err
	}

	session := model.StartChat()

	if err := css.chatService.SaveChatToDB(userID, sessionID); err != nil {
		return err
	}
	fmt.Printf("DEBUG: Saved chat session to history in DB for user ID: %s, session ID: %s\n", userID, sessionID)

	css.sessionsMutex.Lock()
	defer css.sessionsMutex.Unlock()

	css.sessions[sessionID] = &ChatSessionInfo{
		Session:           session,
		CachedContentName: cachedContentName,
		LastActivity:      time.Now(),
		CacheExpiresAt:    cacheExpiryTime,
		UserID:            userID,
		mutex:             &sync.RWMutex{},
	}
	return nil
}

func (css *ChatSessionService) CheckSessionStatus(sessionID string) (SessionStatus, time.Time, error) {
	css.sessionsMutex.RLock()
	sessionInfo, ok := css.sessions[sessionID]
	css.sessionsMutex.RUnlock()

	if !ok {
		log.Printf("DEBUG: Session not found for ID: %s", sessionID)
		return Expired, time.Time{}, ErrSessionNotFound
	}

	sessionInfo.mutex.RLock()
	defer sessionInfo.mutex.RUnlock()

	now := time.Now()

	if now.After(sessionInfo.CacheExpiresAt) {
		log.Printf("DEBUG: Session expired for ID: %s", sessionID)
		return Expired, time.Time{}, nil
	} else if now.After(sessionInfo.CacheExpiresAt.Add(-css.cfg.GracePeriod)) {
		log.Printf("DEBUG: Session in warning state for ID: %s", sessionID)
		return Warning, sessionInfo.CacheExpiresAt, nil
	}

	log.Printf("DEBUG: Session active for ID: %s", sessionID)
	return Active, sessionInfo.CacheExpiresAt, nil
}

func (css *ChatSessionService) UpdateSessionActivity(ctx context.Context, sessionID string) error {
	css.sessionsMutex.RLock()
	sessionInfo, ok := css.sessions[sessionID]
	css.sessionsMutex.RUnlock()

	if !ok {
		return errors.New("session not found")
	}

	sessionInfo.mutex.Lock()
	defer sessionInfo.mutex.Unlock()

	now := time.Now()
	sessionInfo.LastActivity = now
	cacheExpiresAt := sessionInfo.CacheExpiresAt

	// Extend cache if necessary
	if now.After(cacheExpiresAt.Add(-css.cfg.GracePeriod)) {
		newExpirationTime := cacheExpiresAt.Add(css.cfg.CacheExpirationTime)
		if err := css.CacheManager.ExtendCacheLifetime(ctx, sessionInfo.CachedContentName, newExpirationTime); err != nil {
			log.Printf("Failed to extend cache lifetime for session %s: %v", sessionID, err)
		} else {
			sessionInfo.CacheExpiresAt = newExpirationTime
			log.Printf("Extended cache lifetime for session %s to %v", sessionID, newExpirationTime)
		}
	}

	return nil
}

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrCacheDelete     = errors.New("failed to delete cache")
	ErrDBDelete        = errors.New("failed to delete chat from database")
)

type TerminationReason int

const (
	UserInitiated TerminationReason = iota
	SessionTimeout
)

func (css *ChatSessionService) TerminateSession(ctx context.Context, sessionID string, reason TerminationReason) error {
	css.sessionsMutex.Lock()
	sessionInfo, ok := css.sessions[sessionID]
	if !ok {
		css.sessionsMutex.Unlock()
		return ErrSessionNotFound
	}
	delete(css.sessions, sessionID)
	css.sessionsMutex.Unlock()

	// Use defer to ensure we always log the termination
	defer log.Printf("Session %s terminated. Reason: %v", sessionID, reason)

	// Attempt to delete the cached content
	err := css.CacheManager.DeleteCache(ctx, sessionInfo.UserID, sessionID, sessionInfo.CachedContentName)
	if err != nil {
		log.Printf("Failed to delete cached content for session %s: %v", sessionID, err)
		// Don't return here, continue with termination even if cache deletion fails
	}

	return nil
}

func (css *ChatSessionService) StreamChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponseIterator, error) {
	sessionInfo, exists := css.getAndUpdateSession(sessionID)
	if !exists {
		return nil, errors.New("chat session not found")
	}

	formattedMessage := css.formatMessage(message)
	responseIterator := sessionInfo.Session.SendMessageStream(ctx, genai.Text(formattedMessage))

	return responseIterator, nil
}

func (css *ChatSessionService) getAndUpdateSession(sessionID string) (*ChatSessionInfo, bool) {
	css.sessionsMutex.RLock()
	sessionInfo, ok := css.sessions[sessionID]
	css.sessionsMutex.RUnlock()

	if !ok {
		return nil, false
	}

	sessionInfo.mutex.Lock()
	defer sessionInfo.mutex.Unlock()

	sessionInfo.LastActivity = time.Now()

	return sessionInfo, true
}

func (css *ChatSessionService) formatMessage(message string) string {
	return message + "\n\nFormat your answer in markdown with easily readable paragraphs."
}

func (css *ChatSessionService) periodicCleanup() {
	cacheCleanupTicker := time.NewTicker(css.cfg.CacheCleanupDelay)
	sessionCleanupTicker := time.NewTicker(css.cfg.SessionMemoryTimeout)

	for {
		select {
		case <-cacheCleanupTicker.C:
			css.cleanupExpiredCaches()
		case <-sessionCleanupTicker.C:
			css.cleanupExpiredSessions()
		}
	}
}

func (css *ChatSessionService) cleanupExpiredCaches() {
	css.sessionsMutex.Lock()
	defer css.sessionsMutex.Unlock()

	now := time.Now()
	for sessionID, sessionInfo := range css.sessions {
		if now.After(sessionInfo.CacheExpiresAt) {
			log.Printf("DEBUG: Attempting to clean up expired cache for session %s", sessionID)
			ctx := context.Background()
			err := css.CacheManager.RecordCacheTokenUsage(ctx, sessionInfo.UserID, sessionID)
			if err != nil {

				log.Printf("ERROR: Failed to record cache token usage for session %s: %v", sessionID, err)
			} else {
				log.Printf("DEBUG: Successfully recorded cache token usage for session %s", sessionID)
			}
		}
	}
}

func (css *ChatSessionService) cleanupExpiredSessions() {
	css.sessionsMutex.Lock()
	defer css.sessionsMutex.Unlock()

	now := time.Now()
	for sessionID, sessionInfo := range css.sessions {
		if now.After(sessionInfo.CacheExpiresAt.Add(css.cfg.SessionMemoryTimeout)) {
			delete(css.sessions, sessionID)
			log.Printf("Removed expired session %s from memory", sessionID)
		}
	}
}

func (css *ChatSessionService) Sessions() map[string]*ChatSessionInfo {
	css.sessionsMutex.RLock()
	defer css.sessionsMutex.RUnlock()

	// Create a copy of the sessions map to avoid concurrent access issues
	sessionsCopy := make(map[string]*ChatSessionInfo, len(css.sessions))
	for k, v := range css.sessions {
		sessionsCopy[k] = v
	}

	return sessionsCopy
}

func (css *ChatSessionService) GetSessionStatus(sessionID string) (SessionStatusInfo, error) {
	css.sessionsMutex.RLock()
	sessionInfo, ok := css.sessions[sessionID]
	css.sessionsMutex.RUnlock()

	if !ok {
		return SessionStatusInfo{}, ErrSessionNotFound
	}

	sessionInfo.mutex.RLock()
	defer sessionInfo.mutex.RUnlock()
	now := time.Now()
	timeRemaining := int(sessionInfo.CacheExpiresAt.Sub(now).Seconds())

	status := "active"
	if now.After(sessionInfo.CacheExpiresAt) {
		status = "expired"
		timeRemaining = 0
	} else if now.After(sessionInfo.CacheExpiresAt.Add(-css.cfg.GracePeriod)) {
		status = "warning"
	}

	return SessionStatusInfo{
		Status:        status,
		ExpiryTime:    sessionInfo.CacheExpiresAt,
		TimeRemaining: timeRemaining,
	}, nil
}

func (css *ChatSessionService) ExtendSession(ctx context.Context, sessionID string) error {
	css.sessionsMutex.RLock()
	sessionInfo, ok := css.sessions[sessionID]
	css.sessionsMutex.RUnlock()

	if !ok {
		return ErrSessionNotFound
	}

	sessionInfo.mutex.Lock()
	defer sessionInfo.mutex.Unlock()

	newExpirationTime := time.Now().Add(css.cfg.CacheExpirationTime)

	if err := css.CacheManager.ExtendCacheLifetime(ctx, sessionInfo.CachedContentName, newExpirationTime); err != nil {
		return fmt.Errorf("failed to extend cache lifetime: %w", err)
	}

	sessionInfo.CacheExpiresAt = newExpirationTime

	return nil
}
