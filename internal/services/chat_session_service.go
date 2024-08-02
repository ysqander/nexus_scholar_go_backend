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
	LastCacheExtend   time.Time
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
	cacheExtendPeriod time.Duration
}

func NewChatSessionService(
	genAIClient GenAIClient,
	chatService ChatServiceDB,
	CacheManager CacheManager,
	heartbeatTimeout,
	sessionTimeout time.Duration,
) *ChatSessionService {
	css := &ChatSessionService{
		genAIClient:      genAIClient,
		chatService:      chatService,
		CacheManager:     CacheManager,
		heartbeatTimeout: heartbeatTimeout,
		sessionTimeout:   sessionTimeout,
	}
	go css.periodicCleanup()
	return css
}

func (css *ChatSessionService) StartChatSession(ctx context.Context, userID uuid.UUID, cachedContentName string) (string, error) {
	// Get the GenerativeModel using the CacheManagementService
	model, err := css.CacheManager.GetGenerativeModel(ctx, cachedContentName)
	if err != nil {
		return "", err
	}

	session := model.StartChat()
	sessionID := uuid.New().String()

	if err := css.chatService.SaveChatToDB(userID, sessionID); err != nil {
		return "", err
	}

	css.sessionsMutex.Lock()
	defer css.sessionsMutex.Unlock()

	css.sessions.Store(sessionID, ChatSessionInfo{
		Session:           session,
		CachedContentName: cachedContentName,
		LastAccessed:      time.Now(),
		LastHeartbeat:     time.Now(),
		HeartbeatsMissed:  0,
		LastCacheExtend:   time.Now(),
		UserID:            userID,
	})

	return sessionID, nil
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

	// Check if it's time to extend the cache
	if now.Sub(sessionInfo.LastCacheExtend) >= css.cacheExtendPeriod {
		if err := css.CacheManager.ExtendCacheLifetime(ctx, sessionInfo.CachedContentName); err != nil {
			// Log the error, but don't fail the heartbeat update
			// You might want to add proper logging here
		} else {
			sessionInfo.LastCacheExtend = now
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
	if err := css.CacheManager.DeleteCache(ctx, sessionInfo.CachedContentName); err != nil {
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
	ticker := time.NewTicker(3 * time.Minute)
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
