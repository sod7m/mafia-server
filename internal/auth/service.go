package auth

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"

	"mafia-server/internal/domain"
	"mafia-server/internal/ids"
	"mafia-server/internal/persistence"
)

var (
	ErrInvalidNickname = errors.New("nickname must be between 2 and 20 characters")
	ErrUnauthorized    = errors.New("missing or invalid session token")
)

type LoginResult struct {
	User  domain.UserSession `json:"user"`
	Token string             `json:"token"`
}

type Service struct {
	mu       sync.RWMutex
	sessions map[string]domain.UserSession
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
		sessions: make(map[string]domain.UserSession),
		store:    store,
	}

	var loaded map[string]domain.UserSession
	err := store.Load("auth.sessions", &loaded)
	switch {
	case errors.Is(err, persistence.ErrNotFound):
	case err != nil:
		log.Printf("auth: cannot load sessions from store: %v", err)
	default:
		service.sessions = loaded
	}

	return service
}

func (s *Service) persistLocked() {
	if err := s.store.Save("auth.sessions", s.sessions); err != nil {
		log.Printf("auth: cannot persist sessions: %v", err)
	}
}

func (s *Service) Login(nickname string) (LoginResult, error) {
	cleanNickname := strings.TrimSpace(nickname)
	if len([]rune(cleanNickname)) < 2 || len([]rune(cleanNickname)) > 20 {
		return LoginResult{}, ErrInvalidNickname
	}

	user := domain.UserSession{
		ID:       ids.NewID("usr"),
		Nickname: cleanNickname,
	}
	token := ids.NewToken()

	s.mu.Lock()
	s.sessions[token] = user
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
	user, ok := s.sessions[token]
	s.mu.RUnlock()
	return user, ok
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
