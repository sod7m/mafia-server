CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    nickname TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS rooms (
    id TEXT PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    max_players INTEGER NOT NULL,
    owner_id TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS room_players (
    room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    nickname TEXT NOT NULL,
    is_owner BOOLEAN NOT NULL,
    seat_index INTEGER NOT NULL,
    PRIMARY KEY (room_id, user_id),
    UNIQUE (room_id, seat_index)
);

CREATE TABLE IF NOT EXISTS games (
    room_id TEXT PRIMARY KEY REFERENCES rooms(id) ON DELETE CASCADE,
    id TEXT NOT NULL UNIQUE,
    phase TEXT NOT NULL,
    step TEXT NOT NULL,
    round INTEGER NOT NULL,
    phase_started_at TEXT NOT NULL,
    phase_ends_at TEXT,
    phase_duration_seconds INTEGER NOT NULL,
    active_role TEXT,
    active_player_id TEXT,
    active_player_nickname TEXT,
    first_speaker_index INTEGER NOT NULL,
    speech_index INTEGER NOT NULL,
    started_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS game_players (
    room_id TEXT NOT NULL REFERENCES games(room_id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    nickname TEXT NOT NULL,
    is_owner BOOLEAN NOT NULL,
    role TEXT,
    side TEXT,
    is_alive BOOLEAN NOT NULL,
    seat_index INTEGER NOT NULL,
    PRIMARY KEY (room_id, user_id),
    UNIQUE (room_id, seat_index)
);

CREATE TABLE IF NOT EXISTS game_actions (
    id TEXT PRIMARY KEY,
    room_id TEXT NOT NULL REFERENCES games(room_id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    actor_nickname TEXT NOT NULL,
    target_id TEXT,
    target_nickname TEXT,
    phase TEXT NOT NULL,
    round INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    action_index INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS game_events (
    id TEXT PRIMARY KEY,
    room_id TEXT NOT NULL REFERENCES games(room_id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    message TEXT NOT NULL,
    phase TEXT NOT NULL,
    round INTEGER NOT NULL,
    actor_id TEXT,
    target_id TEXT,
    created_at TEXT NOT NULL,
    event_index INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS persistence_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_room_players_room_id ON room_players(room_id);
CREATE INDEX IF NOT EXISTS idx_game_players_room_id ON game_players(room_id);
CREATE INDEX IF NOT EXISTS idx_game_actions_room_idx ON game_actions(room_id, action_index);
CREATE INDEX IF NOT EXISTS idx_game_events_room_idx ON game_events(room_id, event_index);
