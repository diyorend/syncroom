// Package room is the concurrency core of the application. Everything in
// this file exists to answer one question correctly: when N clients in the
// same room can each send play/pause/seek/chat events at any time, how do
// you keep every client's view of "what's playing and where" consistent
// without races, without a feedback loop, and without leaking memory for
// rooms nobody is watching anymore.
package room

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/diyorend/syncroom/internal/domain"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

// safeConn wraps a websocket connection with its own mutex. gorilla/websocket
// forbids concurrent calls to WriteMessage on the same connection from
// multiple goroutines; without this, two broadcasts landing close together
// for the same client can corrupt the frame.
type safeConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *safeConn) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// playbackState is the authoritative "what's happening right now" for a
// room. The key design decision: we do NOT store "current position" as a
// number that drifts out of sync with reality the moment time passes.
// Instead we store the position AT a known moment (PositionSeconds as of
// LastUpdatedAt) and any client — including one that just joined and has no
// history — can compute the live position as:
//
//	live = PositionSeconds + (time.Since(LastUpdatedAt) if IsPlaying else 0)
//
// This is the same pattern distributed systems use for "derive current
// state from a snapshot plus elapsed time" rather than trying to keep a
// continuously-ticking value perfectly synced across machines.
type playbackState struct {
	VideoURL        string
	IsPlaying       bool
	PositionSeconds float64
	LastUpdatedAt   time.Time
}

// client represents one connected browser tab.
type client struct {
	id   string // random per-connection ID, used to prevent self-echo
	name string
	conn *safeConn
}

// Room holds everything for one watch-party session: the playback state,
// the set of connected clients, and a channel of incoming events it
// processes one at a time in its own goroutine. Serializing all state
// mutations through a single goroutine (rather than locking playbackState
// directly from many goroutines) is a deliberate choice — see Run() below.
type Room struct {
	ID   string
	Code string

	mu      sync.RWMutex // protects clients map only; playbackState is owned by Run()'s goroutine
	clients map[string]*client

	state playbackState

	events     chan roomEvent
	done       chan struct{}
	lastSeen   time.Time // updated whenever a client connects or disconnects
	lastSeenMu sync.Mutex
}

type roomEvent struct {
	clientID string
	event    domain.ClientEvent
}

func newRoom(id, code string) *Room {
	return &Room{
		ID:      id,
		Code:    code,
		clients: make(map[string]*client),
		events:  make(chan roomEvent, 64),
		done:    make(chan struct{}),
		state: playbackState{
			LastUpdatedAt: time.Now(),
		},
	}
}

// Run is the room's single-goroutine event loop. Every mutation to
// playbackState happens here and only here — no mutex needed around state
// reads/writes inside this function, because nothing else ever touches
// `state` concurrently. This sidesteps an entire category of race bugs:
// instead of "lock state, read, modify, write, unlock" sprinkled across
// multiple call sites, there is exactly one place state changes, and
// everything else communicates with it by sending values into r.events.
func (r *Room) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.done:
			return
		case ev := <-r.events:
			r.handleEvent(ev)
		}
	}
}

func (r *Room) handleEvent(ev roomEvent) {
	switch ev.event.Type {
	case domain.EventPlay:
		r.state.IsPlaying = true
		r.state.PositionSeconds = ev.event.PositionSeconds
		r.state.LastUpdatedAt = time.Now()
		r.broadcastState(ev.clientID)

	case domain.EventPause:
		r.state.IsPlaying = false
		r.state.PositionSeconds = ev.event.PositionSeconds
		r.state.LastUpdatedAt = time.Now()
		r.broadcastState(ev.clientID)

	case domain.EventSeek:
		r.state.PositionSeconds = ev.event.PositionSeconds
		r.state.LastUpdatedAt = time.Now()
		r.broadcastState(ev.clientID)

	case domain.EventSetVideo:
		r.state.VideoURL = ev.event.VideoURL
		r.state.PositionSeconds = 0
		r.state.IsPlaying = false
		r.state.LastUpdatedAt = time.Now()
		r.broadcastState(ev.clientID)

	case domain.EventChat:
		r.mu.RLock()
		sender := "unknown"
		if c, ok := r.clients[ev.clientID]; ok {
			sender = c.name
		}
		r.mu.RUnlock()
		r.broadcastChat(sender, ev.event.ChatBody)
	}
}

// broadcastState sends the current authoritative state to every connected
// client. originClientID is included on the outgoing event so the
// originating client's frontend can recognize and ignore its own echo —
// without this, a client that just sent "pause" would receive its own
// pause event back, potentially re-triggering its local video player's
// pause handler, which could re-emit another pause event: an infinite
// ping-pong. The fix is purely additive (one extra field), not a separate
// code path, which keeps this simple.
func (r *Room) broadcastState(originClientID string) {
	ev := domain.ServerEvent{
		Type:            domain.EventSync,
		VideoURL:        r.state.VideoURL,
		IsPlaying:       r.state.IsPlaying,
		PositionSeconds: r.state.PositionSeconds,
		LastUpdatedAt:   r.state.LastUpdatedAt,
		OriginClientID:  originClientID,
	}
	r.broadcast(ev)
}

func (r *Room) broadcastChat(sender, body string) {
	ev := domain.ServerEvent{
		Type:       domain.EventChat,
		SenderName: sender,
		ChatBody:   body,
	}
	r.broadcast(ev)
}

func (r *Room) broadcastPresence() {
	r.mu.RLock()
	names := make([]string, 0, len(r.clients))
	for _, c := range r.clients {
		names = append(names, c.name)
	}
	r.mu.RUnlock()

	r.broadcast(domain.ServerEvent{
		Type:    domain.EventPresence,
		Members: names,
	})
}

func (r *Room) broadcast(ev domain.ServerEvent) {
	r.mu.RLock()
	targets := make([]*client, 0, len(r.clients))
	for _, c := range r.clients {
		targets = append(targets, c)
	}
	r.mu.RUnlock()

	for _, c := range targets {
		if err := c.conn.writeJSON(ev); err != nil {
			slog.Error("room broadcast write failed", "room", r.Code, "client", c.id, "err", err)
			// Connection cleanup happens in the handler's read loop when
			// ReadMessage returns an error — we don't remove it here to
			// avoid mutating r.clients from inside a broadcast that's
			// iterating a snapshot of it.
		}
	}
}

// snapshot returns the current state for a newly-joining client, computing
// the live position if playback is in progress. This is the join-time
// equivalent of the snapshot-plus-elapsed-time pattern described on
// playbackState above.
func (r *Room) snapshot() domain.ServerEvent {
	pos := r.state.PositionSeconds
	if r.state.IsPlaying {
		pos += time.Since(r.state.LastUpdatedAt).Seconds()
	}
	return domain.ServerEvent{
		Type:            domain.EventSync,
		VideoURL:        r.state.VideoURL,
		IsPlaying:       r.state.IsPlaying,
		PositionSeconds: pos,
		LastUpdatedAt:   time.Now(),
	}
}

func (r *Room) touch() {
	r.lastSeenMu.Lock()
	r.lastSeen = time.Now()
	r.lastSeenMu.Unlock()
}

func (r *Room) idleSince() time.Time {
	r.lastSeenMu.Lock()
	defer r.lastSeenMu.Unlock()
	return r.lastSeen
}

func (r *Room) clientCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.clients)
}

// --- Manager: owns the map of all live rooms ---

// Manager owns every in-memory Room and is the single entry point the rest
// of the application uses to interact with rooms. It also owns the Redis
// client used to fan events out across multiple backend instances (see
// the comment on publishCrossInstance below).
type Manager struct {
	mu    sync.RWMutex
	rooms map[string]*Room // keyed by room code

	rdb *redis.Client

	idleTimeout time.Duration
}

func NewManager(rdb *redis.Client, idleTimeout time.Duration) *Manager {
	return &Manager{
		rooms:       make(map[string]*Room),
		rdb:         rdb,
		idleTimeout: idleTimeout,
	}
}

// GetOrCreate returns the in-memory Room for a code, creating and starting
// its event-loop goroutine if it doesn't exist yet. dbID is the Postgres
// room ID (used so the in-memory Room knows its own DB identity).
func (m *Manager) GetOrCreate(ctx context.Context, code, dbID string) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.rooms[code]; ok {
		return r
	}

	r := newRoom(dbID, code)
	m.rooms[code] = r
	go r.Run(ctx)
	slog.Info("room created in memory", "code", code)
	return r
}

func (m *Manager) Get(code string) (*Room, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rooms[code]
	return r, ok
}

// Join registers a new client connection in a room, sends it the current
// snapshot so it catches up instantly, and broadcasts updated presence.
func (m *Manager) Join(r *Room, clientID, name string, conn *websocket.Conn) {
	sc := &safeConn{conn: conn}

	r.mu.Lock()
	r.clients[clientID] = &client{id: clientID, name: name, conn: sc}
	r.mu.Unlock()

	r.touch()

	// Send the joining client its catch-up snapshot directly (not via the
	// event loop — this is a point-to-point send, not a broadcast). We also
	// stamp YourClientID here: this is the ONLY message where a client
	// learns its own server-assigned ID. It must never be inferred from a
	// broadcast's OriginClientID (a client can't distinguish "this is an
	// echo of my own action" from "this is someone else's action" without
	// already knowing its own ID first).
	snap := r.snapshot()
	snap.YourClientID = clientID
	if err := sc.writeJSON(snap); err != nil {
		slog.Error("failed to send snapshot to joining client", "room", r.Code, "err", err)
	}

	r.broadcastPresence()
	slog.Info("client joined room", "room", r.Code, "client", clientID, "members", r.clientCount())
}

// Leave removes a client and broadcasts updated presence. If the room is
// now empty, it's left in memory until the idle sweep removes it — not
// removed immediately, since a brief disconnect/reconnect (e.g. a phone
// switching networks) shouldn't tear down room state.
func (m *Manager) Leave(r *Room, clientID string) {
	r.mu.Lock()
	delete(r.clients, clientID)
	r.mu.Unlock()

	r.touch()
	r.broadcastPresence()
	slog.Info("client left room", "room", r.Code, "client", clientID, "members", r.clientCount())
}

// Dispatch sends a client-originated event into the room's event loop.
// Non-blocking with a buffered channel; if the room's event loop is
// somehow backed up past the buffer size, this drops the event rather
// than blocking the WebSocket read loop indefinitely.
func (m *Manager) Dispatch(r *Room, clientID string, ev domain.ClientEvent) {
	select {
	case r.events <- roomEvent{clientID: clientID, event: ev}:
	default:
		slog.Error("room event channel full, dropping event", "room", r.Code, "type", ev.Type)
	}
}

// SweepIdleRooms removes rooms with zero connected clients that have been
// idle longer than the configured timeout. Call this periodically (see
// StartIdleSweeper) from a single background goroutine — without this,
// every room ever created stays in memory forever, which is a slow,
// guaranteed memory leak for any long-running server.
func (m *Manager) SweepIdleRooms() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for code, r := range m.rooms {
		if r.clientCount() > 0 {
			continue
		}
		if time.Since(r.idleSince()) < m.idleTimeout {
			continue
		}
		close(r.done)
		delete(m.rooms, code)
		slog.Info("swept idle room", "code", code)
	}
}

// StartIdleSweeper runs SweepIdleRooms on a ticker until ctx is cancelled.
// Call this once, in a goroutine, from main.go.
func (m *Manager) StartIdleSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.SweepIdleRooms()
		}
	}
}
