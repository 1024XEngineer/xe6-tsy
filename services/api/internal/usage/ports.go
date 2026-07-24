package usage

import (
	"context"
	"time"
)

type Repository interface {
	Record(context.Context, RecordInput) (Detail, bool, error)
	SessionSummary(context.Context, string, string) (Summary, error)
	AccountSummary(context.Context, string, time.Time, time.Time) (Summary, error)
}

// SessionOwnerReader is implemented by an adapter over the sessions module.
// Record consumers use it to reject facts whose account and session disagree.
type SessionOwnerReader interface {
	AccountIDForSession(context.Context, string) (string, error)
}

type Service interface {
	Record(context.Context, RecordInput) (Detail, error)
	SessionUsage(context.Context, string, string) (Summary, error)
	AccountUsage(context.Context, string, time.Time, time.Time) (Summary, error)
}
