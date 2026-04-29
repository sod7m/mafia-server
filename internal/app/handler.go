package app

import (
	"errors"
	"net/http"

	"mafia-server/internal/auth"
	"mafia-server/internal/domain"
	"mafia-server/internal/games"
	"mafia-server/internal/httpx"
	"mafia-server/internal/realtime"
	"mafia-server/internal/rooms"
)

type Handler struct {
	auth     *auth.Service
	rooms    *rooms.Service
	games    *games.Service
	realtime *realtime.Hub
}

func NewHandler() http.Handler {
	handler := &Handler{
		auth:     auth.NewService(),
		rooms:    rooms.NewService(),
		games:    games.NewService(),
		realtime: realtime.NewHub(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.health)

	mux.HandleFunc("POST /api/auth/login", handler.login)
	mux.HandleFunc("POST /api/auth/logout", handler.logout)
	mux.HandleFunc("GET /api/me", handler.me)

	mux.HandleFunc("GET /api/rooms", handler.listRooms)
	mux.HandleFunc("POST /api/rooms", handler.createRoom)
	mux.HandleFunc("GET /api/rooms/{roomId}", handler.getRoom)
	mux.HandleFunc("POST /api/rooms/{roomId}/join", handler.joinRoom)
	mux.HandleFunc("POST /api/rooms/{roomId}/leave", handler.leaveRoom)
	mux.HandleFunc("POST /api/rooms/{roomId}/start", handler.startRoom)
	mux.HandleFunc("GET /api/games/{roomId}", handler.getGame)
	mux.HandleFunc("POST /api/games/{roomId}/phase", handler.setGamePhase)
	mux.Handle("GET /ws", handler.realtime)

	return httpx.WithCORS(mux)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type loginRequest struct {
	Nickname string `json:"nickname"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		httpx.BadRequest(w, err)
		return
	}

	result, err := h.auth.Login(request.Nickname)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	token := auth.TokenFromRequest(r)
	if token != "" {
		h.auth.Logout(token)
	}

	httpx.WriteNoContent(w)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]domain.UserSession{"user": user})
}

func (h *Handler) listRooms(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string][]domain.Room{
		"rooms": h.rooms.AvailableRooms(),
	})
}

type createRoomRequest struct {
	Name       string `json:"name"`
	MaxPlayers int    `json:"maxPlayers"`
}

func (h *Handler) createRoom(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	var request createRoomRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		httpx.BadRequest(w, err)
		return
	}

	room, err := h.rooms.CreateRoom(user, request.Name, request.MaxPlayers)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	h.broadcastRoomUpdated(room)
	httpx.WriteJSON(w, http.StatusCreated, map[string]domain.Room{"room": room})
}

func (h *Handler) getRoom(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("roomId")
	room, ok := h.rooms.GetRoom(roomID)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, rooms.ErrRoomNotFound.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Room{"room": room})
}

func (h *Handler) joinRoom(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	room, err := h.rooms.JoinRoom(user, r.PathValue("roomId"))
	if err != nil {
		h.writeRoomError(w, err)
		return
	}

	h.broadcastRoomUpdated(room)
	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Room{"room": room})
}

func (h *Handler) leaveRoom(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	roomID := r.PathValue("roomId")
	if err := h.rooms.LeaveRoom(user, roomID); err != nil {
		h.writeRoomError(w, err)
		return
	}

	if room, ok := h.rooms.GetRoom(roomID); ok {
		h.broadcastRoomUpdated(room)
	} else {
		h.broadcastRoomDeleted(roomID)
	}
	httpx.WriteNoContent(w)
}

func (h *Handler) startRoom(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	room, err := h.rooms.StartRoom(user, r.PathValue("roomId"))
	if err != nil {
		h.writeRoomError(w, err)
		return
	}

	game := h.games.StartGame(room)
	h.broadcastRoomUpdated(room)
	h.broadcastGameUpdated(game)
	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Room{"room": room})
}

func (h *Handler) getGame(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	roomID := r.PathValue("roomId")
	room, ok := h.rooms.GetRoom(roomID)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, rooms.ErrRoomNotFound.Error())
		return
	}

	if !isRoomParticipant(room, user.ID) {
		httpx.WriteError(w, http.StatusForbidden, rooms.ErrNotParticipant.Error())
		return
	}

	game, ok := h.games.GetByRoomID(roomID)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, games.ErrGameNotFound.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Game{"game": game})
}

type setGamePhaseRequest struct {
	Phase domain.GamePhase `json:"phase"`
}

func (h *Handler) setGamePhase(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	roomID := r.PathValue("roomId")
	room, ok := h.rooms.GetRoom(roomID)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, rooms.ErrRoomNotFound.Error())
		return
	}

	if !isRoomParticipant(room, user.ID) {
		httpx.WriteError(w, http.StatusForbidden, rooms.ErrNotParticipant.Error())
		return
	}

	if room.OwnerID != user.ID {
		httpx.WriteError(w, http.StatusForbidden, rooms.ErrNotOwner.Error())
		return
	}

	var request setGamePhaseRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		httpx.BadRequest(w, err)
		return
	}

	game, err := h.games.SetPhase(roomID, request.Phase)
	if err != nil {
		h.writeGameError(w, err)
		return
	}

	h.broadcastGameUpdated(game)
	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Game{"game": game})
}

func (h *Handler) requireUser(w http.ResponseWriter, r *http.Request) (domain.UserSession, bool) {
	token := auth.TokenFromRequest(r)
	if token == "" {
		httpx.WriteError(w, http.StatusUnauthorized, auth.ErrUnauthorized.Error())
		return domain.UserSession{}, false
	}

	user, ok := h.auth.UserByToken(token)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, auth.ErrUnauthorized.Error())
		return domain.UserSession{}, false
	}

	return user, true
}

func (h *Handler) broadcastRoomUpdated(room domain.Room) {
	h.realtime.Broadcast(realtime.Event{
		Type: realtime.EventRoomUpdated,
		Room: &room,
	})
	h.broadcastRoomsUpdated()
}

func (h *Handler) broadcastRoomDeleted(roomID string) {
	h.realtime.Broadcast(realtime.Event{
		Type:   realtime.EventRoomDeleted,
		RoomID: roomID,
	})
	h.broadcastRoomsUpdated()
}

func (h *Handler) broadcastRoomsUpdated() {
	h.realtime.Broadcast(realtime.Event{
		Type:  realtime.EventRoomsUpdated,
		Rooms: h.rooms.AvailableRooms(),
	})
}

func (h *Handler) broadcastGameUpdated(game domain.Game) {
	h.realtime.Broadcast(realtime.Event{
		Type: realtime.EventGameUpdated,
		Game: &game,
	})
}

func isRoomParticipant(room domain.Room, userID string) bool {
	for _, player := range room.Players {
		if player.ID == userID {
			return true
		}
	}

	return false
}

func (h *Handler) writeRoomError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rooms.ErrRoomNotFound):
		httpx.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, rooms.ErrRoomUnavailable):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, rooms.ErrRoomFull):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, rooms.ErrNotParticipant):
		httpx.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, rooms.ErrNotOwner):
		httpx.WriteError(w, http.StatusForbidden, err.Error())
	default:
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	}
}

func (h *Handler) writeGameError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, games.ErrGameNotFound):
		httpx.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, games.ErrInvalidPhase):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	}
}
