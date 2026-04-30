package games

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"mafia-server/internal/domain"
	"mafia-server/internal/ids"
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

var rolePattern = []domain.GameRole{
	domain.GameRoleCommissioner,
	domain.GameRoleMafia,
	domain.GameRoleDoctor,
	domain.GameRoleCivilian,
	domain.GameRoleCivilian,
	domain.GameRoleMafia,
	domain.GameRoleCivilian,
	domain.GameRoleCivilian,
}

type Service struct {
	mu     sync.RWMutex
	byRoom map[string]domain.Game
}

func NewService() *Service {
	return &Service{
		byRoom: make(map[string]domain.Game),
	}
}

func (s *Service) StartGame(room domain.Room) domain.Game {
	s.mu.Lock()
	defer s.mu.Unlock()

	if game, ok := s.byRoom[room.ID]; ok {
		game.Players = playersFromRoom(room)
		game.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		s.byRoom[room.ID] = game
		return cloneGame(game)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	game := domain.Game{
		ID:        ids.NewID("game"),
		RoomID:    room.ID,
		Phase:     domain.GamePhaseNight,
		Round:     1,
		Players:   playersFromRoom(room),
		StartedAt: now,
		UpdatedAt: now,
	}

	s.byRoom[room.ID] = game
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
		player := view.Players[index]
		if revealAll ||
			player.ID == viewerID ||
			(viewer.Role == domain.GameRoleMafia && player.Role == domain.GameRoleMafia) {
			continue
		}
		if _, inspected := inspectedTargets[player.ID]; inspected {
			continue
		}

		view.Players[index].Role = ""
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

	if phase != game.Phase {
		resolvePhase(&game)
	}

	if phase == domain.GamePhaseNight && game.Phase != domain.GamePhaseNight {
		game.Round++
	}

	game.Phase = phase
	if game.Phase != domain.GamePhaseFinal {
		maybeFinishGame(&game)
	}
	game.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.byRoom[roomID] = game
	return cloneGame(game), nil
}

func (s *Service) SubmitAction(roomID string, actorID string, actionType domain.GameActionType, targetID string) (domain.Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	if err := validateAction(game.Phase, actor.Role, actionType, actor.ID == target.ID); err != nil {
		return domain.Game{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	action := domain.GameAction{
		ID:             ids.NewID("action"),
		Type:           actionType,
		ActorID:        actor.ID,
		ActorNickname:  actor.Nickname,
		TargetID:       target.ID,
		TargetNickname: target.Nickname,
		Phase:          game.Phase,
		Round:          game.Round,
		CreatedAt:      now,
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
		CreatedAt: now,
	})
	if actionType == domain.GameActionInspect {
		game.Events = append(game.Events, domain.GameEvent{
			ID:        ids.NewID("event"),
			Type:      "inspect.resolved",
			Message:   fmt.Sprintf("Комісар перевірив %s: роль %s.", target.Nickname, roleLabel(target.Role)),
			Phase:     game.Phase,
			Round:     game.Round,
			ActorID:   actor.ID,
			TargetID:  target.ID,
			CreatedAt: now,
		})
	}
	game.UpdatedAt = now
	s.byRoom[roomID] = game
	return cloneGame(game), nil
}

func playersFromRoom(room domain.Room) []domain.GamePlayer {
	players := make([]domain.GamePlayer, 0, len(room.Players))
	for index, player := range room.Players {
		players = append(players, domain.GamePlayer{
			ID:       player.ID,
			Nickname: player.Nickname,
			IsOwner:  player.IsOwner,
			Role:     roleForIndex(index),
			IsAlive:  true,
		})
	}

	return players
}

func cloneGame(game domain.Game) domain.Game {
	game.Players = append([]domain.GamePlayer(nil), game.Players...)
	game.Actions = append([]domain.GameAction(nil), game.Actions...)
	game.Events = append([]domain.GameEvent(nil), game.Events...)
	return game
}

func roleForIndex(index int) domain.GameRole {
	if index < len(rolePattern) {
		return rolePattern[index]
	}

	return domain.GameRoleCivilian
}

func playerIndex(players []domain.GamePlayer, playerID string) int {
	for index, player := range players {
		if player.ID == playerID {
			return index
		}
	}

	return -1
}

func validateAction(phase domain.GamePhase, role domain.GameRole, actionType domain.GameActionType, isSelfTarget bool) error {
	switch actionType {
	case domain.GameActionMafiaKill:
		if phase != domain.GamePhaseNight || role != domain.GameRoleMafia || isSelfTarget {
			return ErrActionUnavailable
		}
	case domain.GameActionInspect:
		if phase != domain.GamePhaseNight || role != domain.GameRoleCommissioner || isSelfTarget {
			return ErrActionUnavailable
		}
	case domain.GameActionHeal:
		if phase != domain.GamePhaseNight || role != domain.GameRoleDoctor {
			return ErrActionUnavailable
		}
	case domain.GameActionVote:
		if phase != domain.GamePhaseVoting || isSelfTarget {
			return ErrActionUnavailable
		}
	default:
		return ErrInvalidAction
	}

	return nil
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

func resolvePhase(game *domain.Game) {
	switch game.Phase {
	case domain.GamePhaseNight:
		resolveNight(game)
	case domain.GamePhaseVoting:
		resolveVoting(game)
	}
}

func resolveNight(game *domain.Game) {
	actions := actionsForCurrentPhase(*game)
	healedTargets := make(map[string]struct{})
	for _, action := range actions {
		if action.Type == domain.GameActionHeal {
			healedTargets[action.TargetID] = struct{}{}
		}
	}

	targetID, ok := topTarget(actions, domain.GameActionMafiaKill)
	if !ok {
		game.Events = append(game.Events, newGameEvent("night.resolved", "Ніч минула без атаки мафії.", game.Phase, game.Round, "", ""))
		return
	}

	target, ok := playerByID(game.Players, targetID)
	if !ok {
		return
	}
	if _, healed := healedTargets[targetID]; healed {
		game.Events = append(game.Events, newGameEvent(
			"night.heal.resolved",
			"Цієї ночі нікого не вбито.",
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

func maybeFinishGame(game *domain.Game) {
	mafiaAlive := 0
	mafiaTotal := 0
	townAlive := 0
	for _, player := range game.Players {
		if player.Role == domain.GameRoleMafia {
			mafiaTotal++
		}
		if !player.IsAlive {
			continue
		}
		if player.Role == domain.GameRoleMafia {
			mafiaAlive++
		} else {
			townAlive++
		}
	}

	if mafiaTotal == 0 {
		return
	}
	if mafiaAlive == 0 {
		game.Phase = domain.GamePhaseFinal
		game.Events = append(game.Events, newGameEvent("game.finished", "Мирні перемогли. Уся мафія вибула.", game.Phase, game.Round, "", ""))
		return
	}
	if mafiaAlive >= townAlive {
		game.Phase = domain.GamePhaseFinal
		game.Events = append(game.Events, newGameEvent("game.finished", "Мафія перемогла. Її вже не можна переголосувати.", game.Phase, game.Round, "", ""))
	}
}

func playerByID(players []domain.GamePlayer, playerID string) (domain.GamePlayer, bool) {
	for _, player := range players {
		if player.ID == playerID {
			return player, true
		}
	}

	return domain.GamePlayer{}, false
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

func roleLabel(role domain.GameRole) string {
	switch role {
	case domain.GameRoleMafia:
		return "Мафія"
	case domain.GameRoleCommissioner:
		return "Комісар"
	case domain.GameRoleDoctor:
		return "Лікар"
	default:
		return "Мирний"
	}
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
