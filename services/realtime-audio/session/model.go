package session

import (
	"encoding/json"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// RuntimeState describes media-plane progress independently of business state.
type RuntimeState = realtimev1.RuntimeState

const (
	RuntimeStopped             = realtimev1.RuntimeStopped
	RuntimeStarting            = realtimev1.RuntimeStarting
	RuntimeListening           = realtimev1.RuntimeListening
	RuntimeASRProcessing       = realtimev1.RuntimeASRProcessing
	RuntimeTranslating         = realtimev1.RuntimeTranslating
	RuntimeThinking            = realtimev1.RuntimeThinking
	RuntimeAssistantProcessing = realtimev1.RuntimeAssistantProcessing
	RuntimeTTSProcessing       = realtimev1.RuntimeTTSProcessing
	RuntimePlaying             = realtimev1.RuntimePlaying
	RuntimeStopping            = realtimev1.RuntimeStopping
	RuntimeFailed              = realtimev1.RuntimeFailed
)

// SessionSnapshot is the read-only business session view supplied by member 1.
type SessionSnapshot struct {
	SessionID string
	AccountID string
	// InitialMode is captured at runtime creation; empty preserves legacy interpretation behavior.
	InitialMode realtimev1.Mode
	// StartOperationID is runtime ownership metadata copied from StartRealtimeCommand.
	// It is not business session state and is never persisted by member 3 there.
	StartOperationID string
	// TraceID is runtime request metadata copied from StartRealtimeCommand.
	// It is not business session state and is never persisted by member 3.
	TraceID      string
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

// OutputRoute controls independent TTS and delivery behavior for one target
// language captured at Turn start.
type OutputRoute struct {
	TargetLanguage  string
	TTSEnabled      bool
	DeliveryEnabled bool
}

// LanguageConfigSnapshot is captured once by member 3 when a Turn begins.
type LanguageConfigSnapshot struct {
	SessionID     string
	Version       int64
	LanguagePairs []LanguagePair
	OutputRoutes  []OutputRoute
	Status        string
	UpdatedAt     time.Time
}

// RuntimeSnapshot is the authoritative shared media-plane state for one session.
type RuntimeSnapshot = realtimev1.RuntimeSnapshot

// ProcessingStateUpdate carries one pipeline-owned progress transition into lifecycle state.
type ProcessingStateUpdate struct {
	SessionID         string
	RuntimeState      RuntimeState
	CurrentTurnID     *string
	CurrentPlaybackID *string
	// Expected identities are compare-only cleanup preconditions and are never persisted.
	// A mismatch means a newer Turn or playback already owns the Runtime state.
	ExpectedTurnID     *string
	ExpectedPlaybackID *string
}

// StartRealtimeCommand binds one durable control-plane operation to startup.
type StartRealtimeCommand struct {
	SessionID   string
	OperationID string
	TraceID     string
	StartedBy   string
	InitialMode realtimev1.Mode
}

// StopRealtimeCommand carries the requested shutdown reason and timestamp.
type StopRealtimeCommand struct {
	SessionID string
	TraceID   string
	Reason    string
	EndedAt   time.Time
}
