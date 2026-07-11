package room

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/diyorend/syncroom/internal/domain"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

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

type playbackState struct {
	VideoURL        string
	IsPlaying       bool
	PositionSeconds float64
	LastUpdatedAt   time.Time
}

type client struct {
	id   string
	name string
	conn *safeConn
}

func crossInstanceChannel(code string) string {
	return "syncroom:sync:" + code
}

type pubSubEnvelope struct {
	Code       string             `json:"code"`
	Event      domain.ServerEvent `json:"event"`
	InstanceID string             `json:"instance_id"`
}

type Room struct {
	ID   string
	Code string

	mu      sync.RWMutex
	clients map[string]*client

	state playbackState

	rdb *redis.Client

	instanceID string

	events     chan roomEvent
	remote     chan domain.ServerEvent
	done       chan struct{}
	lastSeen   time.Time
	lastSeenMu sync.Mutex
}

type roomEvent struct {
	clientID string
	event    domain.ClientEvent
}

func newRoom(id, code, initialVideoURL string, rdb *redis.Client, instanceID string) *Room {
	return &Room{
		ID:         id,
		Code:       code,
		clients:    make(map[string]*client),
		rdb:        rdb,
		instanceID: instanceID,
		events:     make(chan roomEvent, 64),
		remote:     make(chan domain.ServerEvent, 64),
		done:       make(chan struct{}),
		state: playbackState{
			VideoURL:      initialVideoURL,
			LastUpdatedAt: time.Now(),
		},
	}
}

func (r *Room) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.done:
			return
		case ev := <-r.events:
			r.handleEvent(ev)
		case ev := <-r.remote:
			r.handleRemoteEvent(ev)
		}
	}
}

func (r *Room) handleRemoteEvent(ev domain.ServerEvent) {
	switch ev.Type {
	case domain.EventSync:
		r.state.VideoURL = ev.VideoURL
		r.state.IsPlaying = ev.IsPlaying
		r.state.PositionSeconds = ev.PositionSeconds
		r.state.LastUpdatedAt = ev.LastUpdatedAt
	}
	r.broadcastLocal(ev)
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

	r.broadcastLocal(domain.ServerEvent{
		Type:    domain.EventPresence,
		Members: names,
	})
}

func (r *Room) broadcast(ev domain.ServerEvent) {
	r.broadcastLocal(ev)
	r.publishRemote(ev)
}

func (r *Room) broadcastLocal(ev domain.ServerEvent) {
	r.mu.RLock()
	targets := make([]*client, 0, len(r.clients))
	for _, c := range r.clients {
		targets = append(targets, c)
	}
	r.mu.RUnlock()

	for _, c := range targets {
		if err := c.conn.writeJSON(ev); err != nil {
			slog.Error("room broadcast write failed", "room", r.Code, "client", c.id, "err", err)
		}
	}
}

func (r *Room) publishRemote(ev domain.ServerEvent) {
	if r.rdb == nil {
		return
	}
	data, err := json.Marshal(pubSubEnvelope{Code: r.Code, Event: ev, InstanceID: r.instanceID})
	if err != nil {
		slog.Error("failed to marshal cross-instance event", "room", r.Code, "err", err)
		return
	}
	if err := r.rdb.Publish(context.Background(), crossInstanceChannel(r.Code), data).Err(); err != nil {
		slog.Error("failed to publish cross-instance event", "room", r.Code, "err", err)
	}
}

func (r *Room) applyRemote(ev domain.ServerEvent) {
	select {
	case r.remote <- ev:
	default:
		slog.Error("room remote channel full, dropping cross-instance event", "room", r.Code, "type", ev.Type)
	}
}

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

type Manager struct {
	mu    sync.RWMutex
	rooms map[string]*Room

	rdb *redis.Client

	instanceID string

	idleTimeout time.Duration
}

func NewManager(rdb *redis.Client, idleTimeout time.Duration) *Manager {
	return &Manager{
		rooms:       make(map[string]*Room),
		rdb:         rdb,
		instanceID:  generateInstanceID(),
		idleTimeout: idleTimeout,
	}
}

func generateInstanceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "instance-fallback"
	}
	return hex.EncodeToString(b)
}

func (m *Manager) GetOrCreate(ctx context.Context, code, dbID, initialVideoURL string) *Room {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.rooms[code]; ok {
		return r
	}

	r := newRoom(dbID, code, initialVideoURL, m.rdb, m.instanceID)
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

func (m *Manager) Join(r *Room, clientID, name string, conn *websocket.Conn) {
	sc := &safeConn{conn: conn}

	r.mu.Lock()
	r.clients[clientID] = &client{id: clientID, name: name, conn: sc}
	r.mu.Unlock()

	r.touch()

	snap := r.snapshot()
	snap.YourClientID = clientID
	if err := sc.writeJSON(snap); err != nil {
		slog.Error("failed to send snapshot to joining client", "room", r.Code, "err", err)
	}

	r.broadcastPresence()
	slog.Info("client joined room", "room", r.Code, "client", clientID, "members", r.clientCount())
}

func (m *Manager) Leave(r *Room, clientID string) {
	r.mu.Lock()
	delete(r.clients, clientID)
	r.mu.Unlock()

	r.touch()
	r.broadcastPresence()
	slog.Info("client left room", "room", r.Code, "client", clientID, "members", r.clientCount())
}

func (m *Manager) Dispatch(r *Room, clientID string, ev domain.ClientEvent) {
	select {
	case r.events <- roomEvent{clientID: clientID, event: ev}:
	default:
		slog.Error("room event channel full, dropping event", "room", r.Code, "type", ev.Type)
	}
}

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

func (m *Manager) StartCrossInstanceSync(ctx context.Context) {
	if m.rdb == nil {
		return
	}

	pubsub := m.rdb.PSubscribe(ctx, "syncroom:sync:*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	slog.Info("cross-instance room sync subscriber started")

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var envelope pubSubEnvelope
			if err := json.Unmarshal([]byte(msg.Payload), &envelope); err != nil {
				slog.Warn("bad cross-instance payload", "err", err)
				continue
			}
			if envelope.InstanceID != "" && envelope.InstanceID == m.instanceID {
				// This instance already broadcast the event locally before
				// publishing it, so applying our own echo back would send
				// it to local clients a second time.
				continue
			}
			m.mu.RLock()
			r, ok := m.rooms[envelope.Code]
			m.mu.RUnlock()
			if !ok {
				continue
			}
			r.applyRemote(envelope.Event)
		}
	}
}

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
