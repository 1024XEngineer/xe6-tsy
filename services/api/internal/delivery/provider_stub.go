package delivery

import (
	"context"
	"errors"
	"fmt"
)

// ErrProviderNotConfigured means that no outbound provider has been wired.
// It is deliberately separate from the permanent rejection marker so startup
// and delivery metrics can distinguish configuration gaps.
var ErrProviderNotConfigured = errors.New("delivery provider not configured")

// UnconfiguredProvider is the fail-closed default for the delivery runtime.
// It never reports a successful send, does not claim provider idempotency, and
// never includes the verified destination in its returned error.
type UnconfiguredProvider struct{}

// Send refuses delivery until an explicit provider adapter is injected.
func (UnconfiguredProvider) Send(ctx context.Context, _ SendRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w: provider adapter is not wired", ErrProviderNotConfigured)
}

// SupportsProviderIdempotency reports that an unconfigured provider cannot
// safely replay an invocation after a process crash.
func (UnconfiguredProvider) SupportsProviderIdempotency() bool { return false }

var _ Provider = UnconfiguredProvider{}
var _ IdempotentProvider = UnconfiguredProvider{}
