package usage

import (
	"context"
	"time"
)

// Repository provides idempotent fact storage and read-only usage summaries.
type Repository interface {
	// Record stores a fact once and reports whether this call created it.
	Record(context.Context, RecordInput) (Detail, bool, error)
	// SessionSummary aggregates usage for a session owned by the supplied account.
	SessionSummary(context.Context, string, string) (Summary, error)
	// AccountSummary aggregates usage in the half-open requested time period.
	AccountSummary(context.Context, string, time.Time, time.Time) (Summary, error)
}

// SessionOwnerReader is implemented by an adapter over the sessions module.
// Record consumers use it to reject facts whose account and session disagree.
type SessionOwnerReader interface {
	// AccountIDForSession returns the authoritative owner of a session.
	AccountIDForSession(context.Context, string) (string, error)
}

// Service defines usage recording and query use cases consumed by adapters.
type Service interface {
	// Record validates ownership and measurement rules before idempotent persistence.
	Record(context.Context, RecordInput) (Detail, error)
	// SessionUsage returns the current account's aggregate for one session.
	SessionUsage(context.Context, string, string) (Summary, error)
	// AccountUsage returns account totals for a validated half-open time period.
	AccountUsage(context.Context, string, time.Time, time.Time) (Summary, error)
}
