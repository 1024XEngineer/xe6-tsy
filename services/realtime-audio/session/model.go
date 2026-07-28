package session

import (
	"encoding/json"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// RuntimeState describes media-plane progress independently of business state.
type RuntimeState = realtimev1.RuntimeState

const (
	RuntimeStopped       = realtimev1.RuntimeStopped
	RuntimeStarting      = realtimev1.RuntimeStarting
	RuntimeListening     = realtimev1.RuntimeListening
	RuntimeASRProcessing = realtimev1.RuntimeASRProcessing
	RuntimeTranslating   = realtimev1.RuntimeTranslating
	RuntimeTTSProcessing = realtimev1.RuntimeTTSProcessing
	RuntimePlaying       = realtimev1.RuntimePlaying
	RuntimeStopping      = realtimev1.RuntimeStopping
	RuntimeFailed        = realtimev1.RuntimeFailed
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

// RuntimeSnapshot is the authoritative shared media-plane state for one session.
type RuntimeSnapshot = realtimev1.RuntimeSnapshot

// RuntimeStateUpdate carries one pipeline-owned progress transition into lifecycle state.
type RuntimeStateUpdate struct {
	SessionID         string
	RuntimeState      RuntimeState
	CurrentTurnID     *string
	CurrentPlaybackID *string
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
