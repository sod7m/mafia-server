package games

import (
	"testing"

	"mafia-server/internal/domain"
)

func TestStartGameCreatesNightRoundOneSnapshot(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Owner", IsOwner: true},
			{ID: "user-2", Nickname: "Joiner", IsOwner: false},
		},
	}

	game := service.StartGame(room)

	if game.ID == "" {
		t.Fatal("expected game id")
	}
	if game.RoomID != room.ID {
		t.Fatalf("expected room id %q, got %q", room.ID, game.RoomID)
	}
	if game.Phase != domain.GamePhaseNight {
		t.Fatalf("expected night phase, got %q", game.Phase)
	}
	if game.Round != 1 {
		t.Fatalf("expected round 1, got %d", game.Round)
	}
	if len(game.Players) != 2 {
		t.Fatalf("expected 2 players, got %d", len(game.Players))
	}
	if !game.Players[0].IsAlive {
		t.Fatal("expected players to start alive")
	}
}

func TestStartGameIsIdempotentPerRoom(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Owner", IsOwner: true},
		},
	}

	first := service.StartGame(room)
	second := service.StartGame(room)

	if first.ID != second.ID {
		t.Fatalf("expected same game id, got %q and %q", first.ID, second.ID)
	}
}

func TestSetPhaseUpdatesPhaseAndRound(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Owner", IsOwner: true},
		},
	}

	game := service.StartGame(room)
	if game.Round != 1 {
		t.Fatalf("expected initial round 1, got %d", game.Round)
	}

	game, err := service.SetPhase(room.ID, domain.GamePhaseDay)
	if err != nil {
		t.Fatalf("SetPhase day returned error: %v", err)
	}
	if game.Phase != domain.GamePhaseDay {
		t.Fatalf("expected day phase, got %q", game.Phase)
	}
	if game.Round != 1 {
		t.Fatalf("expected day to keep round 1, got %d", game.Round)
	}

	game, err = service.SetPhase(room.ID, domain.GamePhaseNight)
	if err != nil {
		t.Fatalf("SetPhase night returned error: %v", err)
	}
	if game.Round != 2 {
		t.Fatalf("expected next night to increment round to 2, got %d", game.Round)
	}
}

func TestSetPhaseRejectsInvalidPhase(t *testing.T) {
	service := NewService()
	room := domain.Room{ID: "room-1"}
	service.StartGame(room)

	if _, err := service.SetPhase(room.ID, domain.GamePhase("bad")); err != ErrInvalidPhase {
		t.Fatalf("expected ErrInvalidPhase, got %v", err)
	}
}
