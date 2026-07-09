# SyncRoom

Watch YouTube videos in sync with anyone. Paste a YouTube link into a room, and every connected client stays frame-perfectly in sync — play, pause, or seek from any client and the change propagates in real time.

## Features

- Create a room, get a shareable code (e.g. `BLUE-FOX-42`)
- Anyone with the code can join — no account required to join, only to create
- YouTube video synced in real time across all clients (play / pause / seek)
- Set a new video URL at any time from any client
- Live chat panel with history persisted to Postgres
- Real-time presence list (who's in the room right now)
- Idle rooms automatically cleaned up from memory after 10 minutes

## Stack

**Backend:** Go 1.26 · Echo · pgx v5 · Redis · gorilla/websocket · JWT · bcrypt · `log/slog`
**Frontend:** React 18 · TypeScript · Vite · Bun · YouTube IFrame API · react-hot-toast
**Infrastructure:** Docker (multi-stage) · Docker Compose · Nginx · PostgreSQL 16 · Redis 7

## Architecture

```
Browser                    Go Backend                     Postgres / Redis
  │                            │
  ├─── POST /api/rooms ────►  RoomHandler.Create()  ──►  INSERT rooms
  ├─── GET  /api/rooms/:code ► RoomHandler.Get()    ──►  SELECT + chat history
  │
  └─── WS  /ws/:code ──────►  WSHandler.Connect()
                                   │
                               Manager.GetOrCreate()   (in-memory Room)
                               Manager.Join()           sends snapshot
                                   │
                            ┌──── Room.Run() ─────┐    goroutine per room
                            │  event loop          │    serialises all mutations
                            │  play/pause/seek  ──►│──► broadcast to all clients
                            │  chat  ──────────────│──► persist to Postgres
                            └──────────────────────┘
                                   │
                            Manager.StartIdleSweeper()  background goroutine
                                   removes empty rooms after 10 min
```

## Key design decisions

**One goroutine per room, event loop pattern.**
All playback state mutations happen inside `Room.Run()` — a single goroutine reading from a buffered channel. No mutex needed around state reads/writes inside the loop. Every other goroutine (HTTP handlers, WebSocket read loops) sends values into the channel and never touches state directly. This eliminates an entire class of race bugs that would appear if multiple goroutines modified the same struct with fine-grained locking.

**Snapshot + elapsed time for playback position.**
The server stores `(position_seconds, is_playing, last_updated_at)` — not a continuously-ticking counter. Any client that joins computes its catch-up position as `position_seconds + elapsed_since(last_updated_at)` if playing. This means new joiners are immediately in sync without replaying any event history, and there's no server-side timer to drift.

**Self-echo prevention via `origin_client_id`.**
Every broadcast includes the ID of the client that triggered it. Each client ignores sync events that originated from itself — without this, a client that seeks would receive its own seek event back, re-trigger its player's seek handler, which would emit another seek event: an infinite loop. The fix is one field on the event struct, not a separate code path.

**Idle room cleanup.**
`Manager.StartIdleSweeper` runs every 2 minutes on a `time.Ticker`. Rooms with zero connected clients that haven't been touched in the configured idle timeout are removed from the in-memory map and their goroutines are stopped by closing the `done` channel. Without this, every created room stays in memory forever — a guaranteed slow memory leak.

**Guests can join without an account.**
`OptionalJWT` middleware is applied on the WebSocket route: if a valid token is present it sets the user's email as their display name; if not, it falls through to a `?name=` query param. This is different from the budget tracker's mandatory-JWT pattern because here the shareable link is the access control — anyone with the room code is welcome.

## API

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/health` | No | DB health check |
| `POST` | `/api/auth/register` | No | `{email, password}` → `{token, user}` |
| `POST` | `/api/auth/login` | No | `{email, password}` → `{token, user}` |
| `POST` | `/api/rooms` | Required | Creates a room, returns `{id, code, ...}` |
| `GET` | `/api/rooms/:code` | No | Returns `{room, messages}` — room state + chat history |
| `GET` | `/ws/:code` | Optional (`?token=`) | WebSocket. `?name=` for guest display name |

### WebSocket events

**Client → Server:**
```json
{ "type": "play",      "position_seconds": 42.5 }
{ "type": "pause",     "position_seconds": 42.5 }
{ "type": "seek",      "position_seconds": 120.0 }
{ "type": "set_video", "video_url": "https://youtube.com/watch?v=..." }
{ "type": "chat",      "chat_body": "hello!" }
```

**Server → Client:**
```json
{ "type": "sync",     "video_url": "...", "is_playing": true, "position_seconds": 42.5, "last_updated_at": "...", "origin_client_id": "abc123" }
{ "type": "presence", "members": ["alice@example.com", "Bob"] }
{ "type": "chat",     "sender_name": "alice@example.com", "chat_body": "hello!" }
```

## Running locally

```bash
# First run — generate bun.lock
cd frontend && bun install && cd ..

docker compose up --build
# Visit http://localhost
```

## Running without Docker

```bash
docker run -d -p 5432:5432 \
  -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=syncroom \
  postgres:16-alpine

docker run -d -p 6379:6379 redis:7-alpine

# Apply migrations
for f in backend/migrations/*.sql; do
  docker exec -i <postgres-id> psql -U postgres -d syncroom < "$f"
done

cd backend
cp .env.example .env
go run ./cmd/server/main.go

cd frontend
bun install && bun run dev
```

## Testing

```bash
cd backend
go test ./...
```

Tests cover: snapshot position math (paused vs playing), idle room cleanup timing, the full WebSocket connect → event → broadcast loop using `httptest` + real WebSocket connections, and the self-echo `origin_client_id` field.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Backend listen port |
| `DATABASE_URL` | required | Postgres DSN |
| `REDIS_URL` | `redis://localhost:6379` | Redis DSN |
| `JWT_SECRET` | required | 32+ char signing secret |
| `JWT_EXPIRY_HOURS` | `24` | Token lifetime |
| `ROOM_IDLE_TIMEOUT_MINUTES` | `10` | Minutes before an empty room is swept from memory |
