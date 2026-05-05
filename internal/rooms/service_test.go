package rooms

import (
	"testing"

	"mafia-server/internal/domain"
)

func TestCreateRoomAddsOwner(t *testing.T) {
	service := NewService()
	user := domain.UserSession{ID: "user-1", Nickname: "DonVito"}

	room, err := service.CreateRoom(user, "Night table", 10)
	if err != nil {
		t.Fatalf("CreateRoom returned error: %v", err)
	}

	if room.Name != "Night table" {
		t.Fatalf("expected room name Night table, got %q", room.Name)
	}
	if room.OwnerID != user.ID {
		t.Fatalf("expected owner %q, got %q", user.ID, room.OwnerID)
	}
	if len(room.Players) != 1 {
		t.Fatalf("expected 1 player, got %d", len(room.Players))
	}
	if !room.Players[0].IsOwner {
		t.Fatal("expected creator to be owner")
	}
}

func TestJoinRoomDerivesRecruitingStatus(t *testing.T) {
	service := NewService()
	owner := domain.UserSession{ID: "owner", Nickname: "Owner"}
	joiner1 := domain.UserSession{ID: "joiner-1", Nickname: "Joiner1"}
	joiner2 := domain.UserSession{ID: "joiner-2", Nickname: "Joiner2"}

	room, err := service.CreateRoom(owner, "Night table", 6)
	if err != nil {
		t.Fatalf("CreateRoom returned error: %v", err)
	}

	if _, err := service.JoinRoom(joiner1, room.ID); err != nil {
		t.Fatalf("first JoinRoom returned error: %v", err)
	}
	joinedRoom, err := service.JoinRoom(joiner2, room.ID)
	if err != nil {
		t.Fatalf("second JoinRoom returned error: %v", err)
	}

	if joinedRoom.Status != domain.RoomStatusRecruiting {
		t.Fatalf("expected recruiting status, got %q", joinedRoom.Status)
	}
	if len(joinedRoom.Players) != 3 {
		t.Fatalf("expected 3 players, got %d", len(joinedRoom.Players))
	}
}

func TestLeaveRoomReassignsOwner(t *testing.T) {
	service := NewService()
	owner := domain.UserSession{ID: "owner", Nickname: "Owner"}
	joiner := domain.UserSession{ID: "joiner", Nickname: "Joiner"}

	room, err := service.CreateRoom(owner, "Night table", 6)
	if err != nil {
		t.Fatalf("CreateRoom returned error: %v", err)
	}
	if _, err := service.JoinRoom(joiner, room.ID); err != nil {
		t.Fatalf("JoinRoom returned error: %v", err)
	}

	if err := service.LeaveRoom(owner, room.ID); err != nil {
		t.Fatalf("LeaveRoom returned error: %v", err)
	}

	nextRoom, ok := service.GetRoom(room.ID)
	if !ok {
		t.Fatal("expected room to remain after owner leaves")
	}
	if nextRoom.OwnerID != joiner.ID {
		t.Fatalf("expected owner %q, got %q", joiner.ID, nextRoom.OwnerID)
	}
	if !nextRoom.Players[0].IsOwner {
		t.Fatal("expected remaining player to be marked owner")
	}
}

func TestStartRoomMovesToInProgress(t *testing.T) {
	service := NewService()
	user := domain.UserSession{ID: "user-1", Nickname: "DonVito"}

	room, err := service.CreateRoom(user, "Night table", 10)
	if err != nil {
		t.Fatalf("CreateRoom returned error: %v", err)
	}
	for _, joiner := range []domain.UserSession{
		{ID: "user-2", Nickname: "Joiner2"},
		{ID: "user-3", Nickname: "Joiner3"},
		{ID: "user-4", Nickname: "Joiner4"},
		{ID: "user-5", Nickname: "Joiner5"},
		{ID: "user-6", Nickname: "Joiner6"},
	} {
		if _, err := service.JoinRoom(joiner, room.ID); err != nil {
			t.Fatalf("JoinRoom returned error: %v", err)
		}
	}

	startedRoom, err := service.StartRoom(user, room.ID)
	if err != nil {
		t.Fatalf("StartRoom returned error: %v", err)
	}
	if startedRoom.Status != domain.RoomStatusInProgress {
		t.Fatalf("expected in_progress status, got %q", startedRoom.Status)
	}
}

func TestStartRoomRequiresMinimumPlayers(t *testing.T) {
	service := NewService()
	user := domain.UserSession{ID: "user-1", Nickname: "DonVito"}

	room, err := service.CreateRoom(user, "Night table", 10)
	if err != nil {
		t.Fatalf("CreateRoom returned error: %v", err)
	}

	if _, err := service.StartRoom(user, room.ID); err != ErrNotEnoughPlayers {
		t.Fatalf("expected ErrNotEnoughPlayers, got %v", err)
	}
}

func TestStartRoomRequiresOwner(t *testing.T) {
	service := NewService()
	owner := domain.UserSession{ID: "owner", Nickname: "Owner"}
	joiner := domain.UserSession{ID: "joiner", Nickname: "Joiner"}

	room, err := service.CreateRoom(owner, "Night table", 10)
	if err != nil {
		t.Fatalf("CreateRoom returned error: %v", err)
	}
	if _, err := service.JoinRoom(joiner, room.ID); err != nil {
		t.Fatalf("JoinRoom returned error: %v", err)
	}

	if _, err := service.StartRoom(joiner, room.ID); err != ErrNotOwner {
		t.Fatalf("expected ErrNotOwner, got %v", err)
	}
}

func TestStartRoomRejectsAlreadyStartedRoom(t *testing.T) {
	service := NewService()
	owner := domain.UserSession{ID: "owner", Nickname: "Owner"}

	room, err := service.CreateRoom(owner, "Night table", 10)
	if err != nil {
		t.Fatalf("CreateRoom returned error: %v", err)
	}
	for _, joiner := range []domain.UserSession{
		{ID: "user-2", Nickname: "Joiner2"},
		{ID: "user-3", Nickname: "Joiner3"},
		{ID: "user-4", Nickname: "Joiner4"},
		{ID: "user-5", Nickname: "Joiner5"},
		{ID: "user-6", Nickname: "Joiner6"},
	} {
		if _, err := service.JoinRoom(joiner, room.ID); err != nil {
			t.Fatalf("JoinRoom returned error: %v", err)
		}
	}

	if _, err := service.StartRoom(owner, room.ID); err != nil {
		t.Fatalf("first StartRoom returned error: %v", err)
	}
	if _, err := service.StartRoom(owner, room.ID); err != ErrRoomUnavailable {
		t.Fatalf("expected ErrRoomUnavailable, got %v", err)
	}
}
