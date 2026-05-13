package games

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"slices"
	"sort"
	"sync"
	"time"

	"mafia-server/internal/domain"
	"mafia-server/internal/ids"
	"mafia-server/internal/persistence"
)

var (
	ErrGameNotFound      = errors.New("game not found")
	ErrInvalidPhase      = errors.New("invalid game phase")
	ErrInvalidAction     = errors.New("invalid game action")
	ErrActionUnavailable = errors.New("action is unavailable in the current phase")
	ErrPlayerNotFound    = errors.New("player not found")
	ErrPlayerDead        = errors.New("player is not alive")
	ErrTargetDead        = errors.New("target is not alive")
)

var validPhases = []domain.GamePhase{
	domain.GamePhaseNight,
	domain.GamePhaseDay,
	domain.GamePhaseVoting,
	domain.GamePhaseFinal,
}

var stepDurations = map[domain.GameStep]time.Duration{
	domain.GameStepNightMistress:     15 * time.Second,
	domain.GameStepNightDoctor:       15 * time.Second,
	domain.GameStepNightCommissioner: 15 * time.Second,
	domain.GameStepNightMafia:        30 * time.Second,
	domain.GameStepDaySpeech:         60 * time.Second,
	domain.GameStepDayDiscussion:     90 * time.Second,
	domain.GameStepVoting:            35 * time.Second,
	domain.GameStepFinal:             0,
}

type nightStep struct {
	step domain.GameStep
	role domain.GameRole
}

var nightSteps = []nightStep{
	{step: domain.GameStepNightMistress, role: domain.GameRoleMistress},
	{step: domain.GameStepNightDoctor, role: domain.GameRoleDoctor},
	{step: domain.GameStepNightCommissioner, role: domain.GameRoleCommissioner},
	{step: domain.GameStepNightMafia, role: domain.GameRoleMafia},
}

// rolesForCount returns the role slice for n players.
// The slice is shuffled by rolePerm so the order here is canonical, not seat order.
//
//	 6–7  players: 1 Mafia, 1 Commissioner, 1 Doctor
//	 8–10 players: 2 Mafia, 1 Commissioner, 1 Doctor
//	11–13 players: 2 Mafia, 1 Commissioner, 1 Doctor, 1 Mistress
//	14–16 players: 3 Mafia, 1 Commissioner, 1 Doctor, 1 Mistress
func rolesForCount(n int) []domain.GameRole {
	var mafiaCount int
	hasMistress := false
	switch {
	case n <= 7:
		mafiaCount = 1
	case n <= 10:
		mafiaCount = 2
	case n <= 13:
		mafiaCount = 2
		hasMistress = true
	default:
		mafiaCount = 3
		hasMistress = true
	}

	roles := make([]domain.GameRole, 0, n)
	roles = append(roles, domain.GameRoleCommissioner)
	roles = append(roles, domain.GameRoleDoctor)
	for range mafiaCount {
		roles = append(roles, domain.GameRoleMafia)
	}
	if hasMistress {
		roles = append(roles, domain.GameRoleMistress)
	}
	for len(roles) < n {
		roles = append(roles, domain.GameRoleCivilian)
	}
	return roles
}

type Service struct {
	mu       sync.RWMutex
	byRoom   map[string]domain.Game
	store    persistence.Store
	rolePerm func(int) []int // injectable for tests; defaults to cryptoRandPerm
}

func NewService() *Service {
	return NewServiceWithStore(persistence.NewNoopStore())
}

func NewServiceWithStore(store persistence.Store) *Service {
	if store == nil {
		store = persistence.NewNoopStore()
	}

	service := &Service{
		byRoom:   make(map[string]domain.Game),
		store:    store,
		rolePerm: cryptoRandPerm,
	}

	var loaded map[string]domain.Game
	err := store.Load("games.state", &loaded)
	switch {
	case errors.Is(err, persistence.ErrNotFound):
	case err != nil:
		log.Printf("games: cannot load state from store: %v", err)
	default:
		service.byRoom = loaded
	}

	return service
}

func (s *Service) persistLocked() {
	if err := s.store.Save("games.state", s.byRoom); err != nil {
		log.Printf("games: cannot persist state: %v", err)
	}
}

func (s *Service) StartGame(room domain.Room) domain.Game {
	s.mu.Lock()
	defer s.mu.Unlock()

	if game, ok := s.byRoom[room.ID]; ok {
		game.Players = s.playersFromRoom(room)
		game.UpdatedAt = formatTime(time.Now().UTC())
		s.byRoom[room.ID] = game
		s.persistLocked()
		return cloneGame(game)
	}

	now := time.Now().UTC()
	game := domain.Game{
		ID:      ids.NewID("game"),
		RoomID:  room.ID,
		Round:   1,
		Players: s.playersFromRoom(room),
	}
	startNight(&game, now, false)
	game.StartedAt = formatTime(now)
	game.UpdatedAt = formatTime(now)

	s.byRoom[room.ID] = game
	s.persistLocked()
	return cloneGame(game)
}

func (s *Service) GetByRoomID(roomID string) (domain.Game, bool) {
	s.mu.RLock()
	game, ok := s.byRoom[roomID]
	s.mu.RUnlock()
	if !ok {
		return domain.Game{}, false
	}

	return cloneGame(game), true
}

func ViewForPlayer(game domain.Game, viewerID string) domain.Game {
	view := cloneGame(game)
	viewer, ok := playerByID(game.Players, viewerID)
	if !ok {
		return view
	}

	revealAll := game.Phase == domain.GamePhaseFinal
	inspectedTargets := inspectedTargetsBy(game.Actions, viewerID)
	for index := range view.Players {
		player := game.Players[index]
		switch {
		case revealAll:
			view.Players[index].Side = sideForRole(player.Role)
		case player.ID == viewerID:
			view.Players[index].Side = sideForRole(player.Role)
		case viewer.Role == domain.GameRoleMafia && player.Role == domain.GameRoleMafia:
			view.Players[index].Side = domain.GameSideMafia
		default:
			view.Players[index].Role = ""
			view.Players[index].Side = ""
			if _, inspected := inspectedTargets[player.ID]; inspected {
				view.Players[index].Side = sideForRole(player.Role)
			}
		}
	}

	view.Actions = visibleActions(game.Actions, viewer, revealAll)
	view.Events = visibleEvents(game.Events, viewer, revealAll)
	return view
}

func (s *Service) SetPhase(roomID string, phase domain.GamePhase) (domain.Game, error) {
	if !slices.Contains(validPhases, phase) {
		return domain.Game{}, ErrInvalidPhase
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	game, ok := s.byRoom[roomID]
	if !ok {
		return domain.Game{}, ErrGameNotFound
	}

	transitionToPhase(&game, phase, time.Now().UTC())
	s.byRoom[roomID] = game
	s.persistLocked()
	return cloneGame(game), nil
}

func (s *Service) AdvancePhase(roomID string) (domain.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	game, ok := s.byRoom[roomID]
	if !ok {
		return domain.Game{}, ErrGameNotFound
	}

	advanceStep(&game, time.Now().UTC(), "manual")
	s.byRoom[roomID] = game
	s.persistLocked()
	return cloneGame(game), nil
}

func (s *Service) AdvanceExpired(now time.Time) []domain.Game {
	s.mu.Lock()
	defer s.mu.Unlock()

	advanced := make([]domain.Game, 0)
	for roomID, game := range s.byRoom {
		if game.Phase == domain.GamePhaseFinal || game.PhaseEndsAt == "" {
			continue
		}

		phaseEndsAt, err := time.Parse(time.RFC3339Nano, game.PhaseEndsAt)
		if err != nil || now.Before(phaseEndsAt) {
			continue
		}

		advanceStep(&game, now.UTC(), "timer")
		s.byRoom[roomID] = game
		advanced = append(advanced, cloneGame(game))
	}

	if len(advanced) > 0 {
		s.persistLocked()
	}

	return advanced
}

func (s *Service) SubmitAction(roomID string, actorID string, actionType domain.GameActionType, targetID string) (domain.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()

	game, ok := s.byRoom[roomID]
	if !ok {
		return domain.Game{}, ErrGameNotFound
	}

	actorIndex := playerIndex(game.Players, actorID)
	if actorIndex < 0 {
		return domain.Game{}, ErrPlayerNotFound
	}

	targetIndex := playerIndex(game.Players, targetID)
	if targetIndex < 0 {
		return domain.Game{}, ErrPlayerNotFound
	}

	actor := game.Players[actorIndex]
	target := game.Players[targetIndex]
	if !actor.IsAlive {
		return domain.Game{}, ErrPlayerDead
	}
	if !target.IsAlive {
		return domain.Game{}, ErrTargetDead
	}
	if isPhaseExpired(game, now) {
		return domain.Game{}, ErrActionUnavailable
	}
	if isNightRoleAction(actionType) && isBlockedThisRound(game, actor.ID) {
		return domain.Game{}, ErrActionUnavailable
	}
	if err := validateAction(game, actor, target, actionType); err != nil {
		return domain.Game{}, err
	}
	if err := validateTargetHistory(game, actor.ID, actionType, target.ID); err != nil {
		return domain.Game{}, err
	}

	nowText := formatTime(now)
	action := domain.GameAction{
		ID:             ids.NewID("action"),
		Type:           actionType,
		ActorID:        actor.ID,
		ActorNickname:  actor.Nickname,
		TargetID:       target.ID,
		TargetNickname: target.Nickname,
		Phase:          game.Phase,
		Round:          game.Round,
		CreatedAt:      nowText,
	}

	game.Actions = upsertAction(game.Actions, action)
	game.Events = append(game.Events, domain.GameEvent{
		ID:        ids.NewID("event"),
		Type:      "action.recorded",
		Message:   actionRecordedMessage(actionType, actor.Nickname, target.Nickname),
		Phase:     game.Phase,
		Round:     game.Round,
		ActorID:   actor.ID,
		TargetID:  target.ID,
		CreatedAt: nowText,
	})
	if actionType == domain.GameActionInspect {
		game.Events = append(game.Events, domain.GameEvent{
			ID:        ids.NewID("event"),
			Type:      "inspect.resolved",
			Message:   fmt.Sprintf("Комісар перевірив %s: сторона %s.", target.Nickname, inspectLabel(target.Role)),
			Phase:     game.Phase,
			Round:     game.Round,
			ActorID:   actor.ID,
			TargetID:  target.ID,
			CreatedAt: nowText,
		})
	}
	game.UpdatedAt = nowText
	s.byRoom[roomID] = game
	s.persistLocked()
	return cloneGame(game), nil
}

func transitionToPhase(game *domain.Game, phase domain.GamePhase, now time.Time) {
	if phase != game.Phase {
		resolveBeforeLeavingPhase(game)
	}

	if maybeFinishGame(game) {
		setFinal(game, now)
		return
	}

	switch phase {
	case domain.GamePhaseNight:
		startNight(game, now, game.Phase != domain.GamePhaseNight)
	case domain.GamePhaseDay:
		startDaySpeeches(game, now)
	case domain.GamePhaseVoting:
		setStepWindow(game, domain.GameStepVoting, now)
	case domain.GamePhaseFinal:
		setFinal(game, now)
	}
	game.UpdatedAt = formatTime(now)
}

func advanceStep(game *domain.Game, now time.Time, reason string) {
	if game.Phase == domain.GamePhaseFinal {
		return
	}

	advanced := false
	switch game.Step {
	case domain.GameStepNightMistress, domain.GameStepNightDoctor, domain.GameStepNightCommissioner:
		advanceNightRoleStep(game, now)
		advanced = true
	case domain.GameStepNightMafia:
		resolveNight(game)
		if maybeFinishGame(game) {
			setFinal(game, now)
		} else {
			startDaySpeeches(game, now)
		}
		advanced = true
	case domain.GameStepDaySpeech:
		advanceDaySpeech(game, now)
		advanced = true
	case domain.GameStepDayDiscussion:
		setStepWindow(game, domain.GameStepVoting, now)
		advanced = true
	case domain.GameStepVoting:
		resolveVoting(game)
		if maybeFinishGame(game) {
			setFinal(game, now)
		} else {
			startNight(game, now, true)
		}
		advanced = true
	default:
		transitionToPhase(game, nextPhase(game.Phase), now)
		advanced = true
	}

	if !advanced || game.Phase == domain.GamePhaseFinal {
		game.UpdatedAt = formatTime(now)
		return
	}

	message := "Фаза змінена."
	if reason == "timer" {
		message = "Час підфази завершився. Сервер перейшов далі."
	}
	game.Events = append(game.Events, newGameEvent("phase.advanced", message, game.Phase, game.Round, "", ""))
	game.UpdatedAt = formatTime(now)
}

func nextPhase(phase domain.GamePhase) domain.GamePhase {
	switch phase {
	case domain.GamePhaseNight:
		return domain.GamePhaseDay
	case domain.GamePhaseDay:
		return domain.GamePhaseVoting
	case domain.GamePhaseVoting:
		return domain.GamePhaseNight
	default:
		return domain.GamePhaseFinal
	}
}

func startNight(game *domain.Game, now time.Time, incrementRound bool) {
	if incrementRound {
		game.Round++
	}
	game.FirstSpeakerIndex = 0
	game.SpeechIndex = 0
	setNextNightStep(game, 0, now)
}

func advanceNightRoleStep(game *domain.Game, now time.Time) {
	currentIndex := 0
	for index, step := range nightSteps {
		if step.step == game.Step {
			currentIndex = index + 1
			break
		}
	}
	setNextNightStep(game, currentIndex, now)
}

func setNextNightStep(game *domain.Game, startIndex int, now time.Time) {
	for index := startIndex; index < len(nightSteps); index++ {
		step := nightSteps[index]
		if !hasRole(game.Players, step.role) {
			// Role not assigned in this game — skip without timer so players
			// learn upfront which special roles are in play.
			continue
		}
		// Role exists: always show the full timer even if the holder died.
		// This hides whether an eliminated player was the doctor, commissioner,
		// or mistress — observers cannot deduce it from a skipped step.
		// Mafia is the only exception: if all mafia died the game is already over.
		if step.role == domain.GameRoleMafia && !hasAliveRole(game.Players, step.role) {
			continue
		}
		setStepWindow(game, step.step, now)
		return
	}

	resolveNight(game)
	if maybeFinishGame(game) {
		setFinal(game, now)
		return
	}
	startDaySpeeches(game, now)
}

func hasRole(players []domain.GamePlayer, role domain.GameRole) bool {
	for _, player := range players {
		if player.Role == role {
			return true
		}
	}
	return false
}

func startDaySpeeches(game *domain.Game, now time.Time) {
	game.FirstSpeakerIndex = 0
	if len(game.Players) > 0 {
		game.FirstSpeakerIndex = (game.Round - 1) % len(game.Players)
	}
	game.SpeechIndex = 0
	if len(speechOrder(*game)) == 0 {
		setStepWindow(game, domain.GameStepDayDiscussion, now)
		return
	}
	setStepWindow(game, domain.GameStepDaySpeech, now)
}

func advanceDaySpeech(game *domain.Game, now time.Time) {
	game.SpeechIndex++
	if game.SpeechIndex >= len(speechOrder(*game)) {
		setStepWindow(game, domain.GameStepDayDiscussion, now)
		return
	}
	setStepWindow(game, domain.GameStepDaySpeech, now)
}

func setFinal(game *domain.Game, now time.Time) {
	setStepWindow(game, domain.GameStepFinal, now)
}

func setStepWindow(game *domain.Game, step domain.GameStep, now time.Time) {
	duration := stepDurations[step]
	game.Step = step
	game.Phase = phaseForStep(step)
	game.ActiveRole = ""
	game.ActivePlayerID = ""
	game.ActivePlayerNickname = ""

	switch step {
	case domain.GameStepNightMistress:
		setActiveRole(game, domain.GameRoleMistress)
	case domain.GameStepNightDoctor:
		setActiveRole(game, domain.GameRoleDoctor)
	case domain.GameStepNightCommissioner:
		setActiveRole(game, domain.GameRoleCommissioner)
	case domain.GameStepNightMafia:
		game.ActiveRole = domain.GameRoleMafia
	case domain.GameStepDaySpeech:
		order := speechOrder(*game)
		if game.SpeechIndex < len(order) {
			game.ActivePlayerID = order[game.SpeechIndex].ID
			game.ActivePlayerNickname = order[game.SpeechIndex].Nickname
		}
	}

	game.PhaseStartedAt = formatTime(now)
	game.PhaseDurationSeconds = int(duration.Seconds())
	if duration <= 0 {
		game.PhaseEndsAt = ""
		return
	}
	game.PhaseEndsAt = formatTime(now.Add(duration))
}

func phaseForStep(step domain.GameStep) domain.GamePhase {
	switch step {
	case domain.GameStepNightMistress, domain.GameStepNightDoctor, domain.GameStepNightCommissioner, domain.GameStepNightMafia:
		return domain.GamePhaseNight
	case domain.GameStepDaySpeech, domain.GameStepDayDiscussion:
		return domain.GamePhaseDay
	case domain.GameStepVoting:
		return domain.GamePhaseVoting
	default:
		return domain.GamePhaseFinal
	}
}

func setActiveRole(game *domain.Game, role domain.GameRole) {
	game.ActiveRole = role
	for _, player := range game.Players {
		if player.Role == role && player.IsAlive {
			game.ActivePlayerID = player.ID
			game.ActivePlayerNickname = player.Nickname
			return
		}
	}
}

func speechOrder(game domain.Game) []domain.GamePlayer {
	if len(game.Players) == 0 {
		return nil
	}

	players := make([]domain.GamePlayer, 0, len(game.Players))
	start := game.FirstSpeakerIndex % len(game.Players)
	for offset := 0; offset < len(game.Players); offset++ {
		player := game.Players[(start+offset)%len(game.Players)]
		if player.IsAlive {
			players = append(players, player)
		}
	}
	return players
}

func resolveBeforeLeavingPhase(game *domain.Game) {
	switch game.Phase {
	case domain.GamePhaseNight:
		resolveNight(game)
	case domain.GamePhaseVoting:
		resolveVoting(game)
	}
}

func isPhaseExpired(game domain.Game, now time.Time) bool {
	if game.Phase == domain.GamePhaseFinal || game.PhaseEndsAt == "" {
		return false
	}

	phaseEndsAt, err := time.Parse(time.RFC3339Nano, game.PhaseEndsAt)
	return err == nil && !now.Before(phaseEndsAt)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func (s *Service) playersFromRoom(room domain.Room) []domain.GamePlayer {
	roles := rolesForCount(len(room.Players))
	perm := s.rolePerm(len(room.Players))
	players := make([]domain.GamePlayer, 0, len(room.Players))
	for i, player := range room.Players {
		role := roles[perm[i]]
		players = append(players, domain.GamePlayer{
			ID:       player.ID,
			Nickname: player.Nickname,
			IsOwner:  player.IsOwner,
			Role:     role,
			Side:     sideForRole(role),
			IsAlive:  true,
		})
	}

	return players
}

// cryptoRandPerm returns a cryptographically random permutation of [0, n).
func cryptoRandPerm(n int) []int {
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := cryptoRandInt(i + 1)
		perm[i], perm[j] = perm[j], perm[i]
	}
	return perm
}

func cryptoRandInt(max int) int {
	if max <= 1 {
		return 0
	}
	n, err := crand.Int(crand.Reader, big.NewInt(int64(max)))
	if err != nil {
		panic(fmt.Sprintf("crypto rand: %v", err))
	}
	return int(n.Int64())
}

func cloneGame(game domain.Game) domain.Game {
	game.Players = append([]domain.GamePlayer(nil), game.Players...)
	game.Actions = append([]domain.GameAction(nil), game.Actions...)
	game.Events = append([]domain.GameEvent(nil), game.Events...)
	return game
}

func playerIndex(players []domain.GamePlayer, playerID string) int {
	for index, player := range players {
		if player.ID == playerID {
			return index
		}
	}

	return -1
}

func playerByID(players []domain.GamePlayer, playerID string) (domain.GamePlayer, bool) {
	for _, player := range players {
		if player.ID == playerID {
			return player, true
		}
	}

	return domain.GamePlayer{}, false
}

func hasAliveRole(players []domain.GamePlayer, role domain.GameRole) bool {
	for _, player := range players {
		if player.Role == role && player.IsAlive {
			return true
		}
	}
	return false
}

func validateAction(game domain.Game, actor domain.GamePlayer, target domain.GamePlayer, actionType domain.GameActionType) error {
	isSelfTarget := actor.ID == target.ID
	switch actionType {
	case domain.GameActionBlock:
		if game.Step != domain.GameStepNightMistress || actor.Role != domain.GameRoleMistress || isSelfTarget {
			return ErrActionUnavailable
		}
	case domain.GameActionHeal:
		if game.Step != domain.GameStepNightDoctor || actor.Role != domain.GameRoleDoctor {
			return ErrActionUnavailable
		}
	case domain.GameActionInspect:
		if game.Step != domain.GameStepNightCommissioner || actor.Role != domain.GameRoleCommissioner || isSelfTarget {
			return ErrActionUnavailable
		}
	case domain.GameActionMafiaKill:
		if game.Step != domain.GameStepNightMafia || actor.Role != domain.GameRoleMafia {
			return ErrActionUnavailable
		}
	case domain.GameActionVote:
		if game.Step != domain.GameStepVoting || isSelfTarget {
			return ErrActionUnavailable
		}
	default:
		return ErrInvalidAction
	}

	return nil
}

func isNightRoleAction(actionType domain.GameActionType) bool {
	switch actionType {
	case domain.GameActionBlock, domain.GameActionHeal, domain.GameActionInspect, domain.GameActionMafiaKill:
		return true
	default:
		return false
	}
}

func validateTargetHistory(game domain.Game, actorID string, actionType domain.GameActionType, targetID string) error {
	switch actionType {
	case domain.GameActionBlock, domain.GameActionHeal:
		if previousRoundTarget(game.Actions, actorID, actionType, game.Round) == targetID {
			return ErrActionUnavailable
		}
	case domain.GameActionInspect:
		if hasPriorInspectTarget(game.Actions, actorID, targetID, game.Round) {
			return ErrActionUnavailable
		}
	}

	return nil
}

func previousRoundTarget(actions []domain.GameAction, actorID string, actionType domain.GameActionType, round int) string {
	for _, action := range actions {
		if action.ActorID == actorID && action.Type == actionType && action.Round == round-1 {
			return action.TargetID
		}
	}
	return ""
}

func hasPriorInspectTarget(actions []domain.GameAction, actorID string, targetID string, currentRound int) bool {
	for _, action := range actions {
		if action.ActorID == actorID &&
			action.Type == domain.GameActionInspect &&
			action.TargetID == targetID &&
			action.Round != currentRound {
			return true
		}
	}
	return false
}

func upsertAction(actions []domain.GameAction, action domain.GameAction) []domain.GameAction {
	for index, current := range actions {
		if current.ActorID == action.ActorID &&
			current.Type == action.Type &&
			current.Phase == action.Phase &&
			current.Round == action.Round {
			actions[index] = action
			return actions
		}
	}

	return append(actions, action)
}

func actionRecordedMessage(actionType domain.GameActionType, actorNickname string, targetNickname string) string {
	switch actionType {
	case domain.GameActionBlock:
		return fmt.Sprintf("%s заблокувала нічний хід %s.", actorNickname, targetNickname)
	case domain.GameActionMafiaKill:
		return fmt.Sprintf("%s вибрав ціль для нічного удару.", actorNickname)
	case domain.GameActionInspect:
		return fmt.Sprintf("%s вибрав гравця для перевірки.", actorNickname)
	case domain.GameActionHeal:
		return fmt.Sprintf("%s вибрав гравця для лікування.", actorNickname)
	case domain.GameActionVote:
		return fmt.Sprintf("%s голосує проти %s.", actorNickname, targetNickname)
	default:
		return fmt.Sprintf("%s виконав дію.", actorNickname)
	}
}

func resolveNight(game *domain.Game) {
	actions := actionsForCurrentPhase(*game)
	healedTargets := make(map[string]struct{})
	for _, action := range actions {
		if action.Type == domain.GameActionHeal && !isBlockedThisRound(*game, action.ActorID) {
			healedTargets[action.TargetID] = struct{}{}
		}
	}

	targetID, ok := mafiaTarget(*game)
	if !ok {
		game.Events = append(game.Events, newGameEvent("night.miss.resolved", "Мафія промахнулась. Цієї ночі нікого не вбито.", game.Phase, game.Round, "", ""))
		return
	}

	target, ok := playerByID(game.Players, targetID)
	if !ok {
		return
	}
	if _, healed := healedTargets[targetID]; healed {
		game.Events = append(game.Events, newGameEvent(
			"night.heal.resolved",
			"Лікар врятував ціль. Цієї ночі нікого не вбито.",
			game.Phase,
			game.Round,
			"",
			target.ID,
		))
		return
	}

	setPlayerAlive(game.Players, targetID, false)
	game.Events = append(game.Events, newGameEvent(
		"night.kill.resolved",
		fmt.Sprintf("%s не пережив ніч.", target.Nickname),
		game.Phase,
		game.Round,
		"",
		target.ID,
	))
}

func mafiaTarget(game domain.Game) (string, bool) {
	unblockedMafia := make(map[string]struct{})
	for _, player := range game.Players {
		if player.Role == domain.GameRoleMafia && player.IsAlive && !isBlockedThisRound(game, player.ID) {
			unblockedMafia[player.ID] = struct{}{}
		}
	}
	if len(unblockedMafia) < 1 {
		return "", false
	}

	actorTargets := make(map[string]string)
	for _, action := range actionsForCurrentPhase(game) {
		if action.Type != domain.GameActionMafiaKill {
			continue
		}
		if _, ok := unblockedMafia[action.ActorID]; !ok {
			continue
		}
		target, ok := playerByID(game.Players, action.TargetID)
		if !ok || !target.IsAlive {
			continue
		}
		actorTargets[action.ActorID] = action.TargetID
	}

	if len(actorTargets) != len(unblockedMafia) {
		return "", false
	}

	var targetID string
	for _, currentTargetID := range actorTargets {
		if targetID == "" {
			targetID = currentTargetID
			continue
		}
		if targetID != currentTargetID {
			return "", false
		}
	}

	return targetID, targetID != ""
}

func resolveVoting(game *domain.Game) {
	actions := actionsForCurrentPhase(*game)
	targetID, ok := topTarget(actions, domain.GameActionVote)
	if !ok {
		game.Events = append(game.Events, newGameEvent("vote.resolved", "Голосування завершилось без вигнання.", game.Phase, game.Round, "", ""))
		return
	}

	target, ok := playerByID(game.Players, targetID)
	if !ok {
		return
	}

	setPlayerAlive(game.Players, targetID, false)
	game.Events = append(game.Events, newGameEvent(
		"vote.exile.resolved",
		fmt.Sprintf("%s вигнаний голосуванням міста.", target.Nickname),
		game.Phase,
		game.Round,
		"",
		target.ID,
	))
}

func actionsForCurrentPhase(game domain.Game) []domain.GameAction {
	actions := make([]domain.GameAction, 0)
	for _, action := range game.Actions {
		if action.Phase == game.Phase && action.Round == game.Round {
			actions = append(actions, action)
		}
	}

	return actions
}

func isBlockedThisRound(game domain.Game, playerID string) bool {
	for _, action := range game.Actions {
		if action.Type == domain.GameActionBlock &&
			action.Round == game.Round &&
			action.TargetID == playerID {
			actor, ok := playerByID(game.Players, action.ActorID)
			if ok && actor.Role == domain.GameRoleMistress && actor.IsAlive {
				return true
			}
		}
	}
	return false
}

func topTarget(actions []domain.GameAction, actionType domain.GameActionType) (string, bool) {
	type tally struct {
		targetID string
		count    int
		firstAt  string
	}

	tallies := make(map[string]tally)
	for _, action := range actions {
		if action.Type != actionType {
			continue
		}

		current := tallies[action.TargetID]
		if current.targetID == "" {
			current.targetID = action.TargetID
			current.firstAt = action.CreatedAt
		}
		current.count++
		tallies[action.TargetID] = current
	}

	if len(tallies) == 0 {
		return "", false
	}

	ranked := make([]tally, 0, len(tallies))
	for _, current := range tallies {
		ranked = append(ranked, current)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].count != ranked[j].count {
			return ranked[i].count > ranked[j].count
		}

		return ranked[i].firstAt < ranked[j].firstAt
	})

	if len(ranked) > 1 && ranked[0].count == ranked[1].count {
		return "", false
	}

	return ranked[0].targetID, true
}

func maybeFinishGame(game *domain.Game) bool {
	mafiaAlive := 0
	townAlive := 0
	for _, player := range game.Players {
		if !player.IsAlive {
			continue
		}
		if sideForRole(player.Role) == domain.GameSideMafia {
			mafiaAlive++
		} else {
			townAlive++
		}
	}

	if mafiaAlive == 0 {
		game.Events = append(game.Events, newGameEvent("game.finished", "Мирні перемогли. Уся мафія вибула.", domain.GamePhaseFinal, game.Round, "", ""))
		return true
	}
	if mafiaAlive >= townAlive {
		game.Events = append(game.Events, newGameEvent("game.finished", "Мафія перемогла. Її вже не можна переголосувати.", domain.GamePhaseFinal, game.Round, "", ""))
		return true
	}
	return false
}

func setPlayerAlive(players []domain.GamePlayer, playerID string, isAlive bool) {
	for index := range players {
		if players[index].ID == playerID {
			players[index].IsAlive = isAlive
			return
		}
	}
}

func newGameEvent(eventType string, message string, phase domain.GamePhase, round int, actorID string, targetID string) domain.GameEvent {
	return domain.GameEvent{
		ID:        ids.NewID("event"),
		Type:      eventType,
		Message:   message,
		Phase:     phase,
		Round:     round,
		ActorID:   actorID,
		TargetID:  targetID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func inspectLabel(role domain.GameRole) string {
	if sideForRole(role) == domain.GameSideMafia {
		return "Мафія"
	}

	return "Мирний"
}

func sideForRole(role domain.GameRole) domain.GameSide {
	if role == domain.GameRoleMafia || role == domain.GameRoleMistress {
		return domain.GameSideMafia
	}
	return domain.GameSideTown
}

func inspectedTargetsBy(actions []domain.GameAction, actorID string) map[string]struct{} {
	targets := make(map[string]struct{})
	for _, action := range actions {
		if action.ActorID == actorID && action.Type == domain.GameActionInspect {
			targets[action.TargetID] = struct{}{}
		}
	}

	return targets
}

func visibleActions(actions []domain.GameAction, viewer domain.GamePlayer, revealAll bool) []domain.GameAction {
	if revealAll {
		return append([]domain.GameAction(nil), actions...)
	}

	visible := make([]domain.GameAction, 0, len(actions))
	for _, action := range actions {
		if action.Type == domain.GameActionVote ||
			action.ActorID == viewer.ID ||
			(viewer.Role == domain.GameRoleMafia && action.Type == domain.GameActionMafiaKill) {
			visible = append(visible, action)
		}
	}

	return visible
}

func visibleEvents(events []domain.GameEvent, viewer domain.GamePlayer, revealAll bool) []domain.GameEvent {
	if revealAll {
		return append([]domain.GameEvent(nil), events...)
	}

	visible := make([]domain.GameEvent, 0, len(events))
	for _, event := range events {
		if isVisibleEvent(event, viewer) {
			visible = append(visible, event)
		}
	}

	return visible
}

func isVisibleEvent(event domain.GameEvent, viewer domain.GamePlayer) bool {
	switch event.Type {
	case "inspect.resolved":
		return event.ActorID == viewer.ID
	case "action.recorded":
		if event.Phase == domain.GamePhaseVoting {
			return true
		}
		return event.ActorID == viewer.ID
	default:
		return true
	}
}
