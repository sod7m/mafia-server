package domain

type RoomStatus string

const (
	RoomStatusWaiting    RoomStatus = "waiting"
	RoomStatusPreparing  RoomStatus = "preparation"
	RoomStatusRecruiting RoomStatus = "recruiting"
	RoomStatusInProgress RoomStatus = "in_progress"
	RoomStatusFinished   RoomStatus = "finished"
)

const (
	MinPlayersInRoom  = 6
	MaxPlayersInRoom  = 16
	MinPlayersToStart = 4
)

type UserSession struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
}

type RoomPlayer struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	IsOwner  bool   `json:"isOwner"`
}

type Room struct {
	ID         string       `json:"id"`
	Code       string       `json:"code"`
	Name       string       `json:"name"`
	Status     RoomStatus   `json:"status"`
	MaxPlayers int          `json:"maxPlayers"`
	OwnerID    string       `json:"ownerId"`
	Players    []RoomPlayer `json:"players"`
	CreatedAt  string       `json:"createdAt"`
}

type GamePhase string

const (
	GamePhaseNight  GamePhase = "night"
	GamePhaseDay    GamePhase = "day"
	GamePhaseVoting GamePhase = "voting"
	GamePhaseFinal  GamePhase = "final"
)

type GameRole string

const (
	GameRoleCivilian     GameRole = "civilian"
	GameRoleMafia        GameRole = "mafia"
	GameRoleCommissioner GameRole = "commissioner"
	GameRoleDoctor       GameRole = "doctor"
)

type GameActionType string

const (
	GameActionMafiaKill GameActionType = "mafia_kill"
	GameActionInspect   GameActionType = "inspect"
	GameActionHeal      GameActionType = "heal"
	GameActionVote      GameActionType = "vote"
)

type GamePlayer struct {
	ID       string   `json:"id"`
	Nickname string   `json:"nickname"`
	IsOwner  bool     `json:"isOwner"`
	Role     GameRole `json:"role,omitempty"`
	IsAlive  bool     `json:"isAlive"`
}

type GameAction struct {
	ID             string         `json:"id"`
	Type           GameActionType `json:"type"`
	ActorID        string         `json:"actorId"`
	ActorNickname  string         `json:"actorNickname"`
	TargetID       string         `json:"targetId"`
	TargetNickname string         `json:"targetNickname"`
	Phase          GamePhase      `json:"phase"`
	Round          int            `json:"round"`
	CreatedAt      string         `json:"createdAt"`
}

type GameEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Phase     GamePhase `json:"phase"`
	Round     int       `json:"round"`
	ActorID   string    `json:"actorId,omitempty"`
	TargetID  string    `json:"targetId,omitempty"`
	CreatedAt string    `json:"createdAt"`
}

type Game struct {
	ID                   string       `json:"id"`
	RoomID               string       `json:"roomId"`
	Phase                GamePhase    `json:"phase"`
	Round                int          `json:"round"`
	PhaseStartedAt       string       `json:"phaseStartedAt"`
	PhaseEndsAt          string       `json:"phaseEndsAt"`
	PhaseDurationSeconds int          `json:"phaseDurationSeconds"`
	Players              []GamePlayer `json:"players"`
	Actions              []GameAction `json:"actions"`
	Events               []GameEvent  `json:"events"`
	StartedAt            string       `json:"startedAt"`
	UpdatedAt            string       `json:"updatedAt"`
}

func IsLobbyStatus(status RoomStatus) bool {
	return status == RoomStatusWaiting ||
		status == RoomStatusPreparing ||
		status == RoomStatusRecruiting
}

func DeriveLobbyStatus(playerCount, maxPlayers int) RoomStatus {
	if playerCount <= 2 {
		return RoomStatusWaiting
	}

	if playerCount >= maxPlayers {
		return RoomStatusPreparing
	}

	return RoomStatusRecruiting
}
