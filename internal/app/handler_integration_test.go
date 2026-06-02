package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"mafia-server/internal/domain"
)

type testPlayer struct {
	ID       string
	Token    string
	Nickname string
}

type authLoginResponse struct {
	User  domain.UserSession `json:"user"`
	Token string             `json:"token"`
}

type roomResponse struct {
	Room domain.Room `json:"room"`
}

type gameResponse struct {
	Game domain.Game `json:"game"`
}

type testAPI struct {
	t      *testing.T
	server *httptest.Server
	client *http.Client
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()

	server := httptest.NewServer(NewHandlerWithConfig(t.Context(), nil, DefaultSecurityConfig()))
	t.Cleanup(server.Close)

	return &testAPI{
		t:      t,
		server: server,
		client: server.Client(),
	}
}

func (api *testAPI) post(path string, token string, payload any) (int, []byte) {
	api.t.Helper()

	var body io.Reader
	if payload != nil {
		rawPayload, err := json.Marshal(payload)
		if err != nil {
			api.t.Fatalf("marshal payload for %s: %v", path, err)
		}
		body = bytes.NewReader(rawPayload)
	}

	request, err := http.NewRequest(http.MethodPost, api.server.URL+path, body)
	if err != nil {
		api.t.Fatalf("build POST request %s: %v", path, err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := api.client.Do(request)
	if err != nil {
		api.t.Fatalf("POST %s failed: %v", path, err)
	}
	defer response.Body.Close()

	rawBody, err := io.ReadAll(response.Body)
	if err != nil {
		api.t.Fatalf("read response body for %s: %v", path, err)
	}

	return response.StatusCode, rawBody
}

func (api *testAPI) get(path string, token string) (int, []byte) {
	api.t.Helper()

	request, err := http.NewRequest(http.MethodGet, api.server.URL+path, nil)
	if err != nil {
		api.t.Fatalf("build GET request %s: %v", path, err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := api.client.Do(request)
	if err != nil {
		api.t.Fatalf("GET %s failed: %v", path, err)
	}
	defer response.Body.Close()

	rawBody, err := io.ReadAll(response.Body)
	if err != nil {
		api.t.Fatalf("read response body for %s: %v", path, err)
	}

	return response.StatusCode, rawBody
}

func decodeJSON[T any](t *testing.T, raw []byte) T {
	t.Helper()

	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode JSON failed: %v; body=%s", err, string(raw))
	}

	return value
}

func mustStatus(t *testing.T, status int, expected int, body []byte) {
	t.Helper()
	if status != expected {
		t.Fatalf("unexpected status: got %d, want %d; body=%s", status, expected, string(body))
	}
}

func setupStartedRoom(t *testing.T, api *testAPI, playerCount int) (string, []testPlayer) {
	t.Helper()

	players := make([]testPlayer, 0, playerCount)
	for index := 1; index <= playerCount; index++ {
		nickname := fmt.Sprintf("Player%d", index)
		status, rawBody := api.post("/api/auth/login", "", map[string]string{"nickname": nickname})
		mustStatus(t, status, http.StatusOK, rawBody)

		login := decodeJSON[authLoginResponse](t, rawBody)
		players = append(players, testPlayer{
			ID:       login.User.ID,
			Token:    login.Token,
			Nickname: nickname,
		})
	}

	owner := players[0]
	status, rawBody := api.post("/api/rooms", owner.Token, map[string]any{
		"name":       "Integration Room",
		"maxPlayers": playerCount,
	})
	mustStatus(t, status, http.StatusCreated, rawBody)

	createdRoom := decodeJSON[roomResponse](t, rawBody)
	roomID := createdRoom.Room.ID

	for index := 1; index < len(players); index++ {
		joinStatus, joinBody := api.post("/api/rooms/"+roomID+"/join", players[index].Token, nil)
		mustStatus(t, joinStatus, http.StatusOK, joinBody)
	}

	startStatus, startBody := api.post("/api/rooms/"+roomID+"/start", owner.Token, nil)
	mustStatus(t, startStatus, http.StatusOK, startBody)

	return roomID, players
}

// advanceToRealRound drives the game past the introductory round 1 (acquaintance
// night + acquaintance day) via the owner's next-phase control, landing on
// round 2's first night step where night actions and votes take effect.
func advanceToRealRound(t *testing.T, api *testAPI, roomID string, ownerToken string) {
	t.Helper()
	for range 60 {
		if getGameFor(t, api, ownerToken, roomID).Round >= 2 {
			return
		}
		status, body := api.post("/api/games/"+roomID+"/next-phase", ownerToken, nil)
		mustStatus(t, status, http.StatusOK, body)
	}
	t.Fatalf("did not reach round 2 within step budget")
}

func getGameFor(t *testing.T, api *testAPI, token string, roomID string) domain.Game {
	t.Helper()

	status, rawBody := api.get("/api/games/"+roomID, token)
	mustStatus(t, status, http.StatusOK, rawBody)
	game := decodeJSON[gameResponse](t, rawBody)

	return game.Game
}

// getPlayerByRole finds the testPlayer whose game role matches the given role.
// Each player sees their own role in the personalized game view.
func getPlayerByRole(t *testing.T, api *testAPI, roomID string, players []testPlayer, role domain.GameRole) testPlayer {
	t.Helper()
	for _, p := range players {
		game := getGameFor(t, api, p.Token, roomID)
		for _, gp := range game.Players {
			if gp.ID == p.ID && gp.Role == role {
				return p
			}
		}
	}
	t.Fatalf("no player with role %q found in game", role)
	return testPlayer{}
}

// getPlayersByRole returns all testPlayers whose game role matches the given role.
func getPlayersByRole(t *testing.T, api *testAPI, roomID string, players []testPlayer, role domain.GameRole) []testPlayer {
	t.Helper()
	var result []testPlayer
	for _, p := range players {
		game := getGameFor(t, api, p.Token, roomID)
		for _, gp := range game.Players {
			if gp.ID == p.ID && gp.Role == role {
				result = append(result, p)
				break
			}
		}
	}
	return result
}

// findOtherPlayer returns the first player whose ID is not in excludeIDs.
func findOtherPlayer(players []testPlayer, excludeIDs ...string) testPlayer {
	excluded := make(map[string]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		excluded[id] = true
	}
	for _, p := range players {
		if !excluded[p.ID] {
			return p
		}
	}
	return testPlayer{}
}

func TestBlockedPlayerCanStillVoteViaAPI(t *testing.T) {
	api := newTestAPI(t)
	roomID, players := setupStartedRoom(t, api, 11)

	owner := players[0]
	doctor := getPlayerByRole(t, api, roomID, players, domain.GameRoleDoctor)
	mistress := getPlayerByRole(t, api, roomID, players, domain.GameRoleMistress)
	voteTarget := findOtherPlayer(players, owner.ID, doctor.ID, mistress.ID)

	advanceToRealRound(t, api, roomID, owner.Token)

	blockStatus, blockBody := api.post("/api/games/"+roomID+"/actions", mistress.Token, map[string]any{
		"type":     "mistress_block",
		"targetId": doctor.ID,
	})
	mustStatus(t, blockStatus, http.StatusOK, blockBody)

	setVotingStatus, setVotingBody := api.post("/api/games/"+roomID+"/phase", owner.Token, map[string]string{"phase": "voting"})
	mustStatus(t, setVotingStatus, http.StatusOK, setVotingBody)

	voteStatus, voteBody := api.post("/api/games/"+roomID+"/actions", doctor.Token, map[string]any{
		"type":     "vote",
		"targetId": voteTarget.ID,
	})
	mustStatus(t, voteStatus, http.StatusOK, voteBody)
}

func TestSingleMafiaCanKillViaAPI(t *testing.T) {
	api := newTestAPI(t)
	roomID, players := setupStartedRoom(t, api, 6)

	owner := players[0]
	mafia := getPlayerByRole(t, api, roomID, players, domain.GameRoleMafia)
	target := findOtherPlayer(players, owner.ID, mafia.ID)

	advanceToRealRound(t, api, roomID, owner.Token)

	// 6-player game has no Mistress: doctor → commissioner → mafia (2 advances)
	for range 2 {
		advanceStatus, advanceBody := api.post("/api/games/"+roomID+"/next-phase", owner.Token, nil)
		mustStatus(t, advanceStatus, http.StatusOK, advanceBody)
	}

	killStatus, killBody := api.post("/api/games/"+roomID+"/actions", mafia.Token, map[string]any{
		"type":     "mafia_kill",
		"targetId": target.ID,
	})
	mustStatus(t, killStatus, http.StatusOK, killBody)

	setDayStatus, setDayBody := api.post("/api/games/"+roomID+"/phase", owner.Token, map[string]string{"phase": "day"})
	mustStatus(t, setDayStatus, http.StatusOK, setDayBody)

	game := getGameFor(t, api, owner.Token, roomID)
	for _, player := range game.Players {
		if player.ID == target.ID {
			if player.IsAlive {
				t.Fatalf("expected target %s to be dead after single-mafia shot", target.ID)
			}
			return
		}
	}

	t.Fatalf("target %s not found in game players", target.ID)
}

func TestMafiaCanSelfTargetViaAPI(t *testing.T) {
	api := newTestAPI(t)
	roomID, players := setupStartedRoom(t, api, 8) // 8-player game = 2 mafia

	owner := players[0]
	mafias := getPlayersByRole(t, api, roomID, players, domain.GameRoleMafia)
	if len(mafias) < 2 {
		t.Fatalf("expected at least 2 mafia players for 8-player game, got %d", len(mafias))
	}
	mafiaOne := mafias[0]
	mafiaTwo := mafias[1]

	advanceToRealRound(t, api, roomID, owner.Token)

	// 8-player game has no Mistress: doctor → commissioner → mafia (2 advances)
	for range 2 {
		advanceStatus, advanceBody := api.post("/api/games/"+roomID+"/next-phase", owner.Token, nil)
		mustStatus(t, advanceStatus, http.StatusOK, advanceBody)
	}

	firstShotStatus, firstShotBody := api.post("/api/games/"+roomID+"/actions", mafiaOne.Token, map[string]any{
		"type":     "mafia_kill",
		"targetId": mafiaOne.ID,
	})
	mustStatus(t, firstShotStatus, http.StatusOK, firstShotBody)

	secondShotStatus, secondShotBody := api.post("/api/games/"+roomID+"/actions", mafiaTwo.Token, map[string]any{
		"type":     "mafia_kill",
		"targetId": mafiaOne.ID,
	})
	mustStatus(t, secondShotStatus, http.StatusOK, secondShotBody)

	setDayStatus, setDayBody := api.post("/api/games/"+roomID+"/phase", owner.Token, map[string]string{"phase": "day"})
	mustStatus(t, setDayStatus, http.StatusOK, setDayBody)

	game := getGameFor(t, api, owner.Token, roomID)
	for _, player := range game.Players {
		if player.ID == mafiaOne.ID {
			if player.IsAlive {
				t.Fatalf("expected mafia self-target %s to be dead after consensus shot", mafiaOne.ID)
			}
			return
		}
	}

	t.Fatalf("mafia player %s not found in game players", mafiaOne.ID)
}

func TestMistressBlockPreventsDoctorHeal(t *testing.T) {
	api := newTestAPI(t)
	roomID, players := setupStartedRoom(t, api, 11)

	owner := players[0]
	doctor := getPlayerByRole(t, api, roomID, players, domain.GameRoleDoctor)
	mistress := getPlayerByRole(t, api, roomID, players, domain.GameRoleMistress)
	target := findOtherPlayer(players, owner.ID, doctor.ID, mistress.ID)

	advanceToRealRound(t, api, roomID, owner.Token)

	blockStatus, blockBody := api.post("/api/games/"+roomID+"/actions", mistress.Token, map[string]any{
		"type":     "mistress_block",
		"targetId": doctor.ID,
	})
	mustStatus(t, blockStatus, http.StatusOK, blockBody)

	advanceStatus, advanceBody := api.post("/api/games/"+roomID+"/next-phase", owner.Token, nil)
	mustStatus(t, advanceStatus, http.StatusOK, advanceBody)

	healStatus, healBody := api.post("/api/games/"+roomID+"/actions", doctor.Token, map[string]any{
		"type":     "heal",
		"targetId": target.ID,
	})
	mustStatus(t, healStatus, http.StatusConflict, healBody)
}

func TestNonOwnerCannotAdvancePhase(t *testing.T) {
	api := newTestAPI(t)
	roomID, players := setupStartedRoom(t, api, 7)

	nonOwner := players[1]

	advanceStatus, advanceBody := api.post("/api/games/"+roomID+"/next-phase", nonOwner.Token, nil)
	mustStatus(t, advanceStatus, http.StatusForbidden, advanceBody)
}

func TestVotingResolvesExile(t *testing.T) {
	api := newTestAPI(t)
	roomID, players := setupStartedRoom(t, api, 7)

	owner := players[0]
	advanceToRealRound(t, api, roomID, owner.Token)

	setVotingStatus, setVotingBody := api.post("/api/games/"+roomID+"/phase", owner.Token, map[string]string{"phase": "voting"})
	mustStatus(t, setVotingStatus, http.StatusOK, setVotingBody)

	voteTarget := players[1]
	for index := 0; index < len(players); index++ {
		if players[index].ID == voteTarget.ID {
			continue
		}
		voteStatus, voteBody := api.post("/api/games/"+roomID+"/actions", players[index].Token, map[string]any{
			"type":     "vote",
			"targetId": voteTarget.ID,
		})
		mustStatus(t, voteStatus, http.StatusOK, voteBody)
	}

	// Voting resolves into the last-word step: the target is NOT eliminated yet.
	lwStatus, lwBody := api.post("/api/games/"+roomID+"/next-phase", owner.Token, nil)
	mustStatus(t, lwStatus, http.StatusOK, lwBody)
	lwGame := getGameFor(t, api, owner.Token, roomID)
	if lwGame.Step != domain.GameStepDayLastWord {
		t.Fatalf("expected last-word step after voting, got %s", lwGame.Step)
	}
	for _, player := range lwGame.Players {
		if player.ID == voteTarget.ID && !player.IsAlive {
			t.Fatalf("voted target %s should still be alive during last word", voteTarget.ID)
		}
	}

	// Last word ends -> exile is finalized.
	finStatus, finBody := api.post("/api/games/"+roomID+"/next-phase", owner.Token, nil)
	mustStatus(t, finStatus, http.StatusOK, finBody)

	game := getGameFor(t, api, owner.Token, roomID)
	for _, player := range game.Players {
		if player.ID == voteTarget.ID {
			if player.IsAlive {
				t.Fatalf("expected voted target %s to be exiled after last word", voteTarget.ID)
			}
			return
		}
	}

	t.Fatalf("vote target %s not found in game players", voteTarget.ID)
}

func TestGameStartsAtNight(t *testing.T) {
	api := newTestAPI(t)

	// 7-player game (no Mistress) starts at night_doctor.
	roomID7, players7 := setupStartedRoom(t, api, 7)
	game7 := getGameFor(t, api, players7[0].Token, roomID7)
	if game7.Phase != domain.GamePhaseNight {
		t.Fatalf("expected night phase, got %s", game7.Phase)
	}
	if game7.Step != domain.GameStepNightDoctor {
		t.Fatalf("expected night_doctor step for 7-player game, got %s", game7.Step)
	}

	// 11-player game (has Mistress) starts at night_mistress.
	roomID11, players11 := setupStartedRoom(t, api, 11)
	game11 := getGameFor(t, api, players11[0].Token, roomID11)
	if game11.Step != domain.GameStepNightMistress {
		t.Fatalf("expected night_mistress step for 11-player game, got %s", game11.Step)
	}
}

func TestRecoveryReturnsParticipantState(t *testing.T) {
	api := newTestAPI(t)
	roomID, players := setupStartedRoom(t, api, 7)

	status, rawBody := api.get("/api/recovery", players[1].Token)
	mustStatus(t, status, http.StatusOK, rawBody)

	recovery := decodeJSON[recoveryResponse](t, rawBody)
	if recovery.User.ID != players[1].ID {
		t.Fatalf("expected recovered user %s, got %s", players[1].ID, recovery.User.ID)
	}
	if recovery.ActiveRoomID != roomID {
		t.Fatalf("expected active room id %s, got %s", roomID, recovery.ActiveRoomID)
	}
	if len(recovery.Rooms) == 0 {
		t.Fatalf("expected at least one room in recovery response")
	}
	if len(recovery.Games) == 0 {
		t.Fatalf("expected at least one game in recovery response")
	}
}

func TestLoginRateLimit(t *testing.T) {
	api := newTestAPI(t)

	lastStatus := http.StatusOK
	var lastBody []byte
	for index := 0; index < 31; index++ {
		nickname := fmt.Sprintf("rate-limit-user-%d", index)
		lastStatus, lastBody = api.post("/api/auth/login", "", map[string]string{"nickname": nickname})
	}

	mustStatus(t, lastStatus, http.StatusTooManyRequests, lastBody)
}

func TestRoomMutationRateLimit(t *testing.T) {
	api := newTestAPI(t)

	loginStatus, loginBody := api.post("/api/auth/login", "", map[string]string{"nickname": "room-rate-owner"})
	mustStatus(t, loginStatus, http.StatusOK, loginBody)
	login := decodeJSON[authLoginResponse](t, loginBody)

	lastStatus := http.StatusCreated
	var lastBody []byte
	for index := 0; index < 61; index++ {
		lastStatus, lastBody = api.post("/api/rooms", login.Token, map[string]any{
			"name":       fmt.Sprintf("Room %d", index),
			"maxPlayers": 6,
		})
		if lastStatus == http.StatusTooManyRequests {
			break
		}
	}

	mustStatus(t, lastStatus, http.StatusTooManyRequests, lastBody)
}
