package delivery

import "time"

// MaxIdempotencyKeyLength is the public contract limit for create and retry keys.
const MaxIdempotencyKeyLength = 200

// Channel identifies a supported outbound delivery mechanism.
type Channel string

const (
	// ChannelEmail sends a message to a verified email destination.
	ChannelEmail Channel = "email"
)

// IsSupportedChannel reports whether a channel is accepted by the public
// delivery contract. Keep this central so HTTP and use cases cannot drift.
func IsSupportedChannel(channel Channel) bool {
	return channel == ChannelEmail
}

// MessageStatus describes the user-visible lifecycle of an outbound message.
type MessageStatus string

const (
	// MessageStatusQueued means the initial provider attempt is waiting for processing.
	MessageStatusQueued MessageStatus = "queued"
	// MessageStatusSending means a worker owns the current provider attempt.
	MessageStatusSending MessageStatus = "sending"
	// MessageStatusSent means a provider attempt completed successfully.
	MessageStatusSent MessageStatus = "sent"
	// MessageStatusFailed means delivery stopped after a failed attempt.
	MessageStatusFailed MessageStatus = "failed"
	// MessageStatusRetrying means another attempt has been scheduled.
	MessageStatusRetrying MessageStatus = "retrying"
	// MessageStatusCancelled means no further delivery attempts are allowed.
	MessageStatusCancelled MessageStatus = "cancelled"

	// deliveryUnknownErrorCode means a non-idempotent provider may have accepted
	// the request before the worker lost its durable completion result. A normal
	// retry must not replay such a message.
	deliveryUnknownErrorCode = "delivery_unknown"
)

// DeliveryAttemptStatus describes one provider invocation independently of its message.
type DeliveryAttemptStatus string

const (
	// AttemptStatusQueued means the attempt is waiting for a worker.
	AttemptStatusQueued DeliveryAttemptStatus = "queued"
	// AttemptStatusSending means the provider call is in progress.
	AttemptStatusSending DeliveryAttemptStatus = "sending"
	// AttemptStatusSucceeded means the provider accepted the request.
	AttemptStatusSucceeded DeliveryAttemptStatus = "succeeded"
	// AttemptStatusFailed means the provider call finished with an error.
	AttemptStatusFailed DeliveryAttemptStatus = "failed"
)

// FinalTurnSnapshot matches the read boundary provided by the turns module.
// Once copied into a Message, these fields are never refreshed during retries.
type FinalTurnSnapshot struct {
	TurnID                string    `json:"turn_id"`
	SessionID             string    `json:"session_id"`
	ParticipantID         *string   `json:"participant_id"`
	SpeakerLabelSnapshot  *string   `json:"speaker_label_snapshot"`
	SourceLanguage        string    `json:"source_language"`
	TargetLanguage        string    `json:"target_language"`
	LanguageConfigVersion int64     `json:"language_config_version"`
	SourceText            string    `json:"source_text"`
	TranslatedText        string    `json:"translated_text"`
	CreatedAt             time.Time `json:"created_at"`
}

// Message is an immutable outbound content snapshot with mutable delivery state.
type Message struct {
	ID              string              `json:"id"`
	AccountID       string              `json:"account_id"`
	Channel         Channel             `json:"channel"`
	DestinationRef  string              `json:"destination_ref"`
	SnapshotVersion int                 `json:"snapshot_version"`
	Turns           []FinalTurnSnapshot `json:"turns"`
	Status          MessageStatus       `json:"status"`
	Attempts        int                 `json:"attempts"`
	LastErrorCode   *string             `json:"last_error_code"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

// DeliveryAttempt records one queued or completed provider invocation.
type DeliveryAttempt struct {
	ID            string                `json:"id"`
	MessageID     string                `json:"message_id"`
	AttemptNumber int                   `json:"attempt_number"`
	Status        DeliveryAttemptStatus `json:"status"`
	ErrorCode     *string               `json:"error_code"`
	NextAttemptAt *time.Time            `json:"next_attempt_at"`
	StartedAt     *time.Time            `json:"started_at"`
	FinishedAt    *time.Time            `json:"finished_at"`
	CreatedAt     time.Time             `json:"created_at"`
}

// CreateMessageRecord groups the message, initial attempt, and idempotency key
// that a repository must persist atomically with an outbox record.
type CreateMessageRecord struct {
	Message        Message
	InitialAttempt DeliveryAttempt
	IdempotencyKey string
}

// CreateRetryRecord groups a manual retry transition and its idempotency key.
type CreateRetryRecord struct {
	AccountID      string
	MessageID      string
	Attempt        DeliveryAttempt
	IdempotencyKey string
}

// VerifiedDestination is the accounts-module result used only for provider calls.
// ProviderTarget must not be persisted in messages, events, API output, or logs.
type VerifiedDestination struct {
	AccountID      string
	Channel        Channel
	DestinationRef string
	ProviderTarget string
}

// SendRequest contains the immutable message snapshot and verified provider target.
// ProviderIdempotencyKey is a durable attempt identifier, not a caller supplied
// request key. Provider adapters must pass it to the external provider's
// idempotency mechanism so crash recovery cannot create a second delivery.
type SendRequest struct {
	Message                Message
	Attempt                DeliveryAttempt
	Destination            VerifiedDestination
	ProviderIdempotencyKey string
}

// CreateInput combines trusted account context, idempotency metadata, and client fields.
type CreateInput struct {
	AccountID      string   `json:"-"`
	IdempotencyKey string   `json:"-"`
	Channel        Channel  `json:"channel"`
	DestinationRef string   `json:"destination_ref"`
	TurnIDs        []string `json:"turn_ids"`
}

// Preference combines a user's channel choice with authoritative verification state.
type Preference struct {
	AccountID string    `json:"account_id"`
	Channel   Channel   `json:"channel"`
	Enabled   bool      `json:"enabled"`
	Verified  bool      `json:"verified"`
	UpdatedAt time.Time `json:"updated_at"`
}
