package games

import (
	"errors"
	"slices"
	"sync"
	"time"

	"mafia-server/internal/domain"
	"mafia-server/internal/ids"
)

var (
	ErrGameNotFound = errors.New("game not found")
	ErrInvalidPhase = errors.New("invalid game phase")
)

var validPhases = []domain.GamePhase{
	domain.GamePhaseNight,
	domain.GamePhaseDay,
	domain.GamePhaseVoting,
	domain.GamePhaseFinal,
}

type Service struct {
	mu     sync.RWMutex
	byRoom map[string]domain.Game
}

func NewService() *Service {
	return &Service{
		byRoom: make(map[string]domain.Game),
	}
}

func (s *Service) StartGame(room domain.Room) domain.Game {
	s.mu.Lock()
	defer s.mu.Unlock()

	if game, ok := s.byRoom[room.ID]; ok {
		game.Players = playersFromRoom(room)
		game.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		s.byRoom[room.ID] = game
		return cloneGame(game)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	game := domain.Game{
		ID:        ids.NewID("game"),
		RoomID:    room.ID,
		Phase:     domain.GamePhaseNight,
		Round:     1,
		Players:   playersFromRoom(room),
		StartedAt: now,
		UpdatedAt: now,
	}

	s.byRoom[room.ID] = game
	return cloneGame(game)
}

func (s *Service) GetByRoomID(roomID string) (domain.Game, bool) {
	s.mu.RLock()
	game, ok := s.byRoom[roomID]
	s.mu.RUnlock()
	if !ok {
		return domain.Game{}, false
	}

	return cloneGame(game), true
}

func (s *Service) SetPhase(roomID string, phase domain.GamePhase) (domain.Game, error) {
	if !slices.Contains(validPhases, phase) {
		return domain.Game{}, ErrInvalidPhase
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	game, ok := s.byRoom[roomID]
	if !ok {
		return domain.Game{}, ErrGameNotFound
	}

	if phase == domain.GamePhaseNight && game.Phase != domain.GamePhaseNight {
		game.Round++
	}

	game.Phase = phase
	game.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.byRoom[roomID] = game
	return cloneGame(game), nil
}

func playersFromRoom(room domain.Room) []domain.GamePlayer {
	players := make([]domain.GamePlayer, 0, len(room.Players))
	for _, player := range room.Players {
		players = append(players, domain.GamePlayer{
			ID:       player.ID,
			Nickname: player.Nickname,
			IsOwner:  player.IsOwner,
			IsAlive:  true,
		})
	}

	return players
}

func cloneGame(game domain.Game) domain.Game {
	game.Players = append([]domain.GamePlayer(nil), game.Players...)
	return game
}
