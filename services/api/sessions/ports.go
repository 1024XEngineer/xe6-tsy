package sessions

import (
	"context"
	"time"
)

// CreateParams carries authenticated ownership and idempotency metadata to the
// repository. Create must atomically reject a reused key with a different hash.
type CreateParams struct {
	ID             string
	AccountID      string
	AudioConfig    AudioConfig
	Capabilities   Capabilities
	IdempotencyKey string
	RequestHash    string
	CreatedAt      time.Time
}

// ListFilter uses an opaque cursor and never requests runtime state.
type ListFilter struct {
	AccountID string
	Status    *Status
	Cursor    string
	Limit     int
}

// ListPage contains persistent sessions only, ordered by created_at and ID.
type ListPage struct {
	Sessions   []VoiceSessionListItem `json:"sessions"`
	NextCursor *string                `json:"next_cursor"`
}

// StartTransitionParams atomically records the created-to-active transition
// and completes the matching pending StartOperation. Implementations check the
// operation, key, and request hash before Expected so an already-active replay
// can return its stored result without invoking realtime again.
type StartTransitionParams struct {
	SessionID      string
	AccountID      string
	OperationID    string
	Expected       Status
	StartedAt      time.Time
	IdempotencyKey string
	RequestHash    string
}

// BeginStartOperationParams creates a durable pending operation before the
// realtime boundary is called.
type BeginStartOperationParams struct {
	OperationID    string
	SessionID      string
	AccountID      string
	IdempotencyKey string
	RequestHash    string
	CreatedAt      time.Time
}

// BeginStartOperationResult distinguishes a newly-created operation from an
// idempotent replay of the same key and request hash.
type BeginStartOperationResult struct {
	Operation StartOperation
	Replayed  bool
}

// ClaimStartCompensationParams asks the repository for exclusive permission to
// stop realtime after activation could not be confirmed.
type ClaimStartCompensationParams struct {
	SessionID   string
	AccountID   string
	OperationID string
	ClaimID     string
	ClaimedAt   time.Time
}

// ClaimStartCompensationResult is the only authority for destructive Start
// compensation. A pending operation may be claimed once. If it is already
// compensating, the same persisted ClaimID reclaims it idempotently so
// interrupted cleanup can resume; a different ClaimID never takes ownership.
// Claimed=false always forbids Realtime.Stop.
type ClaimStartCompensationResult struct {
	Claimed bool
	Reason  StartCompensationClaimReason
}

// CompleteStartCompensationParams records cleanup confirmed by realtime.
type CompleteStartCompensationParams struct {
	SessionID   string
	AccountID   string
	OperationID string
	ClaimID     string
	CompletedAt time.Time
}

// FailStartCompensationParams preserves a failed cleanup attempt for recovery.
type FailStartCompensationParams struct {
	SessionID   string
	AccountID   string
	OperationID string
	ClaimID     string
	FailedAt    time.Time
}

// EndTransitionParams records a cleanup-confirmed transition to ended. End
// request idempotency belongs to EndIntent and is deliberately absent here.
type EndTransitionParams struct {
	SessionID string
	AccountID string
	Expected  Status
	EndedAt   time.Time
	EndReason EndReason
}

// FailureTransitionParams records an unrecoverable active-session failure only
// after realtime confirms that every owned resource has been cleaned up.
type FailureTransitionParams struct {
	SessionID string
	AccountID string
	Expected  Status
	FailedAt  time.Time
	ErrorCode string
}

// EndIntent stores one End request identity before realtime cleanup.
// CompletedAt marks that the corresponding business transition was committed.
type EndIntent struct {
	SessionID      string
	AccountID      string
	Reason         EndReason
	IdempotencyKey string
	RequestHash    string
	TraceID        string
	RequestedAt    time.Time
	CompletedAt    *time.Time
	RetryCount     int
	LastError      *string
	NextAttemptAt  time.Time
	RecoveryOwner  *string
	LeaseExpiresAt *time.Time
}

// ClaimEndIntentParams grants one worker a bounded lease on the next due
// unfinished intent. WorkerID must be unique for one running API instance.
type ClaimEndIntentParams struct {
	WorkerID       string
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
}

// RetryEndIntentParams releases a claimed intent after a failed recovery step
// and persists the bounded-backoff schedule.
type RetryEndIntentParams struct {
	SessionID     string
	AccountID     string
	WorkerID      string
	LastError     string
	NextAttemptAt time.Time
}

// CompleteClaimedEndIntentParams completes an intent only for its current
// recovery owner. Completion is idempotent if another request already won.
type CompleteClaimedEndIntentParams struct {
	SessionID   string
	AccountID   string
	WorkerID    string
	CompletedAt time.Time
}

// MatchesRequest reports whether a repeated end request is an idempotent replay.
// A reused key with a different hash must be reported as a conflict.
func (i EndIntent) MatchesRequest(idempotencyKey string, requestHash string) bool {
	return i.IdempotencyKey == idempotencyKey && i.RequestHash == requestHash
}

// Completed reports whether cleanup and the terminal transition were confirmed.
func (i EndIntent) Completed() bool {
	return i.CompletedAt != nil
}

// Repository owns voice_sessions persistence and operation idempotency.
// Implementations atomically bind StartOperation activation or compensation to
// the business session, while EndIntent exclusively owns end idempotency.
// GetOwned must not reveal whether a missing session belongs to another
// account. AccountID is mandatory on every external read or mutation, and
// Expected changes return ErrConcurrentTransition.
type Repository interface {
	Create(ctx context.Context, params CreateParams) (session VoiceSession, replayed bool, err error)
	GetOwned(ctx context.Context, accountID string, sessionID string) (VoiceSession, error)
	// GetSession is a trusted internal read used when no user actor exists,
	// such as a realtime failure notification. It returns the immutable owner.
	GetSession(ctx context.Context, sessionID string) (SessionSnapshot, error)
	List(ctx context.Context, filter ListFilter) (ListPage, error)
	// GetStartOperation returns the matching request when present. If another
	// key owns a pending, compensating, or compensation_failed operation for the
	// Session, it returns ErrSessionStartInProgress before readiness is checked.
	// A compensated operation does not block a new key and is reported as
	// ErrStartOperationNotFound. Implementations must also make
	// BeginStartOperation conflict with an incomplete EndIntent so a new
	// runtime cannot start after shutdown persistence begins.
	GetStartOperation(
		ctx context.Context,
		accountID string,
		sessionID string,
		idempotencyKey string,
	) (StartOperation, error)
	BeginStartOperation(ctx context.Context, params BeginStartOperationParams) (BeginStartOperationResult, error)
	// ClaimStartCompensation is idempotent for the matching OperationID and
	// persisted ClaimID while the operation remains compensating.
	ClaimStartCompensation(ctx context.Context, params ClaimStartCompensationParams) (ClaimStartCompensationResult, error)
	CompleteStartCompensation(ctx context.Context, params CompleteStartCompensationParams) error
	FailStartCompensation(ctx context.Context, params FailStartCompensationParams) error
	// SaveEndIntent atomically creates or replays the session's EndIntent. A
	// different request identity or an unexpired execution lease conflicts. An
	// unfinished StartOperation returns ErrSessionStartInProgress so
	// created -> ended cannot orphan a runtime.
	SaveEndIntent(ctx context.Context, intent EndIntent) (saved EndIntent, replayed bool, err error)
	GetEndIntent(ctx context.Context, accountID string, sessionID string) (EndIntent, error)
	ClaimPendingEndIntent(ctx context.Context, params ClaimEndIntentParams) (intent EndIntent, claimed bool, err error)
	RetryClaimedEndIntent(ctx context.Context, params RetryEndIntentParams) error
	CompleteClaimedEndIntent(ctx context.Context, params CompleteClaimedEndIntentParams) error
	// CompleteEndIntent is idempotent after the business transition commits.
	CompleteEndIntent(ctx context.Context, accountID string, sessionID string, completedAt time.Time) error
	TransitionToActive(ctx context.Context, params StartTransitionParams) (session VoiceSession, replayed bool, err error)
	TransitionToEnded(ctx context.Context, params EndTransitionParams) (VoiceSession, error)
	TransitionToFailed(ctx context.Context, params FailureTransitionParams) (VoiceSession, error)
}

// RealtimeLifecycle is the only media-plane lifecycle dependency used by
// session management. Start accepts a still-created business session. Calls
// with the same SessionID and OperationID are idempotent and return the latest
// snapshot for that runtime; a different OperationID must not claim an existing
// runtime and returns ErrRealtimeAlreadyRunning or ErrConcurrentTransition.
// Stop is idempotent for one SessionID and EndReason. Success confirms all
// owned resources are cleaned and returns a valid RuntimeStopped snapshot.
type RealtimeLifecycle interface {
	Start(ctx context.Context, command StartRealtimeCommand) (RuntimeSnapshot, error)
	Stop(ctx context.Context, command StopRealtimeCommand) (RuntimeSnapshot, error)

	// GetRuntimeState returns ErrRuntimeSnapshotNotFound when the realtime
	// dependency is reachable but no runtime record exists. Adapters must map
	// provider-specific missing-runtime errors to that sentinel. Cancellation,
	// deadline, not-implemented, and other dependency failures must remain
	// distinguishable for the session service's boundary mapping.
	GetRuntimeState(ctx context.Context, sessionID string) (RuntimeSnapshot, error)
}

// StartRealtimeCommand binds one durable operation to the runtime it creates.
type StartRealtimeCommand struct {
	SessionID   string
	OperationID string
	TraceID     string
	StartedBy   string
}

// StopRealtimeCommand carries the requested shutdown reason and timestamp.
type StopRealtimeCommand struct {
	SessionID string
	TraceID   string
	Reason    EndReason
	EndedAt   time.Time
}

// LanguageConfigReader supplies the minimum bilingual readiness snapshot.
type LanguageConfigReader interface {
	GetCurrentConfig(ctx context.Context, sessionID string) (LanguageConfigSnapshot, error)
}

// LanguageConfigSnapshot is the consumer-owned readiness projection. The
// language adapter derives LanguagePairCount from Issue #88's validated pairs.
type LanguageConfigSnapshot struct {
	SessionID         string
	Version           int
	LanguagePairCount int
	Status            LanguageConfigStatus
}

// Ready reports whether the P0 session has the active two-direction language
// configuration required by Issues #86 and #88.
func (s LanguageConfigSnapshot) Ready() bool {
	return s.Status == LanguageConfigActive && s.LanguagePairCount == 2
}

// WebRTCConnectionReader reads connection readiness without conflating it with runtime state.
type WebRTCConnectionReader interface {
	GetConnectionState(ctx context.Context, sessionID string) (WebRTCConnectionSnapshot, error)
}

// SessionReader is the read-only port provided to realtime-audio and other modules.
type SessionReader interface {
	GetSession(ctx context.Context, sessionID string) (SessionSnapshot, error)
}

// RuntimeFailure records an unrecoverable media failure only after realtime
// confirms that every owned runtime resource has been cleaned up.
type RuntimeFailure struct {
	SessionID  string
	TraceID    string
	ErrorCode  string
	OccurredAt time.Time
}

// RuntimeFailureConsumer is provided by this module to the realtime adapter.
// Implementations serialize it with Start and End for the same session.
type RuntimeFailureConsumer interface {
	ConsumeRuntimeFailure(ctx context.Context, failure RuntimeFailure) error
}

// IDGenerator creates stable entity and operation identities. The two ID kinds
// remain separate so persistence and compensation claims can be audited.
type IDGenerator interface {
	NewVoiceSessionID() string
	NewStartOperationID() string
}

// Clock provides UTC timestamps without coupling services to wall-clock time.
type Clock interface {
	Now() time.Time
}
