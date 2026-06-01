package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"mafia-server/docs"
	"mafia-server/internal/auth"
	"mafia-server/internal/domain"
	"mafia-server/internal/games"
	"mafia-server/internal/httpx"
	"mafia-server/internal/persistence"
	"mafia-server/internal/realtime"
	"mafia-server/internal/rooms"
)

type Handler struct {
	auth                *auth.Service
	rooms               *rooms.Service
	games               *games.Service
	realtime            *realtime.Hub
	loginLimiter        *httpx.FixedWindowLimiter
	roomMutationLimiter *httpx.FixedWindowLimiter
}

type SecurityConfig struct {
	CORSAllowedOrigins         []string
	WSAllowedOrigins           []string
	LoginRateLimitPerMinute    int
	RoomMutationsRatePerMinute int
}

func DefaultSecurityConfig() SecurityConfig {
	return SecurityConfig{
		CORSAllowedOrigins:         []string{"http://localhost:5173", "http://127.0.0.1:5173"},
		WSAllowedOrigins:           []string{"localhost:5173", "127.0.0.1:5173"},
		LoginRateLimitPerMinute:    30,
		RoomMutationsRatePerMinute: 60,
	}
}

func NewHandler() http.Handler {
	return NewHandlerWithConfig(context.Background(), persistence.NewNoopStore(), DefaultSecurityConfig())
}

func NewHandlerWithStore(store persistence.Store) http.Handler {
	return NewHandlerWithConfig(context.Background(), store, DefaultSecurityConfig())
}

func NewHandlerWithConfig(ctx context.Context, store persistence.Store, securityConfig SecurityConfig) http.Handler {
	if securityConfig.LoginRateLimitPerMinute <= 0 {
		securityConfig.LoginRateLimitPerMinute = DefaultSecurityConfig().LoginRateLimitPerMinute
	}
	if securityConfig.RoomMutationsRatePerMinute <= 0 {
		securityConfig.RoomMutationsRatePerMinute = DefaultSecurityConfig().RoomMutationsRatePerMinute
	}

	handler := &Handler{
		auth:                auth.NewServiceWithStore(store),
		rooms:               rooms.NewServiceWithStore(store),
		games:               games.NewServiceWithStore(store),
		realtime:            realtime.NewHubWithOrigins(securityConfig.WSAllowedOrigins),
		loginLimiter:        httpx.NewFixedWindowLimiter(securityConfig.LoginRateLimitPerMinute, time.Minute),
		roomMutationLimiter: httpx.NewFixedWindowLimiter(securityConfig.RoomMutationsRatePerMinute, time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.health)

	// Swagger docs - serve UI and JSON
	mux.HandleFunc("GET /api/docs", handler.swaggerUI)
	mux.HandleFunc("GET /api/docs/swagger.json", handler.swaggerJSON)
	mux.HandleFunc("POST /api/auth/login", handler.withLoginRateLimit(handler.login))
	mux.HandleFunc("POST /api/auth/logout", handler.logout)
	mux.HandleFunc("GET /api/me", handler.me)
	mux.HandleFunc("GET /api/recovery", handler.recovery)

	mux.HandleFunc("GET /api/rooms", handler.listRooms)
	mux.HandleFunc("POST /api/rooms", handler.withRoomMutationRateLimit(handler.createRoom))
	mux.HandleFunc("GET /api/rooms/{roomId}", handler.getRoom)
	mux.HandleFunc("POST /api/rooms/{roomId}/join", handler.withRoomMutationRateLimit(handler.joinRoom))
	mux.HandleFunc("POST /api/rooms/{roomId}/leave", handler.withRoomMutationRateLimit(handler.leaveRoom))
	mux.HandleFunc("POST /api/rooms/{roomId}/start", handler.withRoomMutationRateLimit(handler.startRoom))
	mux.HandleFunc("GET /api/games/{roomId}", handler.getGame)
	mux.HandleFunc("POST /api/games/{roomId}/phase", handler.setGamePhase)
	mux.HandleFunc("POST /api/games/{roomId}/next-phase", handler.advanceGamePhase)
	mux.HandleFunc("POST /api/games/{roomId}/actions", handler.submitGameAction)
	mux.Handle("GET /ws", handler.realtime)

	handler.startPhaseTicker(ctx)
	return httpx.WithCORSOrigins(mux, securityConfig.CORSAllowedOrigins)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type loginRequest struct {
	Nickname string `json:"nickname" example:"Alice"`
}

type loginResponse struct {
	User  domain.UserSession `json:"user"`
	Token string             `json:"token"`
}

// Login godoc
// @Summary Create or get user by nickname
// @Description Authenticate user with nickname (creates if not exists)
// @Accept json
// @Produce json
// @Param body body loginRequest true "Nickname"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string "Invalid nickname"
// @Router /api/auth/login [post]
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

// Logout godoc
// @Summary Logout current user
// @Description Revoke authentication token
// @Security Bearer
// @Produce json
// @Success 204
// @Failure 401 {object} map[string]string "Unauthorized"
// @Router /api/auth/logout [post]
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	token := auth.TokenFromRequest(r)
	if token != "" {
		h.auth.Logout(token)
	}

	httpx.WriteNoContent(w)
}

// Me godoc
// @Summary Get current user info
// @Description Retrieve authenticated user profile
// @Security Bearer
// @Produce json
// @Success 200 {object} map[string]domain.UserSession
// @Failure 401 {object} map[string]string "Unauthorized"
// @Router /api/me [get]
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]domain.UserSession{"user": user})
}

type recoveryResponse struct {
	User         domain.UserSession `json:"user"`
	Rooms        []domain.Room      `json:"rooms"`
	Games        []domain.Game      `json:"games"`
	ActiveRoomID string             `json:"activeRoomId,omitempty"`
}

// Recovery godoc
// @Summary Recover user state after reconnect
// @Description Returns current user, participant rooms, and personalized game views
// @Security Bearer
// @Produce json
// @Success 200 {object} recoveryResponse
// @Failure 401 {object} map[string]string "Unauthorized"
// @Router /api/recovery [get]
func (h *Handler) recovery(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}

	roomsByUser := h.rooms.RoomsByParticipant(user.ID)
	gamesByUser := make([]domain.Game, 0, len(roomsByUser))
	activeRoomID := ""
	for _, room := range roomsByUser {
		game, exists := h.games.GetByRoomID(room.ID)
		if !exists {
			continue
		}

		if activeRoomID == "" && room.Status == domain.RoomStatusInProgress {
			activeRoomID = room.ID
		}
		gamesByUser = append(gamesByUser, games.ViewForPlayer(game, user.ID))
	}

	httpx.WriteJSON(w, http.StatusOK, recoveryResponse{
		User:         user,
		Rooms:        roomsByUser,
		Games:        gamesByUser,
		ActiveRoomID: activeRoomID,
	})
}

// ListRooms godoc
// @Summary List all available rooms
// @Description Get all active game rooms
// @Produce json
// @Success 200 {object} map[string][]domain.Room
// @Router /api/rooms [get]
func (h *Handler) listRooms(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string][]domain.Room{
		"rooms": h.rooms.AvailableRooms(),
	})
}

type createRoomRequest struct {
	Name       string `json:"name" example:"Game Night"`
	MaxPlayers int    `json:"maxPlayers" example:"7"`
}

// CreateRoom godoc
// @Summary Create new game room
// @Description Create a new Mafia game room
// @Security Bearer
// @Accept json
// @Produce json
// @Param body body createRoomRequest true "Room details"
// @Success 201 {object} map[string]domain.Room
// @Failure 400 {object} map[string]string "Invalid input"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Router /api/rooms [post]
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

// GetRoom godoc
// @Summary Get room details
// @Description Retrieve specific room info
// @Produce json
// @Param roomId path string true "Room ID"
// @Success 200 {object} map[string]domain.Room
// @Failure 404 {object} map[string]string "Room not found"
// @Router /api/rooms/{roomId} [get]
func (h *Handler) getRoom(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("roomId")
	room, ok := h.rooms.GetRoom(roomID)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, rooms.ErrRoomNotFound.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Room{"room": room})
}

// JoinRoom godoc
// @Summary Join a game room
// @Description Add current user to a room
// @Security Bearer
// @Produce json
// @Param roomId path string true "Room ID"
// @Success 200 {object} map[string]domain.Room
// @Failure 400 {object} map[string]string "Cannot join room"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 404 {object} map[string]string "Room not found"
// @Router /api/rooms/{roomId}/join [post]
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

// LeaveRoom godoc
// @Summary Leave a game room
// @Description Remove current user from a room
// @Security Bearer
// @Produce json
// @Param roomId path string true "Room ID"
// @Success 204
// @Failure 400 {object} map[string]string "Cannot leave room"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Router /api/rooms/{roomId}/leave [post]
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

// StartRoom godoc
// @Summary Start a game in the room
// @Description Begin Mafia game with current room players
// @Security Bearer
// @Produce json
// @Param roomId path string true "Room ID"
// @Success 200 {object} map[string]domain.Room
// @Failure 400 {object} map[string]string "Cannot start game"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Router /api/rooms/{roomId}/start [post]
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
	h.broadcastGameUpdated(game.RoomID)
	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Room{"room": room})
}

// GetGame godoc
// @Summary Get game state
// @Description Retrieve current game state (personalized for player)
// @Security Bearer
// @Produce json
// @Param roomId path string true "Room ID"
// @Success 200 {object} map[string]domain.Game
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 403 {object} map[string]string "Not a room participant"
// @Failure 404 {object} map[string]string "Game not found"
// @Router /api/games/{roomId} [get]
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

	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Game{"game": games.ViewForPlayer(game, user.ID)})
}

type setGamePhaseRequest struct {
	Phase domain.GamePhase `json:"phase" example:"day"`
}

// SetGamePhase godoc
// @Summary Set game phase manually
// @Description Change game phase (day/night/voting)
// @Security Bearer
// @Accept json
// @Produce json
// @Param roomId path string true "Room ID"
// @Param body body setGamePhaseRequest true "Phase to set"
// @Success 200 {object} map[string]domain.Game
// @Failure 400 {object} map[string]string "Invalid phase"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 403 {object} map[string]string "Only owner can set phase"
// @Router /api/games/{roomId}/phase [post]
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

	h.broadcastGameUpdated(game.RoomID)
	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Game{"game": games.ViewForPlayer(game, user.ID)})
}

// AdvanceGamePhase godoc
// @Summary Advance to next game phase
// @Description Move game to next phase (resolves current phase first)
// @Security Bearer
// @Produce json
// @Param roomId path string true "Room ID"
// @Success 200 {object} map[string]domain.Game
// @Failure 400 {object} map[string]string "Cannot advance phase"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 403 {object} map[string]string "Only owner can advance"
// @Router /api/games/{roomId}/next-phase [post]
func (h *Handler) advanceGamePhase(w http.ResponseWriter, r *http.Request) {
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

	game, err := h.games.AdvancePhase(roomID)
	if err != nil {
		h.writeGameError(w, err)
		return
	}

	h.broadcastGameUpdated(game.RoomID)
	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Game{"game": games.ViewForPlayer(game, user.ID)})
}

type submitGameActionRequest struct {
	Type     domain.GameActionType `json:"type" example:"block"`
	TargetID string                `json:"targetId" example:"user-123"`
}

// SubmitGameAction godoc
// @Summary Submit a night action or vote
// @Description Player submits action: block, heal, inspect, mafia_kill, or vote
// @Security Bearer
// @Accept json
// @Produce json
// @Param roomId path string true "Room ID"
// @Param body body submitGameActionRequest true "Action details"
// @Success 200 {object} map[string]domain.Game
// @Failure 400 {object} map[string]string "Invalid action"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 403 {object} map[string]string "Action not allowed for your role"
// @Failure 409 {object} map[string]string "Action blocked or unavailable"
// @Router /api/games/{roomId}/actions [post]
func (h *Handler) submitGameAction(w http.ResponseWriter, r *http.Request) {
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

	var request submitGameActionRequest
	if err := httpx.DecodeJSON(r, &request); err != nil {
		httpx.BadRequest(w, err)
		return
	}

	game, err := h.games.SubmitAction(roomID, user.ID, request.Type, request.TargetID)
	if err != nil {
		h.writeGameError(w, err)
		return
	}

	// Votes are public — broadcast immediately.
	// Night actions are silent: broadcasting on submission would let observers
	// correlate action timing and deduce who is alive by when steps skip.
	// All players receive the updated state when the phase timer expires.
	if request.Type == domain.GameActionVote {
		h.broadcastGameUpdated(game.RoomID)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]domain.Game{"game": games.ViewForPlayer(game, user.ID)})
}

func (h *Handler) swaggerUI(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html>
<head>
  <title>Mafia API</title>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.21.0/swagger-ui.css">
  <style>
    html {
      box-sizing: border-box;
      overflow: -moz-scrollbars-vertical;
      overflow-y: scroll;
    }
    *,
    *:before,
    *:after {
      box-sizing: inherit;
    }
    body {
      margin: 0;
      padding: 0;
    }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.21.0/swagger-ui-bundle.js"></script>
  <script>
  window.onload = function() {
    SwaggerUIBundle({
      url: "/api/docs/swagger.json",
      dom_id: '#swagger-ui',
      presets: [
        SwaggerUIBundle.presets.apis
      ],
      layout: "BaseLayout"
    })
  }
  </script>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func (h *Handler) swaggerJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(docs.SwaggerJSON) //nolint:errcheck
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

func (h *Handler) broadcastGameUpdated(roomID string) {
	h.realtime.Broadcast(realtime.Event{
		Type:   realtime.EventGameUpdated,
		RoomID: roomID,
	})
}

func (h *Handler) startPhaseTicker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case now := <-ticker.C:
				for _, game := range h.games.AdvanceExpired(now) {
					h.broadcastGameUpdated(game.RoomID)
				}
			}
		}
	}()
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
	case errors.Is(err, rooms.ErrNotEnoughPlayers):
		httpx.WriteError(w, http.StatusConflict, err.Error())
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
	case errors.Is(err, games.ErrInvalidAction):
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, games.ErrActionUnavailable):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, games.ErrActionBlocked):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, games.ErrRepeatHeal):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, games.ErrRepeatBlock):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, games.ErrRepeatInspect):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, games.ErrPlayerNotFound):
		httpx.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, games.ErrPlayerDead):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case errors.Is(err, games.ErrTargetDead):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	default:
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
	}
}

func (h *Handler) withLoginRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return h.withRateLimit("login", h.loginLimiter, next)
}

func (h *Handler) withRoomMutationRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return h.withRateLimit("room-mutation", h.roomMutationLimiter, next)
}

func (h *Handler) withRateLimit(scope string, limiter *httpx.FixedWindowLimiter, next http.HandlerFunc) http.HandlerFunc {
	if limiter == nil {
		return next
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow(h.rateLimitKey(scope, r), time.Now().UTC()) {
			httpx.WriteError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next(w, r)
	}
}

func (h *Handler) rateLimitKey(scope string, r *http.Request) string {
	token := auth.TokenFromRequest(r)
	if token == "" {
		token = "anonymous"
	}
	return scope + "|" + httpx.RateLimitKeyByIP(r) + "|" + token
}
