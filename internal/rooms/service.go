package rooms

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"mafia-server/internal/domain"
	"mafia-server/internal/ids"
)

var (
	ErrInvalidRoomName  = errors.New("room name is required")
	ErrRoomNotFound     = errors.New("room not found")
	ErrRoomUnavailable  = errors.New("room is not available")
	ErrRoomFull         = errors.New("room is full")
	ErrNotParticipant   = errors.New("user is not a room participant")
	ErrNotOwner         = errors.New("user is not the room owner")
	ErrNotEnoughPlayers = errors.New("not enough players to start")
)

type Service struct {
	mu    sync.RWMutex
	rooms map[string]domain.Room
}

func NewService() *Service {
	service := &Service{
		rooms: make(map[string]domain.Room),
	}
	service.seed()
	return service
}

func (s *Service) AvailableRooms() []domain.Room {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rooms := make([]domain.Room, 0, len(s.rooms))
	for _, room := range s.rooms {
		if domain.IsLobbyStatus(room.Status) {
			rooms = append(rooms, cloneRoom(room))
		}
	}

	sortRooms(rooms)
	return rooms
}

func (s *Service) GetRoom(roomID string) (domain.Room, bool) {
	s.mu.RLock()
	room, ok := s.rooms[roomID]
	s.mu.RUnlock()
	if !ok {
		return domain.Room{}, false
	}

	return cloneRoom(room), true
}

func (s *Service) CreateRoom(user domain.UserSession, name string, maxPlayers int) (domain.Room, error) {
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		return domain.Room{}, ErrInvalidRoomName
	}

	if maxPlayers < domain.MinPlayersInRoom {
		maxPlayers = domain.MinPlayersInRoom
	}
	if maxPlayers > domain.MaxPlayersInRoom {
		maxPlayers = domain.MaxPlayersInRoom
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	room := domain.Room{
		ID:         ids.NewID("room"),
		Code:       ids.NewRoomCode(s.existingCodesLocked()),
		Name:       cleanName,
		Status:     domain.RoomStatusWaiting,
		MaxPlayers: maxPlayers,
		OwnerID:    user.ID,
		Players: []domain.RoomPlayer{
			{
				ID:       user.ID,
				Nickname: user.Nickname,
				IsOwner:  true,
			},
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}

	s.rooms[room.ID] = room
	return cloneRoom(room), nil
}

func (s *Service) JoinRoom(user domain.UserSession, roomID string) (domain.Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return domain.Room{}, ErrRoomNotFound
	}

	if !domain.IsLobbyStatus(room.Status) {
		return domain.Room{}, ErrRoomUnavailable
	}

	if playerIndex(room.Players, user.ID) >= 0 {
		return cloneRoom(room), nil
	}

	if len(room.Players) >= room.MaxPlayers {
		return domain.Room{}, ErrRoomFull
	}

	room.Players = append(room.Players, domain.RoomPlayer{
		ID:       user.ID,
		Nickname: user.Nickname,
		IsOwner:  room.OwnerID == user.ID,
	})
	room.Status = domain.DeriveLobbyStatus(len(room.Players), room.MaxPlayers)

	s.rooms[room.ID] = room
	return cloneRoom(room), nil
}

func (s *Service) LeaveRoom(user domain.UserSession, roomID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return ErrRoomNotFound
	}

	index := playerIndex(room.Players, user.ID)
	if index < 0 {
		return nil
	}

	room.Players = append(room.Players[:index], room.Players[index+1:]...)
	if len(room.Players) == 0 {
		delete(s.rooms, room.ID)
		return nil
	}

	normalizeOwner(&room)
	if domain.IsLobbyStatus(room.Status) {
		room.Status = domain.DeriveLobbyStatus(len(room.Players), room.MaxPlayers)
	}

	s.rooms[room.ID] = room
	return nil
}

func (s *Service) StartRoom(user domain.UserSession, roomID string) (domain.Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return domain.Room{}, ErrRoomNotFound
	}

	index := playerIndex(room.Players, user.ID)
	if index < 0 {
		return domain.Room{}, ErrNotParticipant
	}

	if room.OwnerID != user.ID {
		return domain.Room{}, ErrNotOwner
	}

	if !domain.IsLobbyStatus(room.Status) {
		return domain.Room{}, ErrRoomUnavailable
	}

	if len(room.Players) < domain.MinPlayersToStart {
		return domain.Room{}, ErrNotEnoughPlayers
	}

	room.Status = domain.RoomStatusInProgress
	s.rooms[room.ID] = room
	return cloneRoom(room), nil
}

func (s *Service) seed() {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	seedRooms := []domain.Room{
		{
			ID:         "bronx-night",
			Code:       "BRX731",
			Name:       "Бронкс після заходу",
			Status:     domain.RoomStatusWaiting,
			MaxPlayers: 12,
			OwnerID:    "seed-owner-1",
			Players: []domain.RoomPlayer{
				{ID: "seed-owner-1", Nickname: "DonVito", IsOwner: true},
				{ID: "seed-player-1", Nickname: "Capo77", IsOwner: false},
			},
			CreatedAt: now,
		},
		{
			ID:         "silent-table",
			Code:       "SLN502",
			Name:       "Тиха переговорна",
			Status:     domain.RoomStatusRecruiting,
			MaxPlayers: 10,
			OwnerID:    "seed-owner-2",
			Players: []domain.RoomPlayer{
				{ID: "seed-owner-2", Nickname: "Detective", IsOwner: true},
				{ID: "seed-player-2", Nickname: "Shadow", IsOwner: false},
				{ID: "seed-player-3", Nickname: "Medic", IsOwner: false},
				{ID: "seed-player-4", Nickname: "Margo", IsOwner: false},
			},
			CreatedAt: now,
		},
	}

	for _, room := range seedRooms {
		s.rooms[room.ID] = room
	}
}

func (s *Service) existingCodesLocked() map[string]struct{} {
	codes := make(map[string]struct{}, len(s.rooms))
	for _, room := range s.rooms {
		codes[room.Code] = struct{}{}
	}
	return codes
}

func normalizeOwner(room *domain.Room) {
	ownerIndex := playerIndex(room.Players, room.OwnerID)
	if ownerIndex < 0 {
		room.OwnerID = room.Players[0].ID
	}

	for index := range room.Players {
		room.Players[index].IsOwner = room.Players[index].ID == room.OwnerID
	}
}

func playerIndex(players []domain.RoomPlayer, userID string) int {
	for index, player := range players {
		if player.ID == userID {
			return index
		}
	}
	return -1
}

func cloneRoom(room domain.Room) domain.Room {
	room.Players = append([]domain.RoomPlayer(nil), room.Players...)
	return room
}

func sortRooms(rooms []domain.Room) {
	sort.SliceStable(rooms, func(i, j int) bool {
		return rooms[i].CreatedAt > rooms[j].CreatedAt
	})
}
