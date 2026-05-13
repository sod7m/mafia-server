package games

import (
	"fmt"
	"testing"
	"time"

	"mafia-server/internal/domain"
)

// identityPerm returns [0, 1, 2, ...n-1] — deterministic for tests.
func identityPerm(n int) []int {
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	return perm
}

// newTestService creates a Service with deterministic (identity) role permutation.
func newTestService() *Service {
	s := NewService()
	s.rolePerm = identityPerm
	return s
}

// testRoom builds a room with playerCount players.
// With the identity permutation the role at index i is rolesForCount(n)[i]:
//
//	6–7  players: [Commissioner, Doctor, Mafia, Civilian...]
//	8–10 players: [Commissioner, Doctor, Mafia, Mafia, Civilian...]
//	11–13 players: [Commissioner, Doctor, Mafia, Mafia, Mistress, Civilian...]
//	14–16 players: [Commissioner, Doctor, Mafia, Mafia, Mafia, Mistress, Civilian...]
func testRoom(playerCount int) domain.Room {
	players := make([]domain.RoomPlayer, 0, playerCount)
	for index := 1; index <= playerCount; index++ {
		players = append(players, domain.RoomPlayer{
			ID:       fmt.Sprintf("user-%d", index),
			Nickname: fmt.Sprintf("Player %d", index),
			IsOwner:  index == 1,
		})
	}
	return domain.Room{
		ID:      "room-1",
		OwnerID: "user-1",
		Players: players,
	}
}

func findPlayerByRole(t *testing.T, game domain.Game, role domain.GameRole) domain.GamePlayer {
	t.Helper()
	for _, p := range game.Players {
		if p.Role == role {
			return p
		}
	}
	t.Fatalf("no player with role %q found", role)
	return domain.GamePlayer{}
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

// --- Role distribution ---

func TestRoleDistribution(t *testing.T) {
	cases := []struct {
		players      int
		wantMafia    int
		wantMistress int
	}{
		{6, 1, 0},
		{7, 1, 0},
		{8, 2, 0},
		{10, 2, 0},
		{11, 2, 1},
		{13, 2, 1},
		{14, 3, 1},
		{16, 3, 1},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d players", tc.players), func(t *testing.T) {
			service := newTestService()
			game := service.StartGame(testRoom(tc.players))

			counts := make(map[domain.GameRole]int)
			for _, p := range game.Players {
				counts[p.Role]++
			}

			if counts[domain.GameRoleMafia] != tc.wantMafia {
				t.Errorf("mafia: got %d, want %d", counts[domain.GameRoleMafia], tc.wantMafia)
			}
			if counts[domain.GameRoleMistress] != tc.wantMistress {
				t.Errorf("mistress: got %d, want %d", counts[domain.GameRoleMistress], tc.wantMistress)
			}
			if counts[domain.GameRoleDoctor] != 1 {
				t.Errorf("doctor: got %d, want 1", counts[domain.GameRoleDoctor])
			}
			if counts[domain.GameRoleCommissioner] != 1 {
				t.Errorf("commissioner: got %d, want 1", counts[domain.GameRoleCommissioner])
			}
			total := 0
			for _, c := range counts {
				total += c
			}
			if total != tc.players {
				t.Errorf("total roles: got %d, want %d", total, tc.players)
			}
		})
	}
}

// --- Game start / phase progression ---

// 7-player game has no Mistress → first night step is night_doctor.
func TestStartGameCreatesFirstNightStep(t *testing.T) {
	service := newTestService()
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
	if game.Step != domain.GameStepNightDoctor {
		t.Fatalf("expected night_doctor step (no Mistress in 7-player game), got %q", game.Step)
	}
	if game.Round != 1 {
		t.Fatalf("expected round 1, got %d", game.Round)
	}
	if game.PhaseDurationSeconds != 15 {
		t.Fatalf("expected doctor duration 15, got %d", game.PhaseDurationSeconds)
	}
	// identity perm: index 0=Commissioner, 1=Doctor, 2=Mafia
	if findPlayer(t, game, "user-1").Role != domain.GameRoleCommissioner {
		t.Fatalf("expected user-1 commissioner, got %q", findPlayer(t, game, "user-1").Role)
	}
	if findPlayer(t, game, "user-2").Role != domain.GameRoleDoctor {
		t.Fatalf("expected user-2 doctor, got %q", findPlayer(t, game, "user-2").Role)
	}
	if findPlayer(t, game, "user-3").Role != domain.GameRoleMafia {
		t.Fatalf("expected user-3 mafia, got %q", findPlayer(t, game, "user-3").Role)
	}
}

// 11-player game has Mistress → first step is night_mistress.
func TestStartGameWithMistressStartsAtMistressStep(t *testing.T) {
	service := newTestService()
	game := service.StartGame(testRoom(11))

	if game.Step != domain.GameStepNightMistress {
		t.Fatalf("expected night_mistress step for 11-player game, got %q", game.Step)
	}
}

func TestStartGameIsIdempotentPerRoom(t *testing.T) {
	service := newTestService()
	room := testRoom(7)

	first := service.StartGame(room)
	second := service.StartGame(room)

	if first.ID != second.ID {
		t.Fatalf("expected same game id, got %q and %q", first.ID, second.ID)
	}
}

// 7-player game (no Mistress): doctor → commissioner → mafia → day_speech
func TestAdvancePhaseFollowsNightAndDaySteps(t *testing.T) {
	service := newTestService()
	room := testRoom(7)
	service.StartGame(room)

	game, err := service.AdvancePhase(room.ID)
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
	service := newTestService()
	room := testRoom(7)
	started := service.StartGame(room) // starts at night_doctor
	phaseEndsAt, err := time.Parse(time.RFC3339Nano, started.PhaseEndsAt)
	if err != nil {
		t.Fatalf("parse phase end: %v", err)
	}

	advanced := service.AdvanceExpired(phaseEndsAt.Add(time.Second))
	if len(advanced) != 1 {
		t.Fatalf("expected 1 advanced game, got %d", len(advanced))
	}
	if advanced[0].Step != domain.GameStepNightCommissioner {
		t.Fatalf("expected commissioner step after doctor timer, got %q", advanced[0].Step)
	}
}

func TestSetPhaseRejectsInvalidPhase(t *testing.T) {
	service := newTestService()
	room := testRoom(7)
	service.StartGame(room)

	if _, err := service.SetPhase(room.ID, domain.GamePhase("bad")); err != ErrInvalidPhase {
		t.Fatalf("expected ErrInvalidPhase, got %v", err)
	}
}

// --- Mistress (requires 11-player game) ---

func TestMistressBlocksDoctorAction(t *testing.T) {
	service := newTestService()
	room := testRoom(11) // user-5 = Mistress, user-2 = Doctor
	service.StartGame(room)

	// Game starts at night_mistress; user-5 blocks user-2 (Doctor)
	if _, err := service.SubmitAction(room.ID, "user-5", domain.GameActionBlock, "user-2"); err != nil {
		t.Fatalf("block returned error: %v", err)
	}
	advanceToStep(t, service, room.ID, domain.GameStepNightDoctor)

	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionHeal, "user-6"); err != ErrActionUnavailable {
		t.Fatalf("expected blocked doctor action to be unavailable, got %v", err)
	}
}

func TestBlockedPlayerCanStillVote(t *testing.T) {
	service := newTestService()
	room := testRoom(11) // user-5 = Mistress, user-2 = Doctor
	service.StartGame(room)

	if _, err := service.SubmitAction(room.ID, "user-5", domain.GameActionBlock, "user-2"); err != nil {
		t.Fatalf("block returned error: %v", err)
	}
	if _, err := service.SetPhase(room.ID, domain.GamePhaseVoting); err != nil {
		t.Fatalf("SetPhase voting returned error: %v", err)
	}

	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionVote, "user-3"); err != nil {
		t.Fatalf("expected blocked player to still vote, got %v", err)
	}
}

func TestMistressCannotBlockSameTargetConsecutiveNights(t *testing.T) {
	service := newTestService()
	room := testRoom(11) // user-5 = Mistress
	service.StartGame(room)

	if _, err := service.SubmitAction(room.ID, "user-5", domain.GameActionBlock, "user-1"); err != nil {
		t.Fatalf("first block returned error: %v", err)
	}
	_, _ = service.SetPhase(room.ID, domain.GamePhaseDay)
	_, _ = service.SetPhase(room.ID, domain.GamePhaseVoting)
	_, _ = service.SetPhase(room.ID, domain.GamePhaseNight)

	if _, err := service.SubmitAction(room.ID, "user-5", domain.GameActionBlock, "user-1"); err != ErrActionUnavailable {
		t.Fatalf("expected repeated block target to be unavailable, got %v", err)
	}
}

// --- Doctor (7-player game, user-2 = Doctor) ---

func TestDoctorCannotHealSameTargetConsecutiveNights(t *testing.T) {
	service := newTestService()
	room := testRoom(7) // user-2 = Doctor
	service.StartGame(room)
	// Game starts at night_doctor — no advance needed.

	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionHeal, "user-4"); err != nil {
		t.Fatalf("first heal returned error: %v", err)
	}
	_, _ = service.SetPhase(room.ID, domain.GamePhaseDay)
	_, _ = service.SetPhase(room.ID, domain.GamePhaseVoting)
	_, _ = service.SetPhase(room.ID, domain.GamePhaseNight)
	// Round 2, still at night_doctor.

	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionHeal, "user-4"); err != ErrActionUnavailable {
		t.Fatalf("expected repeated heal target to be unavailable, got %v", err)
	}
}

// --- Commissioner ---

func TestCommissionerSeesSideOnlyAndMistressCountsAsMafia(t *testing.T) {
	service := newTestService()
	room := testRoom(11) // user-1 = Commissioner, user-5 = Mistress
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightCommissioner)

	game, err := service.SubmitAction(room.ID, "user-1", domain.GameActionInspect, "user-5")
	if err != nil {
		t.Fatalf("inspect returned error: %v", err)
	}

	result := game.Events[len(game.Events)-1]
	if result.Type != "inspect.resolved" {
		t.Fatalf("expected inspect.resolved event, got %q", result.Type)
	}
	if result.Message != "Комісар перевірив Player 5: сторона Мафія." {
		t.Fatalf("unexpected inspect result message: %q", result.Message)
	}

	view := ViewForPlayer(game, "user-1")
	target := findPlayer(t, view, "user-5")
	if target.Role != "" {
		t.Fatalf("expected exact inspected role to stay hidden, got %q", target.Role)
	}
	if target.Side != domain.GameSideMafia {
		t.Fatalf("expected mafia side, got %q", target.Side)
	}
}

func TestCommissionerCannotInspectSameTargetTwice(t *testing.T) {
	service := newTestService()
	room := testRoom(7) // user-1 = Commissioner
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

// --- Mafia (8-player game = 2 mafia: user-3, user-4) ---

func TestMafiaNeedsAllUnblockedMafiaToShootSameTarget(t *testing.T) {
	service := newTestService()
	room := testRoom(8) // user-3 = Mafia, user-4 = Mafia
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightMafia)

	// Only one of two mafia shoots — target must survive.
	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionMafiaKill, "user-5"); err != nil {
		t.Fatalf("mafia kill returned error: %v", err)
	}
	game, err := service.SetPhase(room.ID, domain.GamePhaseDay)
	if err != nil {
		t.Fatalf("SetPhase day returned error: %v", err)
	}
	if !findPlayer(t, game, "user-5").IsAlive {
		t.Fatal("expected target to survive when one mafia did not shoot")
	}
}

func TestSingleMafiaCanKill(t *testing.T) {
	service := newTestService()
	room := testRoom(6) // user-3 = sole Mafia
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightMafia)

	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionMafiaKill, "user-5"); err != nil {
		t.Fatalf("single mafia kill returned error: %v", err)
	}
	game, err := service.SetPhase(room.ID, domain.GamePhaseDay)
	if err != nil {
		t.Fatalf("SetPhase day returned error: %v", err)
	}
	if findPlayer(t, game, "user-5").IsAlive {
		t.Fatal("expected target to die when the only alive mafia shoots")
	}
}

func TestMafiaCanTargetSelf(t *testing.T) {
	service := newTestService()
	room := testRoom(8) // user-3 = Mafia, user-4 = Mafia
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightMafia)

	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionMafiaKill, "user-3"); err != nil {
		t.Fatalf("first mafia self-target kill returned error: %v", err)
	}
	if _, err := service.SubmitAction(room.ID, "user-4", domain.GameActionMafiaKill, "user-3"); err != nil {
		t.Fatalf("second mafia self-target kill returned error: %v", err)
	}
	game, err := service.SetPhase(room.ID, domain.GamePhaseDay)
	if err != nil {
		t.Fatalf("SetPhase day returned error: %v", err)
	}
	if findPlayer(t, game, "user-3").IsAlive {
		t.Fatal("expected mafia self-target to be applied when all mafia choose the same target")
	}
}

func TestMafiaSameTargetKillsUnlessDoctorHeals(t *testing.T) {
	service := newTestService()
	room := testRoom(8) // user-2 = Doctor, user-3 = Mafia, user-4 = Mafia
	service.StartGame(room)
	// Game starts at night_doctor.
	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionHeal, "user-5"); err != nil {
		t.Fatalf("heal returned error: %v", err)
	}
	advanceToStep(t, service, room.ID, domain.GameStepNightMafia)
	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionMafiaKill, "user-5"); err != nil {
		t.Fatalf("first mafia kill returned error: %v", err)
	}
	if _, err := service.SubmitAction(room.ID, "user-4", domain.GameActionMafiaKill, "user-5"); err != nil {
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
	service := newTestService()
	room := testRoom(8) // user-3 = Mafia, user-4 = Mafia
	service.StartGame(room)
	advanceToStep(t, service, room.ID, domain.GameStepNightMafia)

	if _, err := service.SubmitAction(room.ID, "user-3", domain.GameActionMafiaKill, "user-5"); err != nil {
		t.Fatalf("first mafia kill returned error: %v", err)
	}
	if _, err := service.SubmitAction(room.ID, "user-4", domain.GameActionMafiaKill, "user-6"); err != nil {
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

// --- Voting ---

func TestVotingResolvesExileAndTieSkipsExile(t *testing.T) {
	service := newTestService()
	room := testRoom(7)
	service.StartGame(room)
	if _, err := service.SetPhase(room.ID, domain.GamePhaseVoting); err != nil {
		t.Fatalf("SetPhase voting returned error: %v", err)
	}

	if _, err := service.SubmitAction(room.ID, "user-1", domain.GameActionVote, "user-3"); err != nil {
		t.Fatalf("first vote returned error: %v", err)
	}
	if _, err := service.SubmitAction(room.ID, "user-2", domain.GameActionVote, "user-3"); err != nil {
		t.Fatalf("second vote returned error: %v", err)
	}
	game, err := service.SetPhase(room.ID, domain.GamePhaseNight)
	if err != nil {
		t.Fatalf("SetPhase night returned error: %v", err)
	}
	if findPlayer(t, game, "user-3").IsAlive {
		t.Fatal("expected voted target to be exiled")
	}
}

// --- Visibility ---

// Mafia members see each other's side but must NOT see the Mistress role.
// Requires 11-player game (user-3 = Mafia, user-4 = Mafia, user-5 = Mistress).
func TestViewForMafiaDoesNotRevealMistress(t *testing.T) {
	service := newTestService()
	room := testRoom(11)

	game := service.StartGame(room)

	mafia1 := findPlayerByRole(t, game, domain.GameRoleMafia)
	mistress := findPlayerByRole(t, game, domain.GameRoleMistress)

	var mafia2 domain.GamePlayer
	for _, p := range game.Players {
		if p.Role == domain.GameRoleMafia && p.ID != mafia1.ID {
			mafia2 = p
			break
		}
	}
	if mafia2.ID == "" {
		t.Fatal("expected at least two mafia players in an 11-player game")
	}

	view := ViewForPlayer(game, mafia1.ID)

	if findPlayer(t, view, mafia1.ID).Role != domain.GameRoleMafia {
		t.Fatal("expected mafia viewer to see own role")
	}
	if findPlayer(t, view, mafia2.ID).Role != domain.GameRoleMafia {
		t.Fatal("expected mafia viewer to see ordinary mafia teammate")
	}
	if findPlayer(t, view, mistress.ID).Role != "" {
		t.Fatal("expected mafia viewer not to see mistress role")
	}
}
