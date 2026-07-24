package delivery

import (
	"context"
	"time"
)

type QueueMessage struct {
	AttemptID string
	Receipt   string
}

type Repository interface {
	// CreateMessage atomically persists the message, initial attempt, and outbox record.
	CreateMessage(context.Context, CreateMessageRecord) error
	GetMessage(context.Context, string, string) (Message, error)
	// CreateRetry atomically persists the next attempt, message state, and outbox record.
	CreateRetry(context.Context, CreateRetryRecord) (Message, error)
	GetAttempt(context.Context, string) (DeliveryAttempt, error)
	SetMessageStatus(context.Context, string, MessageStatus, *string) error
	SetAttemptStatus(context.Context, string, DeliveryAttemptStatus, *string) error
	ListPreferences(context.Context, string) ([]Preference, error)
	PutPreference(context.Context, Preference) (Preference, error)
}

type TurnReader interface {
	ReadFinalTurns(context.Context, string, []string) ([]FinalTurnSnapshot, error)
}

// DestinationReader is implemented by an adapter over the accounts module.
type DestinationReader interface {
	ResolveVerifiedDestination(context.Context, string, Channel, string) (VerifiedDestination, error)
}

type Provider interface {
	Send(context.Context, SendRequest) error
}

type Queue interface {
	Enqueue(context.Context, string, string) error // attempt ID, idempotency key
	Receive(context.Context) (QueueMessage, error)
	Ack(context.Context, string) error
	Nack(context.Context, string, time.Time) error
}

type Service interface {
	Create(context.Context, CreateInput) (Message, error)
	Get(context.Context, string, string) (Message, error)
	Retry(context.Context, string, string, string) (Message, error)
	Preferences(context.Context, string) ([]Preference, error)
	PutPreference(context.Context, string, Channel, bool) (Preference, error)
}
