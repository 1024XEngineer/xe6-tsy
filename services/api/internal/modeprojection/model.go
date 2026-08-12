package modeprojection

import (
	"context"
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
	Project(context.Context, realtimev1.ModeChangedEvent) error
	Latest(context.Context, string) (Projection, error)
}

// shouldAdvance uses generation only inside a runtime because generation resets on restart.
// Across runtimes, occurred_at is the available ordering signal; event ID breaks exact timestamp
// ties deterministically. Clock skew therefore limits this audit view and is why it is not live state.
func shouldAdvance(current Projection, event realtimev1.ModeChangedEvent) bool {
	if current.RuntimeInstanceID == event.RuntimeInstanceID {
		return event.ResultingGeneration > current.Generation
	}
	return event.OccurredAt.After(current.OccurredAt) ||
		(event.OccurredAt.Equal(current.OccurredAt) && event.EventID > current.LastEventID)
}
