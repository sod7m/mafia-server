package games

import (
	"testing"
	"time"

	"mafia-server/internal/domain"
)

func TestStartGameCreatesNightMistressStep(t *testing.T) {
	service := NewService()
	room := testRoom(7)

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
	if game.Step != domain.GameStepNightMistress {
		t.Fatalf("expected mistress step, got %q", game.Step)
	}
	if game.Round != 1 {
		t.Fatalf("expected round 1, got %d", game.Round)
	}
	if game.PhaseDurationSeconds != 15 {
		t.Fatalf("expected mistress duration 15, got %d", game.PhaseDurationSeconds)
	}
	if findPlayer(t, game, "user-1").Role != domain.GameRoleCommissioner {
		t.Fatalf("expected first player commissioner role, got %q", findPlayer(t, game, "user-1").Role)
	}
	if findPlayer(t, game, "user-2").Role != domain.GameRoleMafia {
		t.Fatalf("expected second player mafia role, got %q", findPlayer(t, game, "user-2").Role)
	}
	if findPlayer(t, game, "user-3").Role != domain.GameRoleDoctor {
		t.Fatalf("expected third player doctor role, got %q", findPlayer(t, game, "user-3").Role)
	}
	if findPlayer(t, game, "user-4").Role != domain.GameRoleMistress {
		t.Fatalf("expected fourth player mistress role, got %q", findPlayer(t, game, "user-4").Role)
	}
}

func TestStartGameIsIdempotentPerRoom(t *testing.T) {
	service := NewService()
	room := testRoom(7)

	first := service.StartGame(room)
	second := service.StartGame(room)

	if first.ID != second.ID {
		t.Fatalf("expected same game id, got %q and %q", first.ID, second.ID)
	}
}

func TestAdvancePhaseFollowsNightAndDaySteps(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	service.StartGame(room)

	game, err := service.AdvancePhase(room.ID)
	if err != nil {
		t.Fatalf("AdvancePhase doctor returned error: %v", err)
	}
	if game.Step != domain.GameStepNightDoctor {
		t.Fatalf("expected doctor step, got %q", game.Step)
	}

	game, err = service.AdvancePhase(room.ID)
	if err != nil {
		t.Fatalf("AdvancePhase commissioner returned error: %v", err)
	}
	if game.Step != domain.GameStepNightCommissioner {
		t.Fatalf("expected commissioner step, got %q", game.Step)
	}

	game, err = service.AdvancePhase(room.ID)
	if err != nil {
		t.Fatalf("AdvancePhase mafia returned error: %v", err)
	}
	if game.Step != domain.GameStepNightMafia {
		t.Fatalf("expected mafia step, got %q", game.Step)
	}
	if game.PhaseDurationSeconds != 30 {
		t.Fatalf("expected mafia duration 30, got %d", game.PhaseDurationSeconds)
	}

	game, err = service.AdvancePhase(room.ID)
	if err != nil {
		t.Fatalf("AdvancePhase day speech returned error: %v", err)
	}
	if game.Phase != domain.GamePhaseDay || game.Step != domain.GameStepDaySpeech {
		t.Fatalf("expected day speech, got phase %q step %q", game.Phase, game.Step)
	}
	if game.ActivePlayerID != "user-1" {
		t.Fatalf("expected first speaker user-1, got %q", game.ActivePlayerID)
	}
	if game.PhaseDurationSeconds != 60 {
		t.Fatalf("expected speech duration 60, got %d", game.PhaseDurationSeconds)
	}
}

func TestAdvanceExpiredAdvancesTimedOutSubStep(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	started := service.StartGame(room)
	phaseEndsAt, err := time.Parse(time.RFC3339Nano, started.PhaseEndsAt)
	if err != nil {
		t.Fatalf("parse phase end: %v", err)
	}

	advanced := service.AdvanceExpired(phaseEndsAt.Add(time.Second))
	if len(advanced) != 1 {
		t.Fatalf("expected 1 advanced game, got %d", len(advanced))
	}
	if advanced[0].Step != domain.GameStepNightDoctor {
		t.Fatalf("expected doctor step, got %q", advanced[0].Step)
	}
}

func TestSetPhaseRejectsInvalidPhase(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	service.StartGame(room)

	if _, err := service.SetPhase(room.ID, domain.GamePhase("bad")); err != ErrInvalidPhase {
		t.Fatalf("expected ErrInvalidPhase, got %v", err)
	}
}

func TestMistressBlocksDoctorAction(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	service.StartGame(room)

	if _, err := service.SubmitAction(room.ID, "user-4", domain.GameActionBlock, "user-3"); err != nil {
		t.Fatalf("block returned error: %v", err)
	}
	advanceToStep(t, service, room.ID, domain.GameStepNightDoctor)

	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionHeal, "user-5"); err != ErrActionUnavailable {
		t.Fatalf("expected blocked doctor action to be unavailable, got %v", err)
	}
}

func TestMistressCannotBlockSameTargetConsecutiveNights(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	service.StartGame(room)

	if _, err := service.SubmitAction(room.ID, "user-4", domain.GameActionBlock, "user-1"); err != nil {
		t.Fatalf("first block returned error: %v", err)
	}
	_, _ = service.SetPhase(room.ID, domain.GamePhaseDay)
	_, _ = service.SetPhase(room.ID, domain.GamePhaseVoting)
	_, _ = service.SetPhase(room.ID, domain.GamePhaseNight)

	if _, err := service.SubmitAction(room.ID, "user-4", domain.GameActionBlock, "user-1"); err != ErrActionUnavailable {
		t.Fatalf("expected repeated block target to be unavailable, got %v", err)
	}
}

func TestDoctorCannotHealSameTargetConsecutiveNights(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightDoctor)

	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionHeal, "user-5"); err != nil {
		t.Fatalf("first heal returned error: %v", err)
	}
	_, _ = service.SetPhase(room.ID, domain.GamePhaseDay)
	_, _ = service.SetPhase(room.ID, domain.GamePhaseVoting)
	_, _ = service.SetPhase(room.ID, domain.GamePhaseNight)
	advanceToStep(t, service, room.ID, domain.GameStepNightDoctor)

	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionHeal, "user-5"); err != ErrActionUnavailable {
		t.Fatalf("expected repeated heal target to be unavailable, got %v", err)
	}
}

func TestCommissionerSeesSideOnlyAndMistressCountsAsMafia(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightCommissioner)

	game, err := service.SubmitAction(room.ID, "user-1", domain.GameActionInspect, "user-4")
	if err != nil {
		t.Fatalf("inspect returned error: %v", err)
	}

	result := game.Events[len(game.Events)-1]
	if result.Type != "inspect.resolved" {
		t.Fatalf("expected inspect.resolved event, got %q", result.Type)
	}
	if result.Message != "Комісар перевірив Player 4: сторона Мафія." {
		t.Fatalf("unexpected inspect result message: %q", result.Message)
	}

	view := ViewForPlayer(game, "user-1")
	target := findPlayer(t, view, "user-4")
	if target.Role != "" {
		t.Fatalf("expected exact inspected role to stay hidden, got %q", target.Role)
	}
	if target.Side != domain.GameSideMafia {
		t.Fatalf("expected mafia side, got %q", target.Side)
	}
}

func TestCommissionerCannotInspectSameTargetTwice(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightCommissioner)

	if _, err := service.SubmitAction(room.ID, "user-1", domain.GameActionInspect, "user-2"); err != nil {
		t.Fatalf("first inspect returned error: %v", err)
	}
	_, _ = service.SetPhase(room.ID, domain.GamePhaseDay)
	_, _ = service.SetPhase(room.ID, domain.GamePhaseVoting)
	_, _ = service.SetPhase(room.ID, domain.GamePhaseNight)
	advanceToStep(t, service, room.ID, domain.GameStepNightCommissioner)

	if _, err := service.SubmitAction(room.ID, "user-1", domain.GameActionInspect, "user-2"); err != ErrActionUnavailable {
		t.Fatalf("expected repeated inspect target to be unavailable, got %v", err)
	}
}

func TestMafiaNeedsAllUnblockedMafiaToShootSameTarget(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightMafia)

	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionMafiaKill, "user-5"); err != nil {
		t.Fatalf("first mafia kill returned error: %v", err)
	}
	game, err := service.SetPhase(room.ID, domain.GamePhaseDay)
	if err != nil {
		t.Fatalf("SetPhase day returned error: %v", err)
	}
	if !findPlayer(t, game, "user-5").IsAlive {
		t.Fatal("expected target to survive when one mafia did not shoot")
	}
}

func TestMafiaSameTargetKillsUnlessDoctorHeals(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightDoctor)
	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionHeal, "user-5"); err != nil {
		t.Fatalf("heal returned error: %v", err)
	}
	advanceToStep(t, service, room.ID, domain.GameStepNightMafia)
	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionMafiaKill, "user-5"); err != nil {
		t.Fatalf("first mafia kill returned error: %v", err)
	}
	if _, err := service.SubmitAction(room.ID, "user-7", domain.GameActionMafiaKill, "user-5"); err != nil {
		t.Fatalf("second mafia kill returned error: %v", err)
	}

	game, err := service.SetPhase(room.ID, domain.GamePhaseDay)
	if err != nil {
		t.Fatalf("SetPhase day returned error: %v", err)
	}
	if !findPlayer(t, game, "user-5").IsAlive {
		t.Fatal("expected target to survive after doctor heal")
	}
}

func TestMafiaDifferentTargetsMiss(t *testing.T) {
	service := NewService()
	room := testRoom(7)
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightMafia)

	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionMafiaKill, "user-5"); err != nil {
		t.Fatalf("first mafia kill returned error: %v", err)
	}
	if _, err := service.SubmitAction(room.ID, "user-7", domain.GameActionMafiaKill, "user-6"); err != nil {
		t.Fatalf("second mafia kill returned error: %v", err)
	}
	game, err := service.SetPhase(room.ID, domain.GamePhaseDay)
	if err != nil {
		t.Fatalf("SetPhase day returned error: %v", err)
	}
	if !findPlayer(t, game, "user-5").IsAlive || !findPlayer(t, game, "user-6").IsAlive {
		t.Fatal("expected both targets to survive when mafia splits shots")
	}
}

func TestVotingResolvesExileAndTieSkipsExile(t *testing.T) {
	service := NewService()
	room := testRoom(7)
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
	if findPlayer(t, game, "user-2").IsAlive {
		t.Fatal("expected voted target to be exiled")
	}
}

func TestViewForMafiaDoesNotRevealMistress(t *testing.T) {
	service := NewService()
	room := testRoom(7)

	game := service.StartGame(room)
	view := ViewForPlayer(game, "user-2")

	if findPlayer(t, view, "user-2").Role != domain.GameRoleMafia {
		t.Fatal("expected mafia viewer to see own role")
	}
	if findPlayer(t, view, "user-7").Role != domain.GameRoleMafia {
		t.Fatal("expected mafia viewer to see ordinary mafia teammate")
	}
	if findPlayer(t, view, "user-4").Role != "" {
		t.Fatal("expected mafia viewer not to see mistress role")
	}
}

func advanceToStep(t *testing.T, service *Service, roomID string, step domain.GameStep) domain.Game {
	t.Helper()
	game, ok := service.GetByRoomID(roomID)
	if !ok {
		t.Fatalf("game for room %q not found", roomID)
	}
	for game.Step != step {
		var err error
		game, err = service.AdvancePhase(roomID)
		if err != nil {
			t.Fatalf("AdvancePhase returned error: %v", err)
		}
		if game.Step == domain.GameStepFinal {
			t.Fatalf("reached final before step %q", step)
		}
	}
	return game
}

func testRoom(playerCount int) domain.Room {
	players := make([]domain.RoomPlayer, 0, playerCount)
	for index := 1; index <= playerCount; index++ {
		players = append(players, domain.RoomPlayer{
			ID:       "user-" + string(rune('0'+index)),
			Nickname: "Player " + string(rune('0'+index)),
			IsOwner:  index == 1,
		})
	}
	return domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: players,
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
