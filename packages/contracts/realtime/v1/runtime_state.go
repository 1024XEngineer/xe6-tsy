package realtimev1

import "time"

// RuntimeState is the shared media-pipeline state and is independent of business and WebRTC state.
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

// Valid reports whether the state belongs to the public media-runtime contract.
func (s RuntimeState) Valid() bool {
	switch s {
	case RuntimeStopped, RuntimeStarting, RuntimeListening, RuntimeASRProcessing,
		RuntimeTranslating, RuntimeTTSProcessing, RuntimePlaying, RuntimeStopping,
		RuntimeFailed:
		return true
	default:
		return false
	}
}

// RuntimeSnapshot is the authoritative media-plane state read by the control plane.
type RuntimeSnapshot struct {
	SessionID string `json:"session_id"`
	// StartOperationID identifies the durable Start operation that owns this runtime.
	StartOperationID  string       `json:"start_operation_id"`
	RuntimeState      RuntimeState `json:"runtime_state"`
	CurrentTurnID     *string      `json:"current_turn_id"`
	CurrentPlaybackID *string      `json:"current_playback_id"`
	LastErrorCode     *string      `json:"last_error_code"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

// RuntimeErrorCode is stored in RuntimeSnapshot.LastErrorCode after lifecycle failures.
type RuntimeErrorCode string

const (
	RuntimeErrorStartFailed         RuntimeErrorCode = "realtime_start_failed"
	RuntimeErrorStopFailed          RuntimeErrorCode = "realtime_stop_failed"
	RuntimeErrorPipelineFailed      RuntimeErrorCode = "realtime_pipeline_failed"
	RuntimeErrorTranslationRejected RuntimeErrorCode = "realtime_translation_rejected"
)

// Valid reports whether the code belongs to the public runtime-error contract.
func (c RuntimeErrorCode) Valid() bool {
	switch c {
	case RuntimeErrorStartFailed, RuntimeErrorStopFailed, RuntimeErrorPipelineFailed, RuntimeErrorTranslationRejected:
		return true
	default:
		return false
	}
}
