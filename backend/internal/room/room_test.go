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

func almostEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

func newTestManager() *room.Manager {

	return room.NewManager(nil, 5*time.Minute)
}

func TestSnapshot_PausedReturnsStoredPosition(t *testing.T) {
	r := room.ExportNewRoom("id-1", "TEST-ROOM")
	r.ExportSetState(42.5, false, time.Now().Add(-30*time.Second))

	snap := r.ExportSnapshot()

	if !almostEqual(snap.PositionSeconds, 42.5, 0.01) {
		t.Errorf("paused snapshot position = %.3f, want 42.5", snap.PositionSeconds)
	}
}

func TestSnapshot_PlayingAdvancesPosition(t *testing.T) {
	r := room.ExportNewRoom("id-2", "TEST-ROOM-2")
	elapsed := 10 * time.Second
	r.ExportSetState(20.0, true, time.Now().Add(-elapsed))

	snap := r.ExportSnapshot()

	if snap.PositionSeconds < 29.5 || snap.PositionSeconds > 31.0 {
		t.Errorf("playing snapshot position = %.3f, want ~30.0", snap.PositionSeconds)
	}
}

func TestIdleSweeper_RemovesEmptyRooms(t *testing.T) {
	m := room.NewManager(nil, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := m.GetOrCreate(ctx, "IDLE-ROOM", "db-id-1", "")

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
	m := room.NewManager(nil, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.GetOrCreate(ctx, "BUSY-ROOM", "db-id-2", "")
	m.SweepIdleRooms()
}

func TestWSRoom_PlayEventBroadcastedToOtherClient(t *testing.T) {

	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	m := room.NewManager(rdb, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := m.GetOrCreate(ctx, "WS-TEST", "db-ws-test", "")

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
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	connA, _, err := websocket.DefaultDialer.Dial(wsURL+"?id=client-a&name=Alice", nil)
	if err != nil {
		t.Fatalf("client A dial failed: %v", err)
	}
	defer connA.Close()

	if _, _, err := connA.ReadMessage(); err != nil {
		t.Fatalf("client A read snapshot failed: %v", err)
	}

	if _, _, err := connA.ReadMessage(); err != nil {
		t.Fatalf("client A read presence failed: %v", err)
	}

	connB, _, err := websocket.DefaultDialer.Dial(wsURL+"?id=client-b&name=Bob", nil)
	if err != nil {
		t.Fatalf("client B dial failed: %v", err)
	}
	defer connB.Close()

	if _, _, err := connA.ReadMessage(); err != nil {
		t.Fatalf("client A read B's sync failed: %v", err)
	}
	if _, _, err := connA.ReadMessage(); err != nil {
		t.Fatalf("client A read presence update failed: %v", err)
	}

	playEvent := domain.ClientEvent{
		Type:            domain.EventPlay,
		PositionSeconds: 42.0,
		ClientID:        "client-b",
	}
	data, _ := json.Marshal(playEvent)
	m.Dispatch(r, "client-b", playEvent)

	time.Sleep(50 * time.Millisecond)

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
