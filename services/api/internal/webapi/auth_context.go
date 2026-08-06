package webapi

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/api/authcontext"
)

// WithAccountID is the handoff point from trusted authentication middleware.
// HTTP handlers never derive account ownership from client-supplied account IDs.
func WithAccountID(ctx context.Context, accountID string) context.Context {
	return authcontext.WithAccountID(ctx, accountID)
}

// AccountIDFromContext returns a non-empty account ID previously set by trusted middleware.
func AccountIDFromContext(ctx context.Context) (string, bool) {
	return authcontext.AccountID(ctx)
}

// accountIDFromContext keeps the previous unexported helper for existing call sites.
func accountIDFromContext(ctx context.Context) (string, bool) {
	return AccountIDFromContext(ctx)
}
