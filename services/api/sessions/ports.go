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
	Sessions   []VoiceSession
	NextCursor *string
}

// TransitionParams describes a conditional business-state update.
type TransitionParams struct {
	SessionID      string
	AccountID      string
	Expected       Status
	Target         Status
	StartedAt      *time.Time
	EndedAt        *time.Time
	EndReason      *EndReason
	IdempotencyKey string
	RequestHash    string
}

// EndIntent persists a requested shutdown before cross-service cleanup is
// confirmed, allowing a worker or repeated request to retry idempotent Stop.
type EndIntent struct {
	SessionID      string
	AccountID      string
	Reason         EndReason
	IdempotencyKey string
	RequestHash    string
	RequestedAt    time.Time
}

// Repository owns voice_sessions persistence and operation idempotency.
// Implementations must make each create or transition and its idempotency
// record atomic, retain failed end attempts, and report ErrConcurrentTransition
// when Expected changed.
type Repository interface {
	Create(ctx context.Context, params CreateParams) (session VoiceSession, replayed bool, err error)
	Get(ctx context.Context, sessionID string) (VoiceSession, error)
	List(ctx context.Context, filter ListFilter) (ListPage, error)
	SaveEndIntent(ctx context.Context, intent EndIntent) (replayed bool, err error)
	Transition(ctx context.Context, params TransitionParams) (session VoiceSession, replayed bool, err error)
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

// LanguageConfigSnapshot is sufficient to verify an active two-language config.
type LanguageConfigSnapshot struct {
	SessionID         string
	Version           int
	LanguagePairCount int
	Status            string
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
