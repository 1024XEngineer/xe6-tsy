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
	CreateMessage(context.Context, Message) error
	GetMessage(context.Context, string, string) (Message, error)
	CreateAttempt(context.Context, DeliveryAttempt) error
	GetAttempt(context.Context, string) (DeliveryAttempt, error)
	SetMessageStatus(context.Context, string, MessageStatus, *string) error
	SetAttemptStatus(context.Context, string, DeliveryAttemptStatus, *string) error
	ListPreferences(context.Context, string) ([]Preference, error)
	PutPreference(context.Context, Preference) (Preference, error)
}

type TurnReader interface {
	ReadFinalTurns(context.Context, string, []string) ([]FinalTurnSnapshot, error)
}

type Provider interface {
	Send(context.Context, Message) error
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
