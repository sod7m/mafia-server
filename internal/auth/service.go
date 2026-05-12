package auth

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"mafia-server/internal/domain"
	"mafia-server/internal/ids"
	"mafia-server/internal/persistence"
)

const sessionTTL = 24 * time.Hour

var (
	ErrInvalidNickname = errors.New("nickname must be between 2 and 20 characters")
	ErrUnauthorized    = errors.New("missing or invalid session token")
)

type LoginResult struct {
	User  domain.UserSession `json:"user"`
	Token string             `json:"token"`
}

type sessionEntry struct {
	User      domain.UserSession
	ExpiresAt time.Time
}

type Service struct {
	mu       sync.RWMutex
	sessions map[string]sessionEntry
	store    persistence.Store
}

func NewService() *Service {
	return NewServiceWithStore(persistence.NewNoopStore())
}

func NewServiceWithStore(store persistence.Store) *Service {
	if store == nil {
		store = persistence.NewNoopStore()
	}

	service := &Service{
		sessions: make(map[string]sessionEntry),
		store:    store,
	}

	// Load legacy flat map (map[string]domain.UserSession) and convert to sessionEntry.
	var loaded map[string]domain.UserSession
	err := store.Load("auth.sessions", &loaded)
	switch {
	case errors.Is(err, persistence.ErrNotFound):
	case err != nil:
		log.Printf("auth: cannot load sessions from store: %v", err)
	default:
		expiry := time.Now().UTC().Add(sessionTTL)
		for token, user := range loaded {
			service.sessions[token] = sessionEntry{User: user, ExpiresAt: expiry}
		}
	}

	return service
}

// persistLocked saves sessions as a flat map[token]UserSession for store compatibility.
func (s *Service) persistLocked() {
	flat := make(map[string]domain.UserSession, len(s.sessions))
	for token, entry := range s.sessions {
		flat[token] = entry.User
	}
	if err := s.store.Save("auth.sessions", flat); err != nil {
		log.Printf("auth: cannot persist sessions: %v", err)
	}
}

// evictExpiredLocked removes sessions whose TTL has elapsed. Must be called with s.mu held.
func (s *Service) evictExpiredLocked(now time.Time) {
	for token, entry := range s.sessions {
		if now.After(entry.ExpiresAt) {
			delete(s.sessions, token)
		}
	}
}

func (s *Service) Login(nickname string) (LoginResult, error) {
	cleanNickname := strings.TrimSpace(nickname)
	if len([]rune(cleanNickname)) < 2 || len([]rune(cleanNickname)) > 20 {
		return LoginResult{}, ErrInvalidNickname
	}

	now := time.Now().UTC()
	user := domain.UserSession{
		ID:       ids.NewID("usr"),
		Nickname: cleanNickname,
	}
	token := ids.NewToken()

	s.mu.Lock()
	s.evictExpiredLocked(now)
	s.sessions[token] = sessionEntry{User: user, ExpiresAt: now.Add(sessionTTL)}
	s.persistLocked()
	s.mu.Unlock()

	return LoginResult{User: user, Token: token}, nil
}

func (s *Service) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.persistLocked()
	s.mu.Unlock()
}

func (s *Service) UserByToken(token string) (domain.UserSession, bool) {
	s.mu.RLock()
	entry, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok {
		return domain.UserSession{}, false
	}
	if time.Now().UTC().After(entry.ExpiresAt) {
		// Expired — evict lazily on next write; treat as not found.
		return domain.UserSession{}, false
	}
	return entry.User, true
}

func TokenFromRequest(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return ""
	}

	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}

	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}
