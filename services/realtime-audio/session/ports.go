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

// PipelineManager owns processing contexts created for a realtime session.
type PipelineManager interface {
	Start(ctx context.Context, snapshot SessionSnapshot) error
	Stop(ctx context.Context, sessionID string) error
}

// WebRTCConnectionManager closes all connection resources for a session.
type WebRTCConnectionManager interface {
	Close(ctx context.Context, sessionID string) error
}
