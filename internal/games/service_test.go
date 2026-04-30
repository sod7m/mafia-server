package games

import (
	"testing"
	"time"

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
	if game.PhaseStartedAt == "" {
		t.Fatal("expected phase start timestamp")
	}
	if game.PhaseEndsAt == "" {
		t.Fatal("expected phase end timestamp")
	}
	if game.PhaseDurationSeconds != 45 {
		t.Fatalf("expected night duration 45, got %d", game.PhaseDurationSeconds)
	}
	if len(game.Players) != 2 {
		t.Fatalf("expected 2 players, got %d", len(game.Players))
	}
	if !game.Players[0].IsAlive {
		t.Fatal("expected players to start alive")
	}
	if game.Players[0].Role != domain.GameRoleCommissioner {
		t.Fatalf("expected first player commissioner role, got %q", game.Players[0].Role)
	}
	if game.Players[1].Role != domain.GameRoleMafia {
		t.Fatalf("expected second player mafia role, got %q", game.Players[1].Role)
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

func TestAdvancePhaseFollowsFixedOrder(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Owner", IsOwner: true},
			{ID: "user-2", Nickname: "Mafia", IsOwner: false},
			{ID: "user-3", Nickname: "Doctor", IsOwner: false},
			{ID: "user-4", Nickname: "Civilian", IsOwner: false},
		},
	}
	service.StartGame(room)

	game, err := service.AdvancePhase(room.ID)
	if err != nil {
		t.Fatalf("AdvancePhase day returned error: %v", err)
	}
	if game.Phase != domain.GamePhaseDay {
		t.Fatalf("expected day phase, got %q", game.Phase)
	}
	if game.PhaseDurationSeconds != 90 {
		t.Fatalf("expected day duration 90, got %d", game.PhaseDurationSeconds)
	}

	game, err = service.AdvancePhase(room.ID)
	if err != nil {
		t.Fatalf("AdvancePhase voting returned error: %v", err)
	}
	if game.Phase != domain.GamePhaseVoting {
		t.Fatalf("expected voting phase, got %q", game.Phase)
	}

	game, err = service.AdvancePhase(room.ID)
	if err != nil {
		t.Fatalf("AdvancePhase night returned error: %v", err)
	}
	if game.Phase != domain.GamePhaseNight {
		t.Fatalf("expected night phase, got %q", game.Phase)
	}
	if game.Round != 2 {
		t.Fatalf("expected round 2, got %d", game.Round)
	}
}

func TestAdvanceExpiredAdvancesTimedOutGame(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Owner", IsOwner: true},
			{ID: "user-2", Nickname: "Mafia", IsOwner: false},
			{ID: "user-3", Nickname: "Doctor", IsOwner: false},
			{ID: "user-4", Nickname: "Civilian", IsOwner: false},
		},
	}
	started := service.StartGame(room)
	phaseEndsAt, err := time.Parse(time.RFC3339Nano, started.PhaseEndsAt)
	if err != nil {
		t.Fatalf("parse phase end: %v", err)
	}

	advanced := service.AdvanceExpired(phaseEndsAt.Add(time.Second))
	if len(advanced) != 1 {
		t.Fatalf("expected 1 advanced game, got %d", len(advanced))
	}
	if advanced[0].Phase != domain.GamePhaseDay {
		t.Fatalf("expected day phase, got %q", advanced[0].Phase)
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

func TestSubmitNightActionsAndResolveKill(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Commissioner", IsOwner: true},
			{ID: "user-2", Nickname: "Mafia", IsOwner: false},
			{ID: "user-3", Nickname: "Doctor", IsOwner: false},
			{ID: "user-4", Nickname: "Civilian", IsOwner: false},
		},
	}
	service.StartGame(room)

	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionMafiaKill, "user-4"); err != nil {
		t.Fatalf("mafia kill returned error: %v", err)
	}
	if _, err := service.SubmitAction(room.ID, "user-1", domain.GameActionInspect, "user-2"); err != nil {
		t.Fatalf("inspect returned error: %v", err)
	}
	game, err := service.SetPhase(room.ID, domain.GamePhaseDay)
	if err != nil {
		t.Fatalf("SetPhase day returned error: %v", err)
	}

	target := findPlayer(t, game, "user-4")
	if target.IsAlive {
		t.Fatal("expected target to die after unresolved mafia kill")
	}
	if len(game.Events) == 0 {
		t.Fatal("expected resolution events")
	}
}

func TestInspectCreatesImmediateResultEvent(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Commissioner", IsOwner: true},
			{ID: "user-2", Nickname: "Mafia", IsOwner: false},
		},
	}
	service.StartGame(room)

	game, err := service.SubmitAction(room.ID, "user-1", domain.GameActionInspect, "user-2")
	if err != nil {
		t.Fatalf("inspect returned error: %v", err)
	}

	if len(game.Events) < 2 {
		t.Fatalf("expected recorded action and inspect result events, got %d", len(game.Events))
	}

	result := game.Events[len(game.Events)-1]
	if result.Type != "inspect.resolved" {
		t.Fatalf("expected inspect.resolved event, got %q", result.Type)
	}
	if result.Message != "Комісар перевірив Mafia: сторона Мафія." {
		t.Fatalf("unexpected inspect result message: %q", result.Message)
	}
}

func TestInspectReportsTownSideForDoctor(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Commissioner", IsOwner: true},
			{ID: "user-2", Nickname: "Mafia", IsOwner: false},
			{ID: "user-3", Nickname: "Doctor", IsOwner: false},
		},
	}
	service.StartGame(room)

	game, err := service.SubmitAction(room.ID, "user-1", domain.GameActionInspect, "user-3")
	if err != nil {
		t.Fatalf("inspect returned error: %v", err)
	}

	result := game.Events[len(game.Events)-1]
	if result.Message != "Комісар перевірив Doctor: сторона Мирний." {
		t.Fatalf("unexpected inspect result message: %q", result.Message)
	}
}

func TestViewForPlayerHidesUnseenRoles(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Commissioner", IsOwner: true},
			{ID: "user-2", Nickname: "Mafia", IsOwner: false},
			{ID: "user-3", Nickname: "Doctor", IsOwner: false},
			{ID: "user-4", Nickname: "Civilian", IsOwner: false},
		},
	}

	game := service.StartGame(room)
	view := ViewForPlayer(game, "user-3")

	if findPlayer(t, view, "user-3").Role != domain.GameRoleDoctor {
		t.Fatal("expected viewer to see own role")
	}
	if findPlayer(t, view, "user-1").Role != "" {
		t.Fatal("expected viewer not to see commissioner role")
	}
	if findPlayer(t, view, "user-2").Role != "" {
		t.Fatal("expected viewer not to see mafia role")
	}
}

func TestViewForMafiaShowsMafiaTeamOnly(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Commissioner", IsOwner: true},
			{ID: "user-2", Nickname: "MafiaOne", IsOwner: false},
			{ID: "user-3", Nickname: "Doctor", IsOwner: false},
			{ID: "user-4", Nickname: "Civilian", IsOwner: false},
			{ID: "user-5", Nickname: "CivilianTwo", IsOwner: false},
			{ID: "user-6", Nickname: "MafiaTwo", IsOwner: false},
		},
	}

	game := service.StartGame(room)
	view := ViewForPlayer(game, "user-2")

	if findPlayer(t, view, "user-2").Role != domain.GameRoleMafia {
		t.Fatal("expected mafia viewer to see own role")
	}
	if findPlayer(t, view, "user-6").Role != domain.GameRoleMafia {
		t.Fatal("expected mafia viewer to see mafia teammate")
	}
	if findPlayer(t, view, "user-1").Role != "" {
		t.Fatal("expected mafia viewer not to see commissioner role")
	}
}

func TestViewForCommissionerShowsInspectedRoleAndPrivateEvent(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Commissioner", IsOwner: true},
			{ID: "user-2", Nickname: "Mafia", IsOwner: false},
			{ID: "user-3", Nickname: "Doctor", IsOwner: false},
		},
	}
	service.StartGame(room)
	game, err := service.SubmitAction(room.ID, "user-1", domain.GameActionInspect, "user-2")
	if err != nil {
		t.Fatalf("inspect returned error: %v", err)
	}

	commissionerView := ViewForPlayer(game, "user-1")
	if findPlayer(t, commissionerView, "user-2").Role != domain.GameRoleMafia {
		t.Fatal("expected commissioner to see inspected target role")
	}
	if len(commissionerView.Events) != 2 {
		t.Fatalf("expected commissioner to see inspect action and result, got %d events", len(commissionerView.Events))
	}

	doctorView := ViewForPlayer(game, "user-3")
	if findPlayer(t, doctorView, "user-2").Role != "" {
		t.Fatal("expected doctor not to see inspected mafia role")
	}
	if len(doctorView.Events) != 0 {
		t.Fatalf("expected doctor not to see private inspect events, got %d events", len(doctorView.Events))
	}
}

func TestDoctorCanPreventNightKill(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Commissioner", IsOwner: true},
			{ID: "user-2", Nickname: "Mafia", IsOwner: false},
			{ID: "user-3", Nickname: "Doctor", IsOwner: false},
			{ID: "user-4", Nickname: "Civilian", IsOwner: false},
		},
	}
	service.StartGame(room)

	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionMafiaKill, "user-4"); err != nil {
		t.Fatalf("mafia kill returned error: %v", err)
	}
	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionHeal, "user-4"); err != nil {
		t.Fatalf("heal returned error: %v", err)
	}
	game, err := service.SetPhase(room.ID, domain.GamePhaseDay)
	if err != nil {
		t.Fatalf("SetPhase day returned error: %v", err)
	}

	target := findPlayer(t, game, "user-4")
	if !target.IsAlive {
		t.Fatal("expected target to survive after doctor heal")
	}
}

func TestVotingResolvesExile(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Commissioner", IsOwner: true},
			{ID: "user-2", Nickname: "Mafia", IsOwner: false},
			{ID: "user-3", Nickname: "Doctor", IsOwner: false},
			{ID: "user-4", Nickname: "Civilian", IsOwner: false},
		},
	}
	service.StartGame(room)
	if _, err := service.SetPhase(room.ID, domain.GamePhaseVoting); err != nil {
		t.Fatalf("SetPhase voting returned error: %v", err)
	}

	if _, err := service.SubmitAction(room.ID, "user-1", domain.GameActionVote, "user-2"); err != nil {
		t.Fatalf("first vote returned error: %v", err)
	}
	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionVote, "user-2"); err != nil {
		t.Fatalf("second vote returned error: %v", err)
	}
	game, err := service.SetPhase(room.ID, domain.GamePhaseNight)
	if err != nil {
		t.Fatalf("SetPhase night returned error: %v", err)
	}

	target := findPlayer(t, game, "user-2")
	if target.IsAlive {
		t.Fatal("expected voted target to be exiled")
	}
}

func TestSubmitActionRejectsWrongRole(t *testing.T) {
	service := NewService()
	room := domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: []domain.RoomPlayer{
			{ID: "user-1", Nickname: "Commissioner", IsOwner: true},
			{ID: "user-2", Nickname: "Mafia", IsOwner: false},
		},
	}
	service.StartGame(room)

	if _, err := service.SubmitAction(room.ID, "user-1", domain.GameActionMafiaKill, "user-2"); err != ErrActionUnavailable {
		t.Fatalf("expected ErrActionUnavailable, got %v", err)
	}
}

func findPlayer(t *testing.T, game domain.Game, playerID string) domain.GamePlayer {
	t.Helper()
	for _, player := range game.Players {
		if player.ID == playerID {
			return player
		}
	}

	t.Fatalf("player %q not found", playerID)
	return domain.GamePlayer{}
}
