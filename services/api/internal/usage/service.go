package usage

import (
	"context"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// UseCases reserves usage orchestration while storage and ownership adapters are pending.
type UseCases struct{}

// NewUseCases returns the placeholder usage service used by the foundation server.
func NewUseCases() *UseCases { return &UseCases{} }

// Record will validate and idempotently persist one versioned usage fact.
func (*UseCases) Record(context.Context, RecordInput) (Detail, error) {
	return Detail{}, domain.ErrNotImplemented
}

// SessionUsage will verify session ownership before returning its aggregate.
func (*UseCases) SessionUsage(context.Context, string, string) (Summary, error) {
	return Summary{}, domain.ErrNotImplemented
}

// AccountUsage will return account totals for a validated reporting period.
func (*UseCases) AccountUsage(context.Context, string, time.Time, time.Time) (Summary, error) {
	return Summary{}, domain.ErrNotImplemented
}
