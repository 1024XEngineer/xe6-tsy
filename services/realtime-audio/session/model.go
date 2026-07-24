package session

import (
	"encoding/json"
	"time"
)

// RuntimeState describes media-plane progress independently of business state.
type RuntimeState string

const (
	RuntimeStopped       RuntimeState = "stopped"
	RuntimeStarting      RuntimeState = "starting"
	RuntimeListening     RuntimeState = "listening"
	RuntimeASRProcessing RuntimeState = "asr_processing"
	RuntimeTranslating   RuntimeState = "translating"
	RuntimeTTSProcessing RuntimeState = "tts_processing"
	RuntimePlaying       RuntimeState = "playing"
	RuntimeStopping      RuntimeState = "stopping"
	RuntimeFailed        RuntimeState = "failed"
)

// SessionSnapshot is the read-only business session view supplied by member 1.
type SessionSnapshot struct {
	SessionID    string
	AccountID    string
	Status       string
	AudioConfig  json.RawMessage
	Capabilities json.RawMessage
	StartedAt    *time.Time
	EndedAt      *time.Time
}

// LanguagePair defines one allowed source-to-target translation direction.
type LanguagePair struct {
	Source string
	Target string
}

// LanguageConfigSnapshot is captured once by member 3 when a Turn begins.
type LanguageConfigSnapshot struct {
	SessionID     string
	Version       int64
	LanguagePairs []LanguagePair
	Status        string
	UpdatedAt     time.Time
}

// RuntimeSnapshot is the authoritative media-plane state for one session.
type RuntimeSnapshot struct {
	SessionID         string
	RuntimeState      RuntimeState
	CurrentTurnID     *string
	CurrentPlaybackID *string
	LastErrorCode     *string
	UpdatedAt         time.Time
}

// StartRealtimeCommand carries control-plane tracing data into startup.
type StartRealtimeCommand struct {
	SessionID string
	TraceID   string
	StartedBy string
}

// StopRealtimeCommand carries the requested shutdown reason and timestamp.
type StopRealtimeCommand struct {
	SessionID string
	TraceID   string
	Reason    string
	EndedAt   time.Time
}
