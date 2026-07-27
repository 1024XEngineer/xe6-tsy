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
// and the start request's idempotent result. Implementations check an existing
// key and request hash before Expected so an already-active replay can return
// its stored result without invoking realtime again.
type StartTransitionParams struct {
	SessionID      string
	AccountID      string
	Expected       Status
	StartedAt      time.Time
	IdempotencyKey string
	RequestHash    string
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

// EndIntent persists a requested shutdown before cross-service cleanup is
// confirmed. CompletedAt distinguishes a resumable intent from an audited,
// completed request without deleting its idempotency record.
type EndIntent struct {
	SessionID      string
	AccountID      string
	Reason         EndReason
	IdempotencyKey string
	RequestHash    string
	RequestedAt    time.Time
	CompletedAt    *time.Time
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
// Implementations atomically own create and start idempotency, while EndIntent
// exclusively owns end idempotency. GetOwned must not reveal whether a missing
// session belongs to another account. AccountID is mandatory on every external
// read or mutation, and Expected changes return ErrConcurrentTransition.
type Repository interface {
	Create(ctx context.Context, params CreateParams) (session VoiceSession, replayed bool, err error)
	GetOwned(ctx context.Context, accountID string, sessionID string) (VoiceSession, error)
	List(ctx context.Context, filter ListFilter) (ListPage, error)
	SaveEndIntent(ctx context.Context, intent EndIntent) (saved EndIntent, replayed bool, err error)
	GetEndIntent(ctx context.Context, accountID string, sessionID string) (EndIntent, error)
	CompleteEndIntent(ctx context.Context, accountID string, sessionID string, completedAt time.Time) error
	TransitionToActive(ctx context.Context, params StartTransitionParams) (session VoiceSession, replayed bool, err error)
	TransitionToEnded(ctx context.Context, params EndTransitionParams) (VoiceSession, error)
	TransitionToFailed(ctx context.Context, params FailureTransitionParams) (VoiceSession, error)
}

// RealtimeLifecycle is the only media-plane lifecycle dependency used by
// session management. Start accepts a still-created business session.
type RealtimeLifecycle interface {
	Start(ctx context.Context, command StartRealtimeCommand) (RuntimeSnapshot, error)
	Stop(ctx context.Context, command StopRealtimeCommand) (RuntimeSnapshot, error)
	GetRuntimeState(ctx context.Context, sessionID string) (RuntimeSnapshot, error)
}

// StartRealtimeCommand carries trace and actor information across the service boundary.
type StartRealtimeCommand struct {
	SessionID string
	TraceID   string
	StartedBy string
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

// IDGenerator and Clock keep ID and time creation deterministic in unit tests.
type IDGenerator interface {
	NewVoiceSessionID() string
}

// Clock provides UTC timestamps without coupling services to wall-clock time.
type Clock interface {
	Now() time.Time
}
