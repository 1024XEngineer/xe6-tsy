package delivery

import "time"

type Channel string

const (
	ChannelEmail Channel = "email"
)

type MessageStatus string

const (
	MessageStatusQueued    MessageStatus = "queued"
	MessageStatusSending   MessageStatus = "sending"
	MessageStatusSent      MessageStatus = "sent"
	MessageStatusFailed    MessageStatus = "failed"
	MessageStatusRetrying  MessageStatus = "retrying"
	MessageStatusCancelled MessageStatus = "cancelled"
)

type DeliveryAttemptStatus string

const (
	AttemptStatusQueued    DeliveryAttemptStatus = "queued"
	AttemptStatusSending   DeliveryAttemptStatus = "sending"
	AttemptStatusSucceeded DeliveryAttemptStatus = "succeeded"
	AttemptStatusFailed    DeliveryAttemptStatus = "failed"
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
	LanguageConfigVersion *int64    `json:"language_config_version"`
	SourceText            string    `json:"source_text"`
	TranslatedText        string    `json:"translated_text"`
	CreatedAt             time.Time `json:"created_at"`
}

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

type CreateMessageRecord struct {
	Message        Message
	InitialAttempt DeliveryAttempt
	IdempotencyKey string
}

type CreateRetryRecord struct {
	AccountID      string
	MessageID      string
	Attempt        DeliveryAttempt
	IdempotencyKey string
}

type VerifiedDestination struct {
	AccountID      string
	Channel        Channel
	DestinationRef string
	ProviderTarget string
}

type SendRequest struct {
	Message     Message
	Attempt     DeliveryAttempt
	Destination VerifiedDestination
}

type CreateInput struct {
	AccountID      string   `json:"-"`
	IdempotencyKey string   `json:"-"`
	Channel        Channel  `json:"channel"`
	DestinationRef string   `json:"destination_ref"`
	TurnIDs        []string `json:"turn_ids"`
}

type Preference struct {
	AccountID string    `json:"account_id"`
	Channel   Channel   `json:"channel"`
	Enabled   bool      `json:"enabled"`
	Verified  bool      `json:"verified"`
	UpdatedAt time.Time `json:"updated_at"`
}
