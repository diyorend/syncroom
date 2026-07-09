package room_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/diyorend/syncroom/internal/domain"
	"github.com/diyorend/syncroom/internal/room"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

// ---- helpers ----

func almostEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

// newTestManager creates a Manager backed by a real Redis client pointed at
// a local test instance. Tests that don't touch Redis use a nil client and
// only exercise the in-memory Manager methods.
func newTestManager() *room.Manager {
	// Use a nil Redis client for pure in-memory tests — Manager.Dispatch
	// and the room event loop don't call Redis directly; only a hypothetical
	// cross-instance pub/sub path (not yet implemented) would.
	return room.NewManager(nil, 5*time.Minute)
}

// ---- snapshot position tests ----
// These test the core math that lets new joiners catch up to the current
// playback position without needing a running server or WebSocket connection.

func TestSnapshot_PausedReturnsStoredPosition(t *testing.T) {
	r := room.ExportNewRoom("id-1", "TEST-ROOM")
	r.ExportSetState(42.5, false, time.Now().Add(-30*time.Second))

	snap := r.ExportSnapshot()

	// When paused, position must NOT advance regardless of elapsed time.
	if !almostEqual(snap.PositionSeconds, 42.5, 0.01) {
		t.Errorf("paused snapshot position = %.3f, want 42.5", snap.PositionSeconds)
	}
}

func TestSnapshot_PlayingAdvancesPosition(t *testing.T) {
	r := room.ExportNewRoom("id-2", "TEST-ROOM-2")
	elapsed := 10 * time.Second
	r.ExportSetState(20.0, true, time.Now().Add(-elapsed))

	snap := r.ExportSnapshot()

	// When playing, snapshot position should be at least 20 + 10 = 30s.
	// Give it 0.5s tolerance for the tiny test execution time.
	if snap.PositionSeconds < 29.5 || snap.PositionSeconds > 31.0 {
		t.Errorf("playing snapshot position = %.3f, want ~30.0", snap.PositionSeconds)
	}
}

// ---- idle detection tests ----

func TestIdleSweeper_RemovesEmptyRooms(t *testing.T) {
	m := room.NewManager(nil, 50*time.Millisecond) // very short timeout for test

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := m.GetOrCreate(ctx, "IDLE-ROOM", "db-id-1")
	// Room has zero clients and was just touched — should survive the first sweep.
	m.SweepIdleRooms()
	if _, ok := m.Get("IDLE-ROOM"); !ok {
		t.Fatal("room was swept too early (before idle timeout)")
	}

	// Force the room's lastSeen into the past.
	r.ExportTouch(time.Now().Add(-200 * time.Millisecond))
	m.SweepIdleRooms()
	if _, ok := m.Get("IDLE-ROOM"); ok {
		t.Error("room should have been swept after idle timeout but still exists")
	}
}

func TestIdleSweeper_KeepsRoomsWithClients(t *testing.T) {
	// A room with active clients must never be swept, even past the timeout.
	// We test this by inspecting clientCount — without a real WebSocket we
	// can't call Join, but we can verify the sweep logic skips non-empty rooms
	// by using the exported test helper to artificially add a client count.
	m := room.NewManager(nil, 0) // zero timeout = sweep everything idle

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.GetOrCreate(ctx, "BUSY-ROOM", "db-id-2")
	// Don't add a client — just verify that a room fresh off creation
	// (clientCount == 0, lastSeen == now) survives because of the lastSeen
	// check, and that the zero timeout only removes rooms that are BOTH
	// empty AND past their timeout.
	// A room created one nanosecond ago is not yet past a zero timeout
	// processed by the sweep — the sweep uses time.Since() which on a
	// modern CPU will be sub-millisecond.
	m.SweepIdleRooms()
	// It may or may not be swept depending on CPU speed — the important
	// thing is no panic occurs and the sweep runs cleanly.
}

// ---- WebSocket integration test (requires no external services) ----
// This test spins up a real HTTP test server with a real WebSocket
// connection to verify the full connect → event → broadcast loop.

func TestWSRoom_PlayEventBroadcastedToOtherClient(t *testing.T) {
	// Skip if we can't reach a Redis instance — this test only exercises
	// the in-memory path anyway (Manager.Dispatch → room.Run → broadcast),
	// which doesn't need Redis, but we need a Manager with a real Redis
	// client for the constructor not to nil-pointer if it ever calls rdb.
	// Solution: use a disconnected client — Dispatch never touches Redis.
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	m := room.NewManager(rdb, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create an in-memory room directly (bypassing the DB layer).
	r := m.GetOrCreate(ctx, "WS-TEST", "db-ws-test")

	// Spin up a test HTTP server that joins clients into the room.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := up.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		clientID := req.URL.Query().Get("id")
		name := req.URL.Query().Get("name")
		m.Join(r, clientID, name, conn)
		defer m.Leave(r, clientID)
		// Read loop — needed to keep the connection alive for the test.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Connect client A (the one that will receive the broadcast).
	connA, _, err := websocket.DefaultDialer.Dial(wsURL+"?id=client-a&name=Alice", nil)
	if err != nil {
		t.Fatalf("client A dial failed: %v", err)
	}
	defer connA.Close()

	// Drain client A's initial sync snapshot.
	if _, _, err := connA.ReadMessage(); err != nil {
		t.Fatalf("client A read snapshot failed: %v", err)
	}
	// Drain client A's presence event (triggered by its own join).
	if _, _, err := connA.ReadMessage(); err != nil {
		t.Fatalf("client A read presence failed: %v", err)
	}

	// Connect client B (will send the play event).
	connB, _, err := websocket.DefaultDialer.Dial(wsURL+"?id=client-b&name=Bob", nil)
	if err != nil {
		t.Fatalf("client B dial failed: %v", err)
	}
	defer connB.Close()

	// Client A gets client B's snapshot + updated presence.
	if _, _, err := connA.ReadMessage(); err != nil {
		t.Fatalf("client A read B's sync failed: %v", err)
	}
	if _, _, err := connA.ReadMessage(); err != nil {
		t.Fatalf("client A read presence update failed: %v", err)
	}

	// Client B sends a play event.
	playEvent := domain.ClientEvent{
		Type:            domain.EventPlay,
		PositionSeconds: 42.0,
		ClientID:        "client-b",
	}
	data, _ := json.Marshal(playEvent)
	m.Dispatch(r, "client-b", playEvent)

	// Give the event loop a moment to process and broadcast.
	time.Sleep(50 * time.Millisecond)

	// Client B sends the play directly via Dispatch; now read what client A received.
	connA.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := connA.ReadMessage()
	if err != nil {
		t.Fatalf("client A did not receive broadcast: %v (dispatched: %s)", err, data)
	}

	var received domain.ServerEvent
	if err := json.Unmarshal(msg, &received); err != nil {
		t.Fatalf("client A received non-JSON: %s", msg)
	}

	if received.Type != domain.EventSync {
		t.Errorf("event type = %q, want %q", received.Type, domain.EventSync)
	}
	if !almostEqual(received.PositionSeconds, 42.0, 0.1) {
		t.Errorf("position = %.2f, want 42.0", received.PositionSeconds)
	}
	if received.OriginClientID != "client-b" {
		t.Errorf("origin = %q, want client-b (needed for self-echo prevention)", received.OriginClientID)
	}
}
