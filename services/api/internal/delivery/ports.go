package delivery

import (
	"context"
	"errors"
	"time"
)

// ErrProviderRejected marks a provider response that definitively rejects a
// request. Worker treats this marker as terminal; transient transport errors
// must not wrap it.
var ErrProviderRejected = errors.New("delivery provider rejected request")

// QueueMessage carries an attempt identifier and broker receipt used for settlement.
type QueueMessage struct {
	AttemptID string
	Receipt   string
}

// Repository owns message, attempt, preference, and outbox persistence boundaries.
type Repository interface {
	// CreateMessage atomically persists the message, initial attempt, and outbox record.
	CreateMessage(context.Context, CreateMessageRecord) error
	// GetMessage reads a message only within the supplied account ownership scope.
	GetMessage(context.Context, string, string) (Message, error)
	// CreateRetry atomically persists the next attempt, message state, and outbox record.
	CreateRetry(context.Context, CreateRetryRecord) (Message, error)
	// GetAttempt reads one provider attempt for worker processing.
	GetAttempt(context.Context, string) (DeliveryAttempt, error)
	// ClaimAttempt atomically transitions a queued attempt to sending. Only the
	// caller that successfully claims it may invoke the external provider.
	ClaimAttempt(context.Context, string) (DeliveryAttempt, error)
	// RequeueAttempt atomically releases an attempt that has not reached the
	// provider boundary. It must only transition sending -> queued; a provider
	// error after invocation deliberately does not use this operation because
	// acceptance may be unknown.
	RequeueAttempt(context.Context, string, time.Time) error
	// CompleteAttempt atomically records one terminal attempt result and its
	// corresponding user-visible message result before the broker is ACKed.
	CompleteAttempt(context.Context, string, string, DeliveryAttemptStatus, MessageStatus, *string) error
	// SetMessageStatus advances user-visible delivery state and its stable error code.
	SetMessageStatus(context.Context, string, MessageStatus, *string) error
	// SetAttemptStatus advances one provider attempt and its stable error code.
	SetAttemptStatus(context.Context, string, DeliveryAttemptStatus, *string) error
	// ListPreferences returns channel settings for one account.
	ListPreferences(context.Context, string) ([]Preference, error)
	// PutPreference persists a validated channel preference and returns the stored value.
	PutPreference(context.Context, Preference) (Preference, error)
}

// OutboxRepository exposes the durable publisher hand-off used by production
// repositories. API requests commit the outbox row first; a dispatcher later
// publishes it to Valkey, eliminating the database/queue crash window.
type OutboxRepository interface {
	ClaimOutbox(context.Context, int) ([]OutboxRecord, error)
	MarkOutboxPublished(context.Context, string) error
	MarkOutboxFailed(context.Context, string, string) error
}

type OutboxRecord struct {
	ID        string
	AttemptID string
	Key       string
	Attempts  int
}

type IdempotencyReader interface {
	GetMessageByIdempotency(context.Context, string, string) (Message, error)
}

// RetryIdempotencyReader resolves a retry key through the durable outbox row
// and its attempt/message relationship. It is separate from IdempotencyReader
// so lightweight repositories that only support creation replay remain valid.
type RetryIdempotencyReader interface {
	GetMessageByDeliveryIdempotency(context.Context, string, string) (Message, error)
}

type WorkerMessageReader interface {
	GetMessageForWorker(context.Context, string) (Message, error)
}

// TurnReader provides final transcript snapshots without coupling delivery to Turn storage.
type TurnReader interface {
	// ReadFinalTurns returns only final Turns owned by the account for snapshot creation.
	ReadFinalTurns(context.Context, string, []string) ([]FinalTurnSnapshot, error)
}

// DestinationReader is implemented by an adapter over the accounts module.
type DestinationReader interface {
	// ResolveVerifiedDestination returns an account-owned target suitable for provider use.
	ResolveVerifiedDestination(context.Context, string, Channel, string) (VerifiedDestination, error)
}

// Provider isolates the outbound channel implementation from delivery orchestration.
// Implementations must pass SendRequest.ProviderIdempotencyKey to the external
// provider's idempotency mechanism: a process crash can happen after provider
// acceptance but before the terminal database status is committed.
type Provider interface {
	// Send performs one provider invocation for an already verified request.
	Send(context.Context, SendRequest) error
}

// IdempotentProvider declares that the external provider applies
// ProviderIdempotencyKey. A worker can safely resume a sending attempt only for
// this capability. Providers without it are never replayed automatically after
// a crash because that could create a duplicate user-visible delivery.
type IdempotentProvider interface {
	Provider
	SupportsProviderIdempotency() bool
}

// Queue defines reliable attempt delivery and explicit broker settlement.
type Queue interface {
	// Enqueue publishes an attempt using the supplied idempotency key.
	Enqueue(context.Context, string, string) error // attempt ID, idempotency key
	// Receive blocks until work is available or the context is cancelled.
	Receive(context.Context) (QueueMessage, error)
	// Ack confirms successful processing of a broker receipt.
	Ack(context.Context, string) error
	// Nack releases a broker receipt for delivery at or after the requested time.
	Nack(context.Context, string, time.Time) error
}

// Service defines outbound-message use cases consumed by the HTTP adapter.
type Service interface {
	// Create validates selected final Turns and creates an immutable message snapshot.
	Create(context.Context, CreateInput) (Message, error)
	// Get returns an account-owned message and its current delivery state.
	Get(context.Context, string, string) (Message, error)
	// Retry creates the next attempt for an eligible failed message idempotently.
	Retry(context.Context, string, string, string) (Message, error)
	// Preferences returns the current account's channel settings.
	Preferences(context.Context, string) ([]Preference, error)
	// PutPreference updates whether the account enables one supported channel.
	PutPreference(context.Context, string, Channel, bool) (Preference, error)
}
