package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
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

// FakeEmailProviderConfig controls deterministic, offline email behavior.
// SendFunc receives the complete provider request because it is the explicit
// test boundary; callers must not log or persist Destination.ProviderTarget.
// SendErr is used when SendFunc is nil and may be ErrProviderRejected for a
// deterministic permanent-rejection scenario.
type FakeEmailProviderConfig struct {
	SendFunc func(context.Context, SendRequest) error
	SendErr  error
}

// FakeEmailProvider is an explicitly injected, in-memory email provider for
// local development and unit tests. Successful calls are deduplicated by
// ProviderIdempotencyKey for the lifetime of this instance only. Failed calls
// are not marked accepted, so a later attempt with the same key can model a
// retry after an unknown network result. This provider must not be treated as
// crash-safe across process restarts.
//
// Requests returns sanitized observations: ProviderTarget is always blank.
// The real target is passed only to SendFunc while the call is being handled.
type FakeEmailProvider struct {
	mu       sync.Mutex
	config   FakeEmailProviderConfig
	requests []SendRequest
	accepted map[string]fakeProviderIdentity
	inFlight map[string]*fakeProviderCall
}

type fakeProviderCall struct {
	done     chan struct{}
	err      error
	identity fakeProviderIdentity
}

type fakeProviderIdentity struct {
	attemptID      string
	messageID      string
	destinationRef string
}

// NewFakeEmailProvider constructs an explicit offline provider. Production
// wiring should use a real adapter or UnconfiguredProvider instead.
func NewFakeEmailProvider(config FakeEmailProviderConfig) *FakeEmailProvider {
	return &FakeEmailProvider{
		config:   config,
		accepted: make(map[string]fakeProviderIdentity),
		inFlight: make(map[string]*fakeProviderCall),
	}
}

// Send validates the verified email boundary, invokes the configured fake,
// and suppresses duplicate successful calls for the same provider key.
func (p *FakeEmailProvider) Send(ctx context.Context, request SendRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateFakeEmailRequest(request); err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("%w: nil fake provider", ErrProviderNotConfigured)
	}

	key := request.ProviderIdempotencyKey
	p.mu.Lock()
	if p.accepted == nil {
		p.accepted = make(map[string]fakeProviderIdentity)
	}
	if p.inFlight == nil {
		p.inFlight = make(map[string]*fakeProviderCall)
	}
	identity := providerIdentity(request)
	if accepted, ok := p.accepted[key]; ok {
		p.mu.Unlock()
		if accepted != identity {
			return providerIdempotencyConflict()
		}
		return nil
	}
	if call, ok := p.inFlight[key]; ok {
		p.mu.Unlock()
		if call.identity != identity {
			return providerIdempotencyConflict()
		}
		return waitForFakeProviderCall(ctx, call)
	}
	call := &fakeProviderCall{done: make(chan struct{}), identity: identity}
	p.inFlight[key] = call
	p.requests = append(p.requests, sanitizeProviderRequest(request))
	config := p.config
	p.mu.Unlock()

	var err error
	if config.SendFunc != nil {
		err = config.SendFunc(ctx, request)
	} else if config.SendErr != nil {
		err = config.SendErr
	}

	p.mu.Lock()
	delete(p.inFlight, key)
	call.err = err
	if err == nil {
		p.accepted[key] = identity
	}
	close(call.done)
	p.mu.Unlock()
	return err
}

// SupportsProviderIdempotency is false because the in-memory acceptance map is
// lost when the provider instance or process is replaced. The method remains
// explicit so callers cannot accidentally infer crash-safe idempotency.
func (*FakeEmailProvider) SupportsProviderIdempotency() bool { return false }

// Requests returns sanitized copies of provider calls in invocation order.
// In particular, the verified provider target is never returned to callers.
func (p *FakeEmailProvider) Requests() []SendRequest {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]SendRequest, len(p.requests))
	for i, request := range p.requests {
		result[i] = sanitizeProviderRequest(request)
	}
	return result
}

func waitForFakeProviderCall(ctx context.Context, call *fakeProviderCall) error {
	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func validateFakeEmailRequest(request SendRequest) error {
	if strings.TrimSpace(request.ProviderIdempotencyKey) == "" ||
		strings.TrimSpace(request.Attempt.ID) == "" ||
		strings.TrimSpace(request.Message.ID) == "" ||
		request.ProviderIdempotencyKey != request.Attempt.ID ||
		request.Attempt.MessageID != request.Message.ID {
		return fmt.Errorf("%w: provider request identity is incomplete", domain.ErrInvalidArgument)
	}
	if request.Message.AccountID == "" || request.Destination.AccountID != request.Message.AccountID {
		return fmt.Errorf("%w: provider request account scope is invalid", domain.ErrInvalidArgument)
	}
	if request.Message.Channel != ChannelEmail || request.Destination.Channel != ChannelEmail {
		return fmt.Errorf("%w: fake provider supports email only", domain.ErrInvalidArgument)
	}
	if request.Message.DestinationRef == "" || request.Destination.DestinationRef != request.Message.DestinationRef {
		return fmt.Errorf("%w: provider destination reference is invalid", domain.ErrInvalidArgument)
	}
	if strings.TrimSpace(request.Destination.ProviderTarget) == "" {
		return fmt.Errorf("%w: verified provider destination is missing", domain.ErrInvalidArgument)
	}
	return nil
}

func providerIdentity(request SendRequest) fakeProviderIdentity {
	return fakeProviderIdentity{
		attemptID:      request.Attempt.ID,
		messageID:      request.Message.ID,
		destinationRef: request.Message.DestinationRef,
	}
}

func providerIdempotencyConflict() error {
	return fmt.Errorf("%w: %w: provider idempotency key was reused", ErrProviderRejected, domain.ErrConflict)
}

func sanitizeProviderRequest(request SendRequest) SendRequest {
	request.Message.Turns = cloneTurns(request.Message.Turns)
	request.Destination.ProviderTarget = ""
	return request
}

var _ Provider = UnconfiguredProvider{}
var _ IdempotentProvider = UnconfiguredProvider{}
var _ IdempotentProvider = (*FakeEmailProvider)(nil)
