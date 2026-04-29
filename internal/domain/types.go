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
	MinPlayersInRoom = 6
	MaxPlayersInRoom = 16
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

type GamePlayer struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	IsOwner  bool   `json:"isOwner"`
	IsAlive  bool   `json:"isAlive"`
}

type Game struct {
	ID        string       `json:"id"`
	RoomID    string       `json:"roomId"`
	Phase     GamePhase    `json:"phase"`
	Round     int          `json:"round"`
	Players   []GamePlayer `json:"players"`
	StartedAt string       `json:"startedAt"`
	UpdatedAt string       `json:"updatedAt"`
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
