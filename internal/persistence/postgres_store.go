package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"mafia-server/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultDBTimeout     = 5 * time.Second
	defaultSchemaTimeout = 15 * time.Second
)

const (
	authSessionsKey = "auth.sessions"
	roomsStateKey   = "rooms.state"
	gamesStateKey   = "games.state"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	store := &PostgresStore{pool: pool}
	if err := store.prepareSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return store, nil
}

func (s *PostgresStore) Close() {
	s.pool.Close()
}

func (s *PostgresStore) Load(key string, target any) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDBTimeout)
	defer cancel()

	switch key {
	case authSessionsKey:
		typedTarget, ok := target.(*map[string]domain.UserSession)
		if !ok {
			return fmt.Errorf("load state %q: invalid target type %T", key, target)
		}

		sessions, err := s.loadAuthSessions(ctx)
		if errors.Is(err, ErrNotFound) {
			restored, fallbackErr := s.loadLegacySnapshot(ctx, key, typedTarget)
			if fallbackErr != nil {
				return fmt.Errorf("load state %q: %w", key, fallbackErr)
			}
			if restored {
				return nil
			}
		}
		if err != nil {
			return fmt.Errorf("load state %q: %w", key, err)
		}
		*typedTarget = sessions
		return nil
	case roomsStateKey:
		typedTarget, ok := target.(*map[string]domain.Room)
		if !ok {
			return fmt.Errorf("load state %q: invalid target type %T", key, target)
		}

		rooms, err := s.loadRooms(ctx)
		if errors.Is(err, ErrNotFound) {
			restored, fallbackErr := s.loadLegacySnapshot(ctx, key, typedTarget)
			if fallbackErr != nil {
				return fmt.Errorf("load state %q: %w", key, fallbackErr)
			}
			if restored {
				return nil
			}
		}
		if err != nil {
			return fmt.Errorf("load state %q: %w", key, err)
		}
		*typedTarget = rooms
		return nil
	case gamesStateKey:
		typedTarget, ok := target.(*map[string]domain.Game)
		if !ok {
			return fmt.Errorf("load state %q: invalid target type %T", key, target)
		}

		games, err := s.loadGames(ctx)
		if errors.Is(err, ErrNotFound) {
			restored, fallbackErr := s.loadLegacySnapshot(ctx, key, typedTarget)
			if fallbackErr != nil {
				return fmt.Errorf("load state %q: %w", key, fallbackErr)
			}
			if restored {
				return nil
			}
		}
		if err != nil {
			return fmt.Errorf("load state %q: %w", key, err)
		}
		*typedTarget = games
		return nil
	default:
		return fmt.Errorf("load state %q: unsupported state key", key)
	}
}

func (s *PostgresStore) Save(key string, value any) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDBTimeout)
	defer cancel()

	switch key {
	case authSessionsKey:
		sessions, ok := value.(map[string]domain.UserSession)
		if !ok {
			return fmt.Errorf("save state %q: invalid value type %T", key, value)
		}
		if err := s.saveAuthSessions(ctx, sessions); err != nil {
			return fmt.Errorf("save state %q: %w", key, err)
		}
	case roomsStateKey:
		rooms, ok := value.(map[string]domain.Room)
		if !ok {
			return fmt.Errorf("save state %q: invalid value type %T", key, value)
		}
		if err := s.saveRooms(ctx, rooms); err != nil {
			return fmt.Errorf("save state %q: %w", key, err)
		}
	case gamesStateKey:
		games, ok := value.(map[string]domain.Game)
		if !ok {
			return fmt.Errorf("save state %q: invalid value type %T", key, value)
		}
		if err := s.saveGames(ctx, games); err != nil {
			return fmt.Errorf("save state %q: %w", key, err)
		}
	default:
		return fmt.Errorf("save state %q: unsupported state key", key)
	}

	return nil
}

func (s *PostgresStore) prepareSchema(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, defaultSchemaTimeout)
	defer cancel()

	if err := runMigrations(ctx, s.pool); err != nil {
		return fmt.Errorf("prepare postgres schema: %w", err)
	}

	return nil
}

func (s *PostgresStore) loadAuthSessions(ctx context.Context) (map[string]domain.UserSession, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.token, u.id, u.nickname
		FROM sessions s
		JOIN users u ON u.id = s.user_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make(map[string]domain.UserSession)
	for rows.Next() {
		var token string
		var user domain.UserSession
		if err := rows.Scan(&token, &user.ID, &user.Nickname); err != nil {
			return nil, err
		}
		sessions[token] = user
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(sessions) == 0 {
		if err := s.ensureKeyWasPersisted(ctx, authSessionsKey); err != nil {
			return nil, err
		}
	}

	return sessions, nil
}

func (s *PostgresStore) loadRooms(ctx context.Context) (map[string]domain.Room, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, status, max_players, owner_id, created_at
		FROM rooms
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rooms := make(map[string]domain.Room)
	for rows.Next() {
		var room domain.Room
		var status string
		if err := rows.Scan(
			&room.ID,
			&room.Code,
			&room.Name,
			&status,
			&room.MaxPlayers,
			&room.OwnerID,
			&room.CreatedAt,
		); err != nil {
			return nil, err
		}
		room.Status = domain.RoomStatus(status)
		room.Players = make([]domain.RoomPlayer, 0)
		rooms[room.ID] = room
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(rooms) == 0 {
		if err := s.ensureKeyWasPersisted(ctx, roomsStateKey); err != nil {
			return nil, err
		}
		return rooms, nil
	}

	playerRows, err := s.pool.Query(ctx, `
		SELECT room_id, user_id, nickname, is_owner
		FROM room_players
		ORDER BY room_id, seat_index
	`)
	if err != nil {
		return nil, err
	}
	defer playerRows.Close()

	for playerRows.Next() {
		var roomID string
		var player domain.RoomPlayer
		if err := playerRows.Scan(&roomID, &player.ID, &player.Nickname, &player.IsOwner); err != nil {
			return nil, err
		}

		room, ok := rooms[roomID]
		if !ok {
			continue
		}
		room.Players = append(room.Players, player)
		rooms[roomID] = room
	}
	if err := playerRows.Err(); err != nil {
		return nil, err
	}

	return rooms, nil
}

func (s *PostgresStore) loadGames(ctx context.Context) (map[string]domain.Game, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			room_id,
			id,
			phase,
			step,
			round,
			phase_started_at,
			phase_ends_at,
			phase_duration_seconds,
			active_role,
			active_player_id,
			active_player_nickname,
			first_speaker_index,
			speech_index,
			started_at,
			updated_at
		FROM games
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	games := make(map[string]domain.Game)
	for rows.Next() {
		var game domain.Game
		var phase string
		var step string
		var phaseEndsAt *string
		var activeRole *string
		var activePlayerID *string
		var activePlayerNickname *string
		if err := rows.Scan(
			&game.RoomID,
			&game.ID,
			&phase,
			&step,
			&game.Round,
			&game.PhaseStartedAt,
			&phaseEndsAt,
			&game.PhaseDurationSeconds,
			&activeRole,
			&activePlayerID,
			&activePlayerNickname,
			&game.FirstSpeakerIndex,
			&game.SpeechIndex,
			&game.StartedAt,
			&game.UpdatedAt,
		); err != nil {
			return nil, err
		}

		game.Phase = domain.GamePhase(phase)
		game.Step = domain.GameStep(step)
		if phaseEndsAt != nil {
			game.PhaseEndsAt = *phaseEndsAt
		}
		if activeRole != nil {
			game.ActiveRole = domain.GameRole(*activeRole)
		}
		if activePlayerID != nil {
			game.ActivePlayerID = *activePlayerID
		}
		if activePlayerNickname != nil {
			game.ActivePlayerNickname = *activePlayerNickname
		}

		game.Players = make([]domain.GamePlayer, 0)
		game.Actions = make([]domain.GameAction, 0)
		game.Events = make([]domain.GameEvent, 0)
		games[game.RoomID] = game
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(games) == 0 {
		if err := s.ensureKeyWasPersisted(ctx, gamesStateKey); err != nil {
			return nil, err
		}
		return games, nil
	}

	if err := s.loadGamePlayers(ctx, games); err != nil {
		return nil, err
	}
	if err := s.loadGameActions(ctx, games); err != nil {
		return nil, err
	}
	if err := s.loadGameEvents(ctx, games); err != nil {
		return nil, err
	}

	return games, nil
}

func (s *PostgresStore) loadGamePlayers(ctx context.Context, games map[string]domain.Game) error {
	rows, err := s.pool.Query(ctx, `
		SELECT room_id, user_id, nickname, is_owner, role, side, is_alive
		FROM game_players
		ORDER BY room_id, seat_index
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var roomID string
		var player domain.GamePlayer
		var role *string
		var side *string
		if err := rows.Scan(
			&roomID,
			&player.ID,
			&player.Nickname,
			&player.IsOwner,
			&role,
			&side,
			&player.IsAlive,
		); err != nil {
			return err
		}

		if role != nil {
			player.Role = domain.GameRole(*role)
		}
		if side != nil {
			player.Side = domain.GameSide(*side)
		}

		game, ok := games[roomID]
		if !ok {
			continue
		}
		game.Players = append(game.Players, player)
		games[roomID] = game
	}

	return rows.Err()
}

func (s *PostgresStore) loadGameActions(ctx context.Context, games map[string]domain.Game) error {
	rows, err := s.pool.Query(ctx, `
		SELECT
			room_id,
			id,
			type,
			actor_id,
			actor_nickname,
			target_id,
			target_nickname,
			phase,
			round,
			created_at
		FROM game_actions
		ORDER BY room_id, action_index
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var roomID string
		var action domain.GameAction
		var actionType string
		var phase string
		var targetID *string
		var targetNickname *string
		if err := rows.Scan(
			&roomID,
			&action.ID,
			&actionType,
			&action.ActorID,
			&action.ActorNickname,
			&targetID,
			&targetNickname,
			&phase,
			&action.Round,
			&action.CreatedAt,
		); err != nil {
			return err
		}

		action.Type = domain.GameActionType(actionType)
		action.Phase = domain.GamePhase(phase)
		if targetID != nil {
			action.TargetID = *targetID
		}
		if targetNickname != nil {
			action.TargetNickname = *targetNickname
		}

		game, ok := games[roomID]
		if !ok {
			continue
		}
		game.Actions = append(game.Actions, action)
		games[roomID] = game
	}

	return rows.Err()
}

func (s *PostgresStore) loadGameEvents(ctx context.Context, games map[string]domain.Game) error {
	rows, err := s.pool.Query(ctx, `
		SELECT
			room_id,
			id,
			type,
			message,
			phase,
			round,
			actor_id,
			target_id,
			created_at
		FROM game_events
		ORDER BY room_id, event_index
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var roomID string
		var event domain.GameEvent
		var phase string
		var actorID *string
		var targetID *string
		if err := rows.Scan(
			&roomID,
			&event.ID,
			&event.Type,
			&event.Message,
			&phase,
			&event.Round,
			&actorID,
			&targetID,
			&event.CreatedAt,
		); err != nil {
			return err
		}

		event.Phase = domain.GamePhase(phase)
		if actorID != nil {
			event.ActorID = *actorID
		}
		if targetID != nil {
			event.TargetID = *targetID
		}

		game, ok := games[roomID]
		if !ok {
			continue
		}
		game.Events = append(game.Events, event)
		games[roomID] = game
	}

	return rows.Err()
}

func (s *PostgresStore) saveAuthSessions(ctx context.Context, sessions map[string]domain.UserSession) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM sessions`); err != nil {
			return err
		}

		for token, user := range sessions {
			if _, err := tx.Exec(
				ctx,
				`INSERT INTO users (id, nickname)
				 VALUES ($1, $2)
				 ON CONFLICT (id) DO UPDATE SET nickname = EXCLUDED.nickname`,
				user.ID,
				user.Nickname,
			); err != nil {
				return err
			}

			if _, err := tx.Exec(
				ctx,
				`INSERT INTO sessions (token, user_id) VALUES ($1, $2)`,
				token,
				user.ID,
			); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(
			ctx,
			`DELETE FROM users u WHERE NOT EXISTS (SELECT 1 FROM sessions s WHERE s.user_id = u.id)`,
		); err != nil {
			return err
		}

		return s.setPersistedKey(ctx, tx, authSessionsKey)
	})
}

func (s *PostgresStore) saveRooms(ctx context.Context, rooms map[string]domain.Room) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		existing, err := fetchStringSet(ctx, tx, `SELECT id FROM rooms`)
		if err != nil {
			return err
		}
		for roomID := range existing {
			if _, ok := rooms[roomID]; ok {
				continue
			}
			if _, err := tx.Exec(ctx, `DELETE FROM rooms WHERE id = $1`, roomID); err != nil {
				return err
			}
		}

		for _, room := range rooms {
			if _, err := tx.Exec(
				ctx,
				`INSERT INTO rooms (id, code, name, status, max_players, owner_id, created_at)
				 VALUES ($1, $2, $3, $4, $5, $6, $7)
				 ON CONFLICT (id) DO UPDATE
				 SET code = EXCLUDED.code,
					 name = EXCLUDED.name,
					 status = EXCLUDED.status,
					 max_players = EXCLUDED.max_players,
					 owner_id = EXCLUDED.owner_id,
					 created_at = EXCLUDED.created_at`,
				room.ID,
				room.Code,
				room.Name,
				string(room.Status),
				room.MaxPlayers,
				room.OwnerID,
				room.CreatedAt,
			); err != nil {
				return err
			}

			if _, err := tx.Exec(ctx, `DELETE FROM room_players WHERE room_id = $1`, room.ID); err != nil {
				return err
			}
			for index, player := range room.Players {
				if _, err := tx.Exec(
					ctx,
					`INSERT INTO room_players (room_id, user_id, nickname, is_owner, seat_index)
					 VALUES ($1, $2, $3, $4, $5)`,
					room.ID,
					player.ID,
					player.Nickname,
					player.IsOwner,
					index,
				); err != nil {
					return err
				}
			}
		}

		return s.setPersistedKey(ctx, tx, roomsStateKey)
	})
}

func (s *PostgresStore) saveGames(ctx context.Context, games map[string]domain.Game) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		existing, err := fetchStringSet(ctx, tx, `SELECT room_id FROM games`)
		if err != nil {
			return err
		}
		for roomID := range existing {
			if _, ok := games[roomID]; ok {
				continue
			}
			if _, err := tx.Exec(ctx, `DELETE FROM games WHERE room_id = $1`, roomID); err != nil {
				return err
			}
		}

		for _, game := range games {
			if _, err := tx.Exec(
				ctx,
				`INSERT INTO games (
					room_id,
					id,
					phase,
					step,
					round,
					phase_started_at,
					phase_ends_at,
					phase_duration_seconds,
					active_role,
					active_player_id,
					active_player_nickname,
					first_speaker_index,
					speech_index,
					started_at,
					updated_at
				) VALUES (
					$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
				)
				ON CONFLICT (room_id) DO UPDATE
				SET id = EXCLUDED.id,
					phase = EXCLUDED.phase,
					step = EXCLUDED.step,
					round = EXCLUDED.round,
					phase_started_at = EXCLUDED.phase_started_at,
					phase_ends_at = EXCLUDED.phase_ends_at,
					phase_duration_seconds = EXCLUDED.phase_duration_seconds,
					active_role = EXCLUDED.active_role,
					active_player_id = EXCLUDED.active_player_id,
					active_player_nickname = EXCLUDED.active_player_nickname,
					first_speaker_index = EXCLUDED.first_speaker_index,
					speech_index = EXCLUDED.speech_index,
					started_at = EXCLUDED.started_at,
					updated_at = EXCLUDED.updated_at`,
				game.RoomID,
				game.ID,
				string(game.Phase),
				string(game.Step),
				game.Round,
				game.PhaseStartedAt,
				nullableText(game.PhaseEndsAt),
				game.PhaseDurationSeconds,
				nullableText(string(game.ActiveRole)),
				nullableText(game.ActivePlayerID),
				nullableText(game.ActivePlayerNickname),
				game.FirstSpeakerIndex,
				game.SpeechIndex,
				game.StartedAt,
				game.UpdatedAt,
			); err != nil {
				return err
			}

			if _, err := tx.Exec(ctx, `DELETE FROM game_players WHERE room_id = $1`, game.RoomID); err != nil {
				return err
			}
			for index, player := range game.Players {
				if _, err := tx.Exec(
					ctx,
					`INSERT INTO game_players (
						room_id, user_id, nickname, is_owner, role, side, is_alive, seat_index
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
					game.RoomID,
					player.ID,
					player.Nickname,
					player.IsOwner,
					nullableText(string(player.Role)),
					nullableText(string(player.Side)),
					player.IsAlive,
					index,
				); err != nil {
					return err
				}
			}

			if _, err := tx.Exec(ctx, `DELETE FROM game_actions WHERE room_id = $1`, game.RoomID); err != nil {
				return err
			}
			for index, action := range game.Actions {
				if _, err := tx.Exec(
					ctx,
					`INSERT INTO game_actions (
						id,
						room_id,
						type,
						actor_id,
						actor_nickname,
						target_id,
						target_nickname,
						phase,
						round,
						created_at,
						action_index
					) VALUES (
						$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
					)`,
					action.ID,
					game.RoomID,
					string(action.Type),
					action.ActorID,
					action.ActorNickname,
					nullableText(action.TargetID),
					nullableText(action.TargetNickname),
					string(action.Phase),
					action.Round,
					action.CreatedAt,
					index,
				); err != nil {
					return err
				}
			}

			if _, err := tx.Exec(ctx, `DELETE FROM game_events WHERE room_id = $1`, game.RoomID); err != nil {
				return err
			}
			for index, event := range game.Events {
				if _, err := tx.Exec(
					ctx,
					`INSERT INTO game_events (
						id,
						room_id,
						type,
						message,
						phase,
						round,
						actor_id,
						target_id,
						created_at,
						event_index
					) VALUES (
						$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
					)`,
					event.ID,
					game.RoomID,
					event.Type,
					event.Message,
					string(event.Phase),
					event.Round,
					nullableText(event.ActorID),
					nullableText(event.TargetID),
					event.CreatedAt,
					index,
				); err != nil {
					return err
				}
			}
		}

		return s.setPersistedKey(ctx, tx, gamesStateKey)
	})
}

func (s *PostgresStore) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	return tx.Commit(ctx)
}

func (s *PostgresStore) ensureKeyWasPersisted(ctx context.Context, key string) error {
	persisted, err := s.isKeyPersisted(ctx, key)
	if err != nil {
		return err
	}
	if !persisted {
		return ErrNotFound
	}

	return nil
}

func (s *PostgresStore) isKeyPersisted(ctx context.Context, key string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM persistence_meta WHERE key = $1)`,
		key,
	).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *PostgresStore) setPersistedKey(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(
		ctx,
		`INSERT INTO persistence_meta (key, value)
		 VALUES ($1, '1')
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key,
	)
	return err
}

func fetchStringSet(ctx context.Context, tx pgx.Tx, query string) (map[string]struct{}, error) {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]struct{})
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result[value] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func nullableText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (s *PostgresStore) loadLegacySnapshot(ctx context.Context, key string, target any) (bool, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `SELECT payload FROM state_snapshots WHERE key = $1`, key).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if err := json.Unmarshal(payload, target); err != nil {
		return false, err
	}

	return true, nil
}
