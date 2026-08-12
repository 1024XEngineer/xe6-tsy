package modeprojection

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

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

func (r *MemoryRepository) Project(ctx context.Context, event realtimev1.ModeChangedEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return domain.ErrInvalidArgument
	}
	if r == nil {
		return domain.ErrNotImplemented
	}
	payloadHash, err := hashModeChangedEvent(event)
	if err != nil {
		return err
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
			UpdatedAt:         time.Now().UTC(),
		}
	}
	return nil
}

// Latest returns the latest fact observed by API and never claims realtime authority.
func (r *MemoryRepository) Latest(ctx context.Context, sessionID string) (Projection, error) {
	if err := ctx.Err(); err != nil {
		return Projection{}, err
	}
	if r == nil || sessionID == "" {
		return Projection{}, domain.ErrInvalidArgument
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	projection, ok := r.projections[sessionID]
	if !ok {
		return Projection{}, domain.ErrNotFound
	}
	return projection, nil
}

var _ Repository = (*MemoryRepository)(nil)
