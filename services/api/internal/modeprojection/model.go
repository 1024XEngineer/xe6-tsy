package modeprojection

import (
	"context"
	"crypto/sha256"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// Projection is the latest mode fact observed by API for one session.
// Realtime remains authoritative; this record is only a durable read projection.
type Projection struct {
	SessionID         string
	RuntimeInstanceID string
	ActiveMode        realtimev1.Mode
	Generation        int64
	LastEventID       string
	OccurredAt        time.Time
	UpdatedAt         time.Time
}

// Repository stores immutable mode-change facts and advances the latest-observed projection.
type Repository interface {
	ProjectModeChanged(context.Context, realtimev1.ModeChangedEvent, [sha256.Size]byte) error
}

func shouldAdvance(current Projection, event realtimev1.ModeChangedEvent) bool {
	if current.RuntimeInstanceID == event.RuntimeInstanceID {
		return event.ResultingGeneration > current.Generation
	}
	return event.OccurredAt.After(current.OccurredAt)
}
