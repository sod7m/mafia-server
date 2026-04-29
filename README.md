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

Not implemented yet:
1. PostgreSQL persistence.
2. Full server-authoritative game engine.
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
- `POST /api/rooms/{roomId}/start`

### Games

- `GET /api/games/{roomId}`
- `POST /api/games/{roomId}/phase`

Current game snapshot starts with:

- `phase: night`
- `round: 1`
- room players copied into game players

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

### Realtime

- `GET /ws`

Events sent by server:

- `rooms.updated`
- `room.updated`
- `room.deleted`
- `game.updated` after room start and phase changes
