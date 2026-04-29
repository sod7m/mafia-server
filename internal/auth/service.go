package auth

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"mafia-server/internal/domain"
	"mafia-server/internal/ids"
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
}

func NewService() *Service {
	return &Service{
		sessions: make(map[string]domain.UserSession),
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
	s.mu.Unlock()

	return LoginResult{User: user, Token: token}, nil
}

func (s *Service) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
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
