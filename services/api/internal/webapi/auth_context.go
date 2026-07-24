package webapi

import "context"

type accountIDContextKey struct{}

// WithAccountID is the handoff point from trusted authentication middleware.
// HTTP handlers never derive account ownership from client-supplied account IDs.
func WithAccountID(ctx context.Context, accountID string) context.Context {
	return context.WithValue(ctx, accountIDContextKey{}, accountID)
}

func accountIDFromContext(ctx context.Context) (string, bool) {
	accountID, ok := ctx.Value(accountIDContextKey{}).(string)
	return accountID, ok && accountID != ""
}
