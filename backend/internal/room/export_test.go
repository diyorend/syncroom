// export_test.go exposes internal Room state for tests in the room_test
// package. Compiled only during `go test` — the _test.go suffix excludes
// it from production builds. Keeps production code unexported while giving
// tests the access they need without reflection hacks.
package room

import (
	"time"

	"github.com/diyorend/syncroom/internal/domain"
)

func ExportNewRoom(id, code string) *Room {
	return newRoom(id, code)
}

func (r *Room) ExportSetState(positionSeconds float64, isPlaying bool, lastUpdatedAt time.Time) {
	r.state.PositionSeconds = positionSeconds
	r.state.IsPlaying = isPlaying
	r.state.LastUpdatedAt = lastUpdatedAt
}

// ExportSnapshot returns the full domain.ServerEvent so tests can inspect
// any field without a separate mirror type.
func (r *Room) ExportSnapshot() domain.ServerEvent {
	return r.snapshot()
}

func (r *Room) ExportTouch(t time.Time) {
	r.lastSeenMu.Lock()
	r.lastSeen = t
	r.lastSeenMu.Unlock()
}
