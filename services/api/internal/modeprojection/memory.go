package modeprojection

import (
	"context"
	"crypto/sha256"
	"sync"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type memoryEvent struct {
	hash [sha256.Size]byte
}

// MemoryRepository mirrors the durable ordering rules for deterministic offline tests.
type MemoryRepository struct {
	mu          sync.RWMutex
	events      map[string]memoryEvent
	projections map[string]Projection
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		events:      make(map[string]memoryEvent),
		projections: make(map[string]Projection),
	}
}

func (r *MemoryRepository) ProjectModeChanged(ctx context.Context, event realtimev1.ModeChangedEvent, payloadHash [sha256.Size]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if r == nil {
		return domain.ErrNotImplemented
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.events[event.EventID]; ok {
		if previous.hash != payloadHash {
			return domain.ErrConflict
		}
		return nil
	}
	r.events[event.EventID] = memoryEvent{hash: payloadHash}
	current, exists := r.projections[event.SessionID]
	if !exists || shouldAdvance(current, event) {
		r.projections[event.SessionID] = Projection{
			SessionID:         event.SessionID,
			RuntimeInstanceID: event.RuntimeInstanceID,
			ActiveMode:        event.ToMode,
			Generation:        event.ResultingGeneration,
			LastEventID:       event.EventID,
			OccurredAt:        event.OccurredAt,
		}
	}
	return nil
}

// Projection returns a copy of the latest observed state, if one exists.
func (r *MemoryRepository) Projection(ctx context.Context, sessionID string) (Projection, bool, error) {
	if err := ctx.Err(); err != nil {
		return Projection{}, false, err
	}
	if r == nil || sessionID == "" {
		return Projection{}, false, domain.ErrInvalidArgument
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	projection, ok := r.projections[sessionID]
	return projection, ok, nil
}

var _ Repository = (*MemoryRepository)(nil)
