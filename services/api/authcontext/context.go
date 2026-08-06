// Package authcontext owns the account identity handoff between authentication middleware and API adapters.
package authcontext

import "context"

type accountIDContextKey struct{}

// WithAccountID attaches an account identity established by trusted authentication middleware.
func WithAccountID(ctx context.Context, accountID string) context.Context {
	return context.WithValue(ctx, accountIDContextKey{}, accountID)
}

// AccountID reads the account identity established by trusted authentication middleware.
func AccountID(ctx context.Context) (string, bool) {
	accountID, ok := ctx.Value(accountIDContextKey{}).(string)
	return accountID, ok && accountID != ""
}
