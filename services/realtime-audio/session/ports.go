package session

import "context"

// SessionReader reads business state without allowing realtime code to mutate it.
type SessionReader interface {
	GetSession(ctx context.Context, sessionID string) (SessionSnapshot, error)
}

// LanguageConfigReader returns the active configuration for a session.
// Member 3 allocates Turn IDs, so this boundary never accepts or returns one.
type LanguageConfigReader interface {
	GetCurrentConfig(ctx context.Context, sessionID string) (LanguageConfigSnapshot, error)
}

// RuntimeRepository persists the authoritative runtime snapshot.
type RuntimeRepository interface {
	Get(ctx context.Context, sessionID string) (RuntimeSnapshot, error)
	Save(ctx context.Context, snapshot RuntimeSnapshot) error
}

// RuntimeStateReporter is the narrow processing-state boundary exposed to the pipeline.
// LifecycleService implements it so reporting and Start/Stop share one per-session lock.
type RuntimeStateReporter interface {
	SetProcessingState(ctx context.Context, update ProcessingStateUpdate) error
}

// RuntimeFailureReporter persists a terminal media-pipeline failure.
// Processing failures intentionally carry no new public error code until the
// shared RuntimeErrorCode contract is frozen; RuntimeFailed is authoritative.
type RuntimeFailureReporter interface {
	SetRuntimeFailed(ctx context.Context, sessionID string) error
}

// PipelineManager owns processing contexts created for a realtime session.
// Stop must be idempotent: an already stopped or absent pipeline returns nil.
type PipelineManager interface {
	Start(ctx context.Context, snapshot SessionSnapshot) error
	Stop(ctx context.Context, sessionID string) error
}

// PipelineActivator is an optional second phase for managers that must wait
// until LifecycleService has persisted RuntimeListening before reading media.
// The operation ID prevents a stale activation from starting a replacement worker.
// Existing PipelineManager implementations remain valid without this hook.
type PipelineActivator interface {
	Activate(ctx context.Context, sessionID string, operationID string) error
}

// PipelineHealthReader lets lifecycle recovery distinguish a live worker from
// a persisted active state left behind by a terminated manager entry.
type PipelineHealthReader interface {
	PipelineActive(sessionID string) bool
}

// WebRTCConnectionManager closes all connection resources for a session.
// Close must be idempotent so lifecycle retries can finish partial cleanup.
type WebRTCConnectionManager interface {
	Close(ctx context.Context, sessionID string) error
}
