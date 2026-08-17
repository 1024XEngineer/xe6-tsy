package delivery

import "time"

// MaxIdempotencyKeyLength is the public contract limit for create and retry keys.
const MaxIdempotencyKeyLength = 200

// Channel identifies a supported outbound delivery mechanism.
type Channel string

const (
	// ChannelEmail sends a message to a verified email destination.
	ChannelEmail Channel = "email"
	// ChannelWeChat sends a message to a verified WeChat Work userid.
	ChannelWeChat Channel = "wechat"
)

func automaticTurnRunStatus(targetCount, settledCount, succeededCount, failedCount int) AutomaticTurnRunStatus {
	if targetCount > 0 && settledCount >= targetCount {
		switch {
		case failedCount == 0:
			return AutomaticTurnRunSucceeded
		case succeededCount == 0:
			return AutomaticTurnRunFailed
		default:
			return AutomaticTurnRunPartiallySucceeded
		}
	}
	return AutomaticTurnRunPending
}

// IsSupportedChannel reports whether a channel is accepted by the public
// delivery contract. Keep this central so HTTP and use cases cannot drift.
func IsSupportedChannel(channel Channel) bool {
	switch channel {
	case ChannelEmail, ChannelWeChat:
		return true
	default:
		return false
	}
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
	AccountID      string    `json:"account_id"`
	Channel        Channel   `json:"channel"`
	DestinationRef string    `json:"destination_ref"`
	Enabled        bool      `json:"enabled"`
	Verified       bool      `json:"verified"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// AutomaticTurnSettlementStatus is the durable aggregate state for one
// automatic delivery target of a Final Turn.
type AutomaticTurnSettlementStatus string

const (
	AutomaticTurnSettlementQueued    AutomaticTurnSettlementStatus = "queued"
	AutomaticTurnSettlementSucceeded AutomaticTurnSettlementStatus = "succeeded"
	AutomaticTurnSettlementFailed    AutomaticTurnSettlementStatus = "failed"
)

// AutomaticTurnSettlement records target-level delivery outcome without
// copying provider credentials or mutable message content.
type AutomaticTurnSettlement struct {
	AccountID      string                        `json:"account_id"`
	TurnID         string                        `json:"turn_id"`
	SessionID      string                        `json:"session_id"`
	TargetLanguage string                        `json:"target_language"`
	Channel        Channel                       `json:"channel"`
	DestinationRef string                        `json:"destination_ref"`
	Status         AutomaticTurnSettlementStatus `json:"status"`
	MessageID      string                        `json:"message_id,omitempty"`
	ErrorCode      *string                       `json:"error_code,omitempty"`
	CreatedAt      time.Time                     `json:"created_at"`
	UpdatedAt      time.Time                     `json:"updated_at"`
}

// AutomaticTurnRunStatus is the aggregate state of all automatic targets for
// one Final Turn.
type AutomaticTurnRunStatus string

const (
	AutomaticTurnRunPending            AutomaticTurnRunStatus = "pending"
	AutomaticTurnRunSucceeded          AutomaticTurnRunStatus = "succeeded"
	AutomaticTurnRunPartiallySucceeded AutomaticTurnRunStatus = "partially_succeeded"
	AutomaticTurnRunFailed             AutomaticTurnRunStatus = "failed"
	AutomaticTurnRunFallbackPending    AutomaticTurnRunStatus = "fallback_pending"
	AutomaticTurnRunFallbackPlayed     AutomaticTurnRunStatus = "fallback_played"
	AutomaticTurnRunRestored           AutomaticTurnRunStatus = "restored"
)

const maxAutomaticTargetAttempts = 2

type AutomaticTurnTrigger string

const (
	AutomaticTurnTriggerConfiguredRoute AutomaticTurnTrigger = "configured_route"
	AutomaticTurnTriggerLongSentence    AutomaticTurnTrigger = "long_sentence"
)

// AutomaticTurnRun keeps the immutable fallback snapshot and target aggregate.
type AutomaticTurnRun struct {
	AccountID             string                 `json:"account_id"`
	TurnID                string                 `json:"turn_id"`
	SessionID             string                 `json:"session_id"`
	TraceID               string                 `json:"trace_id"`
	TargetLanguage        string                 `json:"target_language"`
	TranslatedText        string                 `json:"translated_text"`
	LanguageConfigVersion int64                  `json:"language_config_version"`
	Trigger               AutomaticTurnTrigger   `json:"delivery_trigger"`
	Status                AutomaticTurnRunStatus `json:"status"`
	TargetCount           int                    `json:"target_count"`
	SettledCount          int                    `json:"settled_count"`
	SucceededCount        int                    `json:"succeeded_count"`
	FailedCount           int                    `json:"failed_count"`
	FallbackOperationID   string                 `json:"fallback_operation_id"`
	CreatedAt             time.Time              `json:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at"`
}

// AutomaticOutputStatus is the public recovery state for one automatically
// delivered Final Turn. It deliberately excludes the content snapshot and
// internal operation identifiers.
type AutomaticOutputStatus struct {
	TurnID    string                 `json:"turn_id"`
	Status    AutomaticTurnRunStatus `json:"status"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// AutomaticTargetRecord contains all rows created atomically for one target.
type AutomaticTargetRecord struct {
	Message        Message
	InitialAttempt DeliveryAttempt
	Settlement     AutomaticTurnSettlement
	IdempotencyKey string
}

// AutomaticTurnScheduleRecord contains one aggregate run and every target to
// create in the same transaction.
type AutomaticTurnScheduleRecord struct {
	Run     AutomaticTurnRun
	Targets []AutomaticTargetRecord
}
