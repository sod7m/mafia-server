# Mafia Server

Backend API for the Mafia game.

Current state:
1. Go HTTP server.
2. Nickname sessions with optional PostgreSQL persistence.
3. Rooms with optional PostgreSQL persistence.
4. HTTP API for lobby/room flow.
5. WebSocket broadcast for room updates.
6. Game snapshot created on room start, with optional PostgreSQL persistence.
7. Server-side game step switching for night roles, day speeches, discussion and voting.
8. Owner-only room start and owner-only phase controls.
9. Server-side dev game actions for night moves and voting.
10. Per-player private game snapshots for roles, private actions and inspect results.
11. Server step timer with fixed order and automatic step advancement.
12. Environment-driven CORS/WS allowlists and rate limits for login/room mutations.
13. Recovery endpoint for reconnect/reload (`GET /api/recovery`).

Not implemented yet:
1. LiveKit voice/video token endpoint.

## Requirements

- Go 1.24+

## Run

```bash
go run ./cmd/api
```

## Local PostgreSQL via Docker

Повний beginner-friendly гайд також є тут:
`F:\mafia-project\docs\local_postgres_for_newbies.md`

### Простими словами (для новачка)

1. У грі **немає реєстрації акаунта з паролем**.  
   Користувач просто вводить nickname і отримує токен сесії (`/api/auth/login`).
2. `POSTGRES_PASSWORD` — це **пароль до бази даних PostgreSQL**, а не пароль гравця.
3. `.env` — локальний файл з налаштуваннями/секретами для твого ПК.  
   Його не треба комітити в git (він уже в `.gitignore`).
4. `DATABASE_URL` — це рядок підключення до БД.  
   Якщо він заданий, сервер зберігає стан у PostgreSQL.  
   Якщо порожній — сервер працює як раніше, повністю in-memory.

### Швидкий старт (Windows + Docker) — копіюй по кроках

1. Створи локальний `.env` з шаблону:

```powershell
Copy-Item .env.example .env
```

2. Відкрий `.\.env` і зміни тільки пароль БД, наприклад:

```env
POSTGRES_PASSWORD=my_strong_local_password_123
```

3. Підніми PostgreSQL у Docker:

```powershell
docker compose up -d postgres
```

4. Завантаж змінні з `.env` у поточний PowerShell:

```powershell
Get-Content .env | Where-Object { $_ -notmatch '^\s*#' -and $_ -notmatch '^\s*$' } | ForEach-Object {
  $parts = $_ -split '=', 2
  if ($parts.Length -eq 2) { Set-Item -Path ("Env:" + $parts[0]) -Value $parts[1] }
}
```

5. Запусти backend:

```powershell
go run ./cmd/api
```

6. Зупинити PostgreSQL пізніше:

```powershell
docker compose down
```

`DATABASE_URL` вмикає збереження стану в нормалізованих PostgreSQL таблицях (`users`, `sessions`, `rooms`, `room_players`, `games`, `game_players`, `game_actions`, `game_events`) з авто-міграціями.  
Без `DATABASE_URL` сервер працює в old-school in-memory режимі.

### Що саме зберігається в PostgreSQL зараз

1. Сесії (`auth`)
2. Кімнати (`rooms`)
3. Ігрові стани (`games`)

Дані зберігаються у нормалізованій схемі + міграціях, без JSON snapshot-таблиці як основного формату.

### Security env (важливо)

У `.env` тепер є додаткові параметри безпеки:

1. `CORS_ALLOWED_ORIGINS` — дозволені Origin для HTTP API (через кому).
2. `WS_ALLOWED_ORIGINS` — allowlist для WebSocket OriginPatterns (через кому).
3. `RATE_LIMIT_LOGIN_PER_MINUTE` — ліміт запитів на `POST /api/auth/login`.
4. `RATE_LIMIT_ROOM_MUTATION_PER_MINUTE` — ліміт запитів на `POST /api/rooms*`.

Default address:

```text
http://localhost:8080
```

You can override it:

```bash
HTTP_ADDR=:8081 go run ./cmd/api
```

On Windows PowerShell:

```powershell
$env:HTTP_ADDR=":8081"
$env:GOTELEMETRY="off"
go run ./cmd/api
```

## API Documentation (Swagger UI)

**Interactive API documentation available at:**

```
http://localhost:8080/api/docs
```

All endpoints fully documented with:
- Request/response examples
- Parameter descriptions
- Error codes
- Authentication requirements
- Try-it-out functionality (test requests directly in browser)

Local Windows Application Control can block freshly built `.exe` files in this workspace. If that happens, use the devserver test runner:

```powershell
$env:HTTP_ADDR="127.0.0.1:8080"
$env:GOTELEMETRY="off"
$env:GOTELEMETRYDIR="F:\mafia-project\mafia-server\.gotelemetry"
$env:GOCACHE="F:\mafia-project\mafia-server\.gocache"
$env:GOTMPDIR="F:\mafia-project\mafia-server\.gotmp"
go test -tags devserver -run TestDevServer -timeout 0 ./cmd/api
```

This runner is only included when the `devserver` build tag is set.

## Smoke Test

Health:

```bash
curl http://localhost:8080/healthz
```

Login:

```bash
curl -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d "{\"nickname\":\"DonVito\"}"
```

List rooms:

```bash
curl http://localhost:8080/api/rooms
```

Create room:

```bash
curl -X POST http://localhost:8080/api/rooms \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <TOKEN>" \
  -d "{\"name\":\"Night table\",\"maxPlayers\":10}"
```

## API Endpoints

**See full API documentation with examples and try-it-out at:**
```
http://localhost:8080/api/docs
```

Quick reference:

### Auth
- `POST /api/auth/login` — Create/get user by nickname
- `POST /api/auth/logout` — Revoke token
- `GET /api/me` — Get current user info
- `GET /api/recovery` — Restore rooms/games for current token after reconnect/reload

### Rooms
- `GET /api/rooms` — List available rooms
- `POST /api/rooms` — Create new room
- `GET /api/rooms/{roomId}` — Get room details
- `POST /api/rooms/{roomId}/join` — Join room
- `POST /api/rooms/{roomId}/leave` — Leave room
- `POST /api/rooms/{roomId}/start` — Start game (owner only)

### Games
- `GET /api/games/{roomId}` — Get game state (personalized)
- `POST /api/games/{roomId}/phase` — Set phase (owner only)
- `POST /api/games/{roomId}/next-phase` — Advance phase (owner only)
- `POST /api/games/{roomId}/actions` — Submit night action/vote

**Minimum players to start room:** 6

**Phase flow:** night → day → voting → (repeat or final)

`GET /api/games/{roomId}` returns a private view for the authenticated player:

- every player sees their own role
- mafia players see ordinary mafia teammates, but not the mistress
- commissioner sees inspected player side only: town or mafia
- other roles stay hidden until `final`
- private night actions and commissioner inspect results are not shown to unrelated players

Current game snapshot starts with:

- `phase: night`
- `step: night_mistress`
- `round: 1`
- `phaseStartedAt`
- `phaseEndsAt`
- `phaseDurationSeconds`
- room players copied into game players
- deterministic dev roles assigned by seat order:
  - commissioner
  - mafia
  - doctor
  - mistress
  - civilians, with a second ordinary mafia at seat 7

Change phase:

```bash
curl -X POST http://localhost:8080/api/games/<ROOM_ID>/phase \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <TOKEN>" \
  -d "{\"phase\":\"day\"}"
```

Allowed phases:

- `night`
- `day`
- `voting`
- `final`

Switching from a non-night phase back to `night` increments the game round.

Preferred step flow:

- `night_mistress -> night_doctor -> night_commissioner -> night_mafia`
- `day_speech` once per alive player, rotating first speaker by round
- `day_discussion -> voting -> night_mistress`
- `POST /api/games/{roomId}/next-phase` advances the current step.
- The backend also advances expired steps automatically.

Default step durations:

- `night_mistress`: 15 seconds
- `night_doctor`: 15 seconds
- `night_commissioner`: 15 seconds
- `night_mafia`: 30 seconds
- `day_speech`: 60 seconds per alive speaker
- `day_discussion`: 90 seconds
- `voting`: 35 seconds
- `final`: no timer

Submit action:

```bash
curl -X POST http://localhost:8080/api/games/<ROOM_ID>/actions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <TOKEN>" \
  -d "{\"type\":\"vote\",\"targetId\":\"<PLAYER_ID>\"}"
```

Allowed action flow:

- `night`:
  - `mistress_block` by mistress during `night_mistress`
  - `heal` by doctor during `night_doctor`
  - `inspect` by commissioner during `night_commissioner`
  - `mafia_kill` by mafia
- `voting`:
  - `vote` by alive players

Phase resolution:

- Leaving `night` resolves mistress block, doctor heal and mafia shots.
- Leaving `voting` resolves exile by vote majority.
- Mafia kill succeeds only when all alive unblocked ordinary mafia choose the same target (one unblocked mafia is enough).
- If all mafia-side players are dead, the game moves to `final`.
- If mafia-side count is at least the alive town count, the game moves to `final`.

Current dev limitation: WebSocket `game.updated` is a room-level signal, not a personalized payload. Clients refresh `GET /api/games/{roomId}` after the signal to receive their private view.

### Realtime

- `GET /ws`

Events sent by server:

- `rooms.updated`
- `room.updated`
- `room.deleted`
- `game.updated` room-level signal after room start, phase changes and submitted actions
