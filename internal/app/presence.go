package app

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	// gameIdleTTL is how long a game may go without any participant fetching it
	// before it is considered abandoned and torn down.
	gameIdleTTL = 5 * time.Minute
	// reaperInterval is how often the background reaper checks for abandoned games.
	reaperInterval = 30 * time.Second
)

// presenceTracker records the last time each room's game was actively fetched by
// a participant. A successful GET /api/games/{roomId} (or a game action) is only
// possible for a player who is actually in that game, so a fresh timestamp means
// "someone is still here". When the timestamp goes stale, nobody is around.
type presenceTracker struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
}

func newPresenceTracker() *presenceTracker {
	return &presenceTracker{lastSeen: make(map[string]time.Time)}
}

// touch marks a room as active right now.
func (p *presenceTracker) touch(roomID string) {
	if roomID == "" {
		return
	}
	p.mu.Lock()
	p.lastSeen[roomID] = time.Now().UTC()
	p.mu.Unlock()
}

func (p *presenceTracker) seenAt(roomID string) (time.Time, bool) {
	p.mu.Lock()
	seen, ok := p.lastSeen[roomID]
	p.mu.Unlock()
	return seen, ok
}

func (p *presenceTracker) forget(roomID string) {
	p.mu.Lock()
	delete(p.lastSeen, roomID)
	p.mu.Unlock()
}

// startGameReaper launches a goroutine that periodically removes games (and
// their rooms) that no participant has touched for gameIdleTTL. This frees the
// state left behind by players who closed their tab without leaving — the
// websocket disconnect alone never removed them.
func (h *Handler) startGameReaper(ctx context.Context) {
	ticker := time.NewTicker(reaperInterval)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case now := <-ticker.C:
				h.reapAbandonedGames(now)
			}
		}
	}()
}

func (h *Handler) reapAbandonedGames(now time.Time) {
	for _, roomID := range h.games.RoomIDs() {
		seen, ok := h.presence.seenAt(roomID)
		if !ok {
			// First time we see this game without a record (e.g. restored from
			// storage): start its clock now rather than reaping it blindly.
			h.presence.touch(roomID)
			continue
		}
		if now.Sub(seen) < gameIdleTTL {
			continue
		}

		h.games.Delete(roomID)
		h.rooms.DeleteRoom(roomID)
		h.presence.forget(roomID)
		h.broadcastRoomDeleted(roomID)
		log.Printf("reaper: removed abandoned game/room %s (idle for %s)", roomID, now.Sub(seen).Round(time.Second))
	}
}
