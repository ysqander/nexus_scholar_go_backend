package services

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
)

type ChatSessionInfo struct {
	Session           *genai.ChatSession
	CachedContentName string
	LastAccessed      time.Time
	LastHeartbeat     time.Time
	HeartbeatsMissed  int
	CacheExpiresAt    time.Time
	UserID            uuid.UUID
}

type ChatMessage struct {
	Type      string
	Content   string
	Timestamp time.Time
}

type ChatSessionService struct {
	sessions          sync.Map
	sessionsMutex     sync.RWMutex
	genAIClient       GenAIClient
	chatService       ChatServiceDB
	CacheManager      CacheManager
	heartbeatTimeout  time.Duration
	sessionTimeout    time.Duration
	heartbeatInterval time.Duration
	cacheExtendPeriod time.Duration
}

func NewChatSessionService(
	genAIClient GenAIClient,
	chatService ChatServiceDB,
	CacheManager CacheManager,
	heartbeatTimeout,
	sessionTimeout time.Duration,
	heartbeatInterval time.Duration,
	cacheExtendPeriod time.Duration,
) *ChatSessionService {
	css := &ChatSessionService{
		genAIClient:       genAIClient,
		chatService:       chatService,
		CacheManager:      CacheManager,
		heartbeatTimeout:  heartbeatTimeout,
		sessionTimeout:    sessionTimeout,
		heartbeatInterval: heartbeatInterval,
		cacheExtendPeriod: cacheExtendPeriod,
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
		LastAccessed:      time.Now(),
		LastHeartbeat:     time.Now(),
		HeartbeatsMissed:  0,
		CacheExpiresAt:    cacheCreateTime,
		UserID:            userID,
	})

	return nil
}

func (css *ChatSessionService) UpdateSessionHeartbeat(ctx context.Context, sessionID string) error {
	css.sessionsMutex.Lock()
	defer css.sessionsMutex.Unlock()

	sessionInterface, ok := css.sessions.Load(sessionID)
	if !ok {
		return errors.New("session not found")
	}

	sessionInfo := sessionInterface.(ChatSessionInfo)
	now := time.Now()
	sessionInfo.LastHeartbeat = now
	sessionInfo.HeartbeatsMissed = 0
	sessionInfo.LastAccessed = now

	// Calculate the time until cache expiration
	timeUntilExpiration := sessionInfo.CacheExpiresAt.Sub(now)

	// Check if it's time to extend the cache
	// Extend if expiration is between 2 and 3 heartbeat intervals away
	if timeUntilExpiration > 2*css.heartbeatInterval && timeUntilExpiration <= 3*css.heartbeatInterval {
		newExpirationTime := sessionInfo.CacheExpiresAt.Add(css.cacheExtendPeriod)
		if err := css.CacheManager.ExtendCacheLifetime(ctx, sessionInfo.CachedContentName, newExpirationTime); err != nil {
			// Log the error, but don't fail the heartbeat update
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
	HeartbeatMissed
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
	sessionInfo.LastAccessed = time.Now()
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

func (css *ChatSessionService) CleanupExpiredSessions() {
	now := time.Now()
	css.sessions.Range(func(key, value interface{}) bool {
		sessionID := key.(string)
		sessionInfo := value.(ChatSessionInfo)

		var reason TerminationReason
		var shouldTerminate bool

		if now.Sub(sessionInfo.LastAccessed) > css.sessionTimeout {
			reason = SessionTimeout
			shouldTerminate = true
		} else if now.Sub(sessionInfo.LastHeartbeat) > css.heartbeatTimeout {
			sessionInfo.HeartbeatsMissed++
			if sessionInfo.HeartbeatsMissed >= 3 {
				reason = HeartbeatMissed
				shouldTerminate = true
			} else {
				css.sessions.Store(sessionID, sessionInfo)
			}
		}

		if shouldTerminate {
			if err := css.TerminateSession(context.Background(), sessionID, reason); err != nil {
				log.Printf("Failed to terminate session %s: %v", sessionID, err)
			}
		}

		return true
	})
}

func (css *ChatSessionService) Sessions() *sync.Map {
	return &css.sessions
}
