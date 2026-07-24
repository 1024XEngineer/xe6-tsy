package delivery

import (
	"context"
	"time"
)

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
	// SetMessageStatus advances user-visible delivery state and its stable error code.
	SetMessageStatus(context.Context, string, MessageStatus, *string) error
	// SetAttemptStatus advances one provider attempt and its stable error code.
	SetAttemptStatus(context.Context, string, DeliveryAttemptStatus, *string) error
	// ListPreferences returns channel settings for one account.
	ListPreferences(context.Context, string) ([]Preference, error)
	// PutPreference persists a validated channel preference and returns the stored value.
	PutPreference(context.Context, Preference) (Preference, error)
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
type Provider interface {
	// Send performs one provider invocation for an already verified request.
	Send(context.Context, SendRequest) error
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
