package delivery

import (
	"context"
	"errors"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
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
	// Preferences returns the current account's target-level automatic delivery settings.
	Preferences(context.Context, string) ([]Preference, error)
	// PutPreference updates whether one verified destination receives automatic delivery.
	PutPreference(context.Context, string, Channel, string, bool) (Preference, error)
	// ListMessageTargets returns account-owned destination bindings.
	ListMessageTargets(context.Context, string, *Channel) ([]MessageTarget, error)
	// BindEmailTarget verifies and stores one email destination for the account.
	BindEmailTarget(context.Context, string, string) (MessageTarget, error)
	// RequestEmailBindVerification sends a one-time bind token to the supplied inbox.
	RequestEmailBindVerification(context.Context, string, string, string) error
	// BindWeChatTarget verifies and stores one WeChat Work destination for the account.
	BindWeChatTarget(context.Context, string, string) (MessageTarget, error)
	// RevokeMessageTarget marks one verified destination as revoked.
	RevokeMessageTarget(context.Context, string, Channel, string) error
}

// MessageListingService exposes recent account-owned delivery state without
// expanding lightweight Service implementations that do not persist messages.
type MessageListingService interface {
	ListMessages(context.Context, string, int) ([]Message, error)
}

// WebhookTargetBindingService is an optional extension for deployments that
// support account-owned webhook targets.
type WebhookTargetBindingService interface {
	BindWebhookTarget(context.Context, string, string) (MessageTarget, error)
}

// AutomaticOutputStatusService exposes durable automatic-output recovery
// state without expanding lightweight Service implementations.
type AutomaticOutputStatusService interface {
	ListAutomaticOutputStatus(context.Context, string, string, int) ([]AutomaticOutputStatus, error)
}

// FinalTurnScheduler creates one immutable asynchronous message for an
// eligible Final Turn after it has been durably stored.
type FinalTurnScheduler interface {
	ScheduleFinalTurn(context.Context, string, recordsv1.FinalTurnEvent) error
}

// AutomaticTurnSchedulerRepository persists a complete automatic schedule.
type AutomaticTurnSchedulerRepository interface {
	GetAutomaticTurnRun(context.Context, string, string) (AutomaticTurnRun, error)
	ScheduleAutomaticTurn(context.Context, AutomaticTurnScheduleRecord) error
}

// AutomaticTurnRetryRepository stores and claims failed automatic target attempts.
type AutomaticTurnRetryRepository interface {
	ListAutomaticTurnRetryCandidates(context.Context, int) ([]AutomaticTurnRun, error)
	ListAutomaticTurnSettlements(context.Context, string, string) ([]AutomaticTurnSettlement, error)
	RetryAutomaticTurnTarget(context.Context, string, string, string, string) (Message, error)
}

// AutomaticTurnFallbackRepository claims and records fallback playback lifecycle state.
type AutomaticTurnFallbackRepository interface {
	ListAutomaticTurnRecoveryCandidates(context.Context, int) ([]AutomaticTurnRun, error)
	ListAutomaticTurnRestoreCandidates(context.Context, int) ([]AutomaticTurnRun, error)
	ClaimAutomaticTurnFallback(context.Context, string, string) (AutomaticTurnRun, bool, error)
	MarkAutomaticTurnFallbackPlayed(context.Context, string, string) error
	MarkAutomaticTurnRestored(context.Context, string, string) error
}

// AutomaticTurnOutputRestorer restores bidirectional output after fallback playback.
type AutomaticTurnOutputRestorer interface {
	RestoreBidirectionalOutput(context.Context, string, string, int, string) error
}

// AutomaticTurnFallbackPlayer plays the immutable translation fallback snapshot.
type AutomaticTurnFallbackPlayer interface {
	PlayFallback(context.Context, string, realtimev1.FallbackPlaybackRequest) (realtimev1.FallbackPlaybackReceipt, error)
}

// AutomaticTurnSettlementRepository stores and reads target-level outcomes.
// It is an optional extension so existing lightweight repositories remain
// source-compatible while production uses the durable implementation.
type AutomaticTurnSettlementRepository interface {
	CreateAutomaticTurnSettlement(context.Context, AutomaticTurnSettlement) error
	ListAutomaticTurnSettlements(context.Context, string, string) ([]AutomaticTurnSettlement, error)
	UpdateAutomaticTurnSettlement(context.Context, AutomaticTurnSettlement) error
}
