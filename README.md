# Mafia Server

Backend API for the Mafia game.

Current state:
1. Go HTTP server.
2. In-memory nickname sessions.
3. In-memory rooms.
4. HTTP API for lobby/room flow.
5. WebSocket broadcast for room updates.
6. In-memory game snapshot created on room start.
7. Server-side game phase switching for dev gameplay flow.
8. Owner-only room start and owner-only phase controls.
9. Server-side dev game actions for night moves and voting.
10. Per-player private game snapshots for roles, private actions and inspect results.
11. Server phase timer with fixed phase order and automatic phase advancement.

Not implemented yet:
1. PostgreSQL persistence.
2. Full production-grade server-authoritative game engine.
3. LiveKit voice/video token endpoint.

## Requirements

- Go 1.23+

## Run

```bash
go run ./cmd/api
```

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

## API

### Health

- `GET /healthz`

### Auth

- `POST /api/auth/login`
- `POST /api/auth/logout`
- `GET /api/me`

### Rooms

- `GET /api/rooms`
- `POST /api/rooms`
- `GET /api/rooms/{roomId}`
- `POST /api/rooms/{roomId}/join`
- `POST /api/rooms/{roomId}/leave`
- `POST /api/rooms/{roomId}/start` owner only

Starting a room requires at least 4 players in the current dev rules.

### Games

- `GET /api/games/{roomId}`
- `POST /api/games/{roomId}/phase` owner only
- `POST /api/games/{roomId}/next-phase` owner only
- `POST /api/games/{roomId}/actions`

`GET /api/games/{roomId}` returns a private view for the authenticated player:

- every player sees their own role
- mafia players see mafia teammates
- commissioner sees roles they have inspected
- other roles stay hidden until `final`
- private night actions and commissioner inspect results are not shown to unrelated players

Current game snapshot starts with:

- `phase: night`
- `round: 1`
- `phaseStartedAt`
- `phaseEndsAt`
- `phaseDurationSeconds`
- room players copied into game players
- deterministic dev roles assigned by seat order:
  - commissioner
  - mafia
  - doctor
  - civilians, with a second mafia at seat 6

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

Preferred phase flow:

- `night -> day -> voting -> night`
- `POST /api/games/{roomId}/next-phase` follows this order.
- The backend also advances expired phases automatically.

Default dev phase durations:

- `night`: 45 seconds
- `day`: 90 seconds
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
  - `mafia_kill` by mafia
  - `inspect` by commissioner
  - `heal` by doctor
- `voting`:
  - `vote` by alive players

Phase resolution:

- Leaving `night` resolves mafia kill, doctor heal and commissioner inspect events.
- Leaving `voting` resolves exile by vote majority.
- If all mafia are dead, the game moves to `final`.
- If mafia count is at least the alive town count, the game moves to `final`.

Current dev limitation: WebSocket `game.updated` is a room-level signal, not a personalized payload. Clients refresh `GET /api/games/{roomId}` after the signal to receive their private view.

### Realtime

- `GET /ws`

Events sent by server:

- `rooms.updated`
- `room.updated`
- `room.deleted`
- `game.updated` room-level signal after room start, phase changes and submitted actions
