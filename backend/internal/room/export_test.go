package room

import (
	"time"

	"github.com/diyorend/syncroom/internal/domain"
)

func ExportNewRoom(id, code string) *Room {
	return newRoom(id, code, "", nil, "test-instance")
}

func (r *Room) ExportSetState(positionSeconds float64, isPlaying bool, lastUpdatedAt time.Time) {
	r.state.PositionSeconds = positionSeconds
	r.state.IsPlaying = isPlaying
	r.state.LastUpdatedAt = lastUpdatedAt
}

func (r *Room) ExportSnapshot() domain.ServerEvent {
	return r.snapshot()
}

func (r *Room) ExportTouch(t time.Time) {
	r.lastSeenMu.Lock()
	r.lastSeen = t
	r.lastSeenMu.Unlock()
}
