package delivery

import "context"

type Repository interface {
	Create(context.Context, Message) error
	Get(context.Context, string, string) (Message, error)
	MarkSending(context.Context, string) error
	MarkSent(context.Context, string) error
	MarkFailed(context.Context, string, string) error
	ScheduleRetry(context.Context, string) error
	ListPreferences(context.Context, string) ([]Preference, error)
	PutPreference(context.Context, Preference) (Preference, error)
}

type TurnReader interface {
	ReadFinalTurns(context.Context, string, []string) ([]TurnSnapshot, error)
}

type Provider interface {
	Send(context.Context, Message) error
}

type Queue interface {
	Enqueue(context.Context, string) error
	Receive(context.Context) (string, error)
}

type Service interface {
	Create(context.Context, CreateInput) (Message, error)
	Get(context.Context, string, string) (Message, error)
	Retry(context.Context, string, string, string) (Message, error)
	Preferences(context.Context, string) ([]Preference, error)
	PutPreference(context.Context, string, Channel, bool) (Preference, error)
}
