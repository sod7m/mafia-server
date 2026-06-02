package app

import (
	"fmt"
	"testing"
	"time"

	"mafia-server/internal/domain"
	"mafia-server/internal/games"
	"mafia-server/internal/realtime"
	"mafia-server/internal/rooms"
)

func newReaperHandler() *Handler {
	return &Handler{
		rooms:    rooms.NewService(),
		games:    games.NewService(),
		realtime: realtime.NewHub(),
		presence: newPresenceTracker(),
	}
}

func craftStartedGame(h *Handler, roomID string) {
	room := domain.Room{ID: roomID, Players: make([]domain.RoomPlayer, domain.MinPlayersToStart)}
	for i := range room.Players {
		room.Players[i] = domain.RoomPlayer{ID: fmt.Sprintf("usr_%d", i), Nickname: fmt.Sprintf("P%d", i)}
	}
	h.games.StartGame(room)
}

func TestReaperRemovesIdleGame(t *testing.T) {
	h := newReaperHandler()
	craftStartedGame(h, "room_idle")

	// Backdate presence beyond the idle TTL.
	h.presence.lastSeen["room_idle"] = time.Now().UTC().Add(-gameIdleTTL - time.Minute)

	h.reapAbandonedGames(time.Now().UTC())

	if _, ok := h.games.GetByRoomID("room_idle"); ok {
		t.Fatal("expected idle game to be reaped, but it is still present")
	}
	if _, ok := h.presence.seenAt("room_idle"); ok {
		t.Fatal("expected presence record to be forgotten after reaping")
	}
}

func TestReaperKeepsActiveGame(t *testing.T) {
	h := newReaperHandler()
	craftStartedGame(h, "room_active")
	h.presence.touch("room_active") // seen just now

	h.reapAbandonedGames(time.Now().UTC())

	if _, ok := h.games.GetByRoomID("room_active"); !ok {
		t.Fatal("active game was reaped but should have been kept")
	}
}

func TestReaperStartsClockForUntrackedGame(t *testing.T) {
	h := newReaperHandler()
	craftStartedGame(h, "room_restored")
	// No presence record at all (e.g. restored from storage).

	h.reapAbandonedGames(time.Now().UTC())

	if _, ok := h.games.GetByRoomID("room_restored"); !ok {
		t.Fatal("untracked game should not be reaped on the first pass")
	}
	if _, ok := h.presence.seenAt("room_restored"); !ok {
		t.Fatal("reaper should have started the idle clock for the untracked game")
	}
}
