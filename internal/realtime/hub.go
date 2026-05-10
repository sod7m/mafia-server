package realtime

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"mafia-server/internal/domain"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	EventRoomsUpdated = "rooms.updated"
	EventRoomUpdated  = "room.updated"
	EventRoomDeleted  = "room.deleted"
	EventGameUpdated  = "game.updated"
)

type Event struct {
	Type   string        `json:"type"`
	Rooms  []domain.Room `json:"rooms,omitempty"`
	Room   *domain.Room  `json:"room,omitempty"`
	RoomID string        `json:"roomId,omitempty"`
	Game   *domain.Game  `json:"game,omitempty"`
}

type Hub struct {
	mu            sync.RWMutex
	clients       map[*client]struct{}
	acceptOptions websocket.AcceptOptions
}

type client struct {
	events chan Event
}

func NewHub() *Hub {
	return NewHubWithOrigins([]string{"localhost:5173", "127.0.0.1:5173"})
}

func NewHubWithOrigins(originPatterns []string) *Hub {
	patterns := normalizeOriginPatterns(originPatterns)
	return &Hub{
		clients: make(map[*client]struct{}),
		acceptOptions: websocket.AcceptOptions{
			OriginPatterns: patterns,
		},
	}
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	options := h.acceptOptions
	conn, err := websocket.Accept(w, r, &options)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	currentClient := &client{
		events: make(chan Event, 16),
	}
	h.register(currentClient)
	defer h.unregister(currentClient)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-currentClient.events:
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := wsjson.Write(writeCtx, conn, event)
			cancel()
			if err != nil {
				log.Printf("websocket write failed: %v", err)
				return
			}
		}
	}
}

func (h *Hub) Broadcast(event Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for currentClient := range h.clients {
		select {
		case currentClient.events <- event:
		default:
		}
	}
}

func (h *Hub) register(currentClient *client) {
	h.mu.Lock()
	h.clients[currentClient] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) unregister(currentClient *client) {
	h.mu.Lock()
	delete(h.clients, currentClient)
	close(currentClient.events)
	h.mu.Unlock()
}

func normalizeOriginPatterns(originPatterns []string) []string {
	if len(originPatterns) == 0 {
		return []string{"localhost:5173", "127.0.0.1:5173"}
	}

	patterns := make([]string, 0, len(originPatterns))
	for _, pattern := range originPatterns {
		clean := strings.TrimSpace(pattern)
		if clean == "" {
			continue
		}
		patterns = append(patterns, clean)
	}
	if len(patterns) == 0 {
		return []string{"localhost:5173", "127.0.0.1:5173"}
	}

	return patterns
}
