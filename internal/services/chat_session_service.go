package services

import (
	"context"
	"errors"
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
}

type ChatMessage struct {
	Type      string
	Content   string
	Timestamp time.Time
}

type ChatSessionService struct {
	sessions      sync.Map
	sessionsMutex sync.RWMutex
	genAIClient   GenAIClient
	chatService   ChatServiceDB
	CacheManager  CacheManager
	cfg           *config.Config
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
		sessions:     sync.Map{},
	}
	go css.periodicCleanup()
	return css
}

func (css *ChatSessionService) StartChatSession(ctx context.Context, userID uuid.UUID, cachedContentName string, sessionID string, cacheCreateTime time.Time) error {
	// Get the GenerativeModel using the CacheManagementService
	model, err := css.CacheManager.GetGenerativeModel(ctx, cachedContentName)
	if err != nil {
		return err
	}

	session := model.StartChat()

	if err := css.chatService.SaveChatToDB(userID, sessionID); err != nil {
		return err
	}

	css.sessionsMutex.Lock()
	defer css.sessionsMutex.Unlock()

	css.sessions.Store(sessionID, ChatSessionInfo{
		Session:           session,
		CachedContentName: cachedContentName,
		LastActivity:      time.Now(),
		CacheExpiresAt:    cacheCreateTime,
		UserID:            userID,
	})

	return nil
}

func (css *ChatSessionService) CheckSessionStatus(sessionID string) (SessionStatus, error) {
	css.sessionsMutex.RLock()
	defer css.sessionsMutex.RUnlock()

	sessionInfo, ok := css.sessions.Load(sessionID)
	if !ok {
		return Expired, ErrSessionNotFound
	}

	info := sessionInfo.(ChatSessionInfo)
	now := time.Now()
	inactivityDuration := now.Sub(info.LastActivity)

	if inactivityDuration >= css.cfg.SessionTimeout {
		return Expired, nil
	} else if inactivityDuration >= (css.cfg.SessionTimeout - css.cfg.GracePeriod) {
		if info.WarningTime.IsZero() {
			info.WarningTime = now
			css.sessions.Store(sessionID, info)
		}
		return Warning, nil
	}

	return Active, nil
}

func (css *ChatSessionService) UpdateSessionActivity(ctx context.Context, sessionID string) error {
	css.sessionsMutex.Lock()
	defer css.sessionsMutex.Unlock()

	sessionInterface, ok := css.sessions.Load(sessionID)
	if !ok {
		return errors.New("session not found")
	}

	sessionInfo := sessionInterface.(ChatSessionInfo)
	now := time.Now()
	sessionInfo.LastActivity = now
	sessionInfo.WarningTime = time.Time{} // Reset warning time

	// Extend cache if necessary TODO: Check if expirytime is in less than inactivity time + 30 seconds
	if now.After(sessionInfo.CacheExpiresAt.Add(-css.cfg.SessionTimeout - 30*time.Second)) {
		newExpirationTime := sessionInfo.CacheExpiresAt.Add(css.cfg.CacheExtendPeriod)
		if err := css.CacheManager.ExtendCacheLifetime(ctx, sessionInfo.CachedContentName, newExpirationTime); err != nil {
			log.Printf("Failed to extend cache lifetime for session %s: %v", sessionID, err)
		} else {
			sessionInfo.CacheExpiresAt = newExpirationTime
			log.Printf("Extended cache lifetime for session %s to %v", sessionID, newExpirationTime)
		}
	}

	css.sessions.Store(sessionID, sessionInfo)
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
	defer css.sessionsMutex.Unlock()

	sessionInterface, ok := css.sessions.Load(sessionID)
	if !ok {
		return ErrSessionNotFound
	}

	sessionInfo := sessionInterface.(ChatSessionInfo)

	// Delete the cached content when terminating the session
	if err := css.CacheManager.DeleteCache(ctx, sessionInfo.UserID, sessionID, sessionInfo.CachedContentName); err != nil {
		log.Printf("Failed to delete cached content for session %s: %v", sessionID, err)
	}

	// Remove the session from the map
	css.sessions.Delete(sessionID)

	// Log the termination
	log.Printf("Session %s terminated. Reason: %v", sessionID, reason)

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

func (css *ChatSessionService) getAndUpdateSession(sessionID string) (ChatSessionInfo, bool) {
	css.sessionsMutex.Lock()
	defer css.sessionsMutex.Unlock()

	sessionInterface, ok := css.sessions.Load(sessionID)
	if !ok {
		return ChatSessionInfo{}, false
	}

	sessionInfo := sessionInterface.(ChatSessionInfo)
	sessionInfo.LastActivity = time.Now()
	css.sessions.Store(sessionID, sessionInfo)

	return sessionInfo, true
}

func (css *ChatSessionService) formatMessage(message string) string {
	return message + "\n\nFormat your answer in markdown with easily readable paragraphs."
}

func (css *ChatSessionService) periodicCleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		css.CleanupExpiredSessions()
	}
}

// Modify the CleanupExpiredSessions method
func (css *ChatSessionService) CleanupExpiredSessions() {
	css.sessions.Range(func(key, value interface{}) bool {
		sessionID := key.(string)

		status, _ := css.CheckSessionStatus(sessionID)
		if status == Expired {
			if err := css.TerminateSession(context.Background(), sessionID, SessionTimeout); err != nil {
				log.Printf("Failed to terminate session %s: %v", sessionID, err)
			}
		}

		return true
	})
}

func (css *ChatSessionService) Sessions() *sync.Map {
	return &css.sessions
}
