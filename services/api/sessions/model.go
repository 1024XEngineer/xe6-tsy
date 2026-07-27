package sessions

import (
	"encoding/json"
	"time"
)

// Status is the persisted business lifecycle state owned by services/api.
// Runtime and WebRTC connection states are deliberately modeled separately.
type Status string

const (
	StatusCreated Status = "created"
	StatusActive  Status = "active"
	StatusEnded   Status = "ended"
	StatusFailed  Status = "failed"
)

// RuntimeState is the media-plane lifecycle state returned by realtime-audio.
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

// EndReason is the stable reason accepted by the session end use case.
type EndReason string

const (
	EndReasonUserRequested      EndReason = "user_requested"
	EndReasonOperatorCancelled  EndReason = "operator_cancelled"
	EndReasonClientDisconnected EndReason = "client_disconnected"
)

// AudioConfig is the client audio capability snapshot persisted at creation.
type AudioConfig struct {
	Codec            string `json:"codec"`
	SampleRateHz     int    `json:"sample_rate_hz"`
	Channels         int    `json:"channels"`
	EchoCancellation bool   `json:"echo_cancellation"`
	NoiseSuppression bool   `json:"noise_suppression"`
	AutoGainControl  bool   `json:"auto_gain_control"`
}

// DefaultAudioConfig returns the P0 browser defaults from issue #86.
func DefaultAudioConfig() AudioConfig {
	return AudioConfig{
		Codec:            "opus",
		SampleRateHz:     48000,
		Channels:         1,
		EchoCancellation: true,
		NoiseSuppression: true,
		AutoGainControl:  true,
	}
}

// Capabilities records terminal features without asserting WebRTC readiness.
type Capabilities struct {
	WebRTC             bool `json:"webrtc"`
	DataChannel        bool `json:"data_channel"`
	Microphone         bool `json:"microphone"`
	Speaker            bool `json:"speaker"`
	SpeakerDiarization bool `json:"speaker_diarization"`
}

// VoiceSession is the persistent control-plane entity. It must never contain
// runtime_state or connection_state because those belong to other modules.
type VoiceSession struct {
	ID           string          `json:"id"`
	AccountID    string          `json:"account_id"`
	Status       Status          `json:"status"`
	AudioConfig  json.RawMessage `json:"audio_config"`
	Capabilities json.RawMessage `json:"capabilities"`
	StartedAt    *time.Time      `json:"started_at"`
	EndedAt      *time.Time      `json:"ended_at"`
	CreatedAt    time.Time       `json:"created_at"`
}

// RuntimeSnapshot is the read-only media-plane state consumed by this module.
type RuntimeSnapshot struct {
	SessionID         string
	RuntimeState      RuntimeState
	CurrentTurnID     *string
	CurrentPlaybackID *string
	LastErrorCode     *string
	UpdatedAt         time.Time
}

// WebRTCConnectionSnapshot is independent from RuntimeSnapshot and is used
// only to enforce the startup readiness precondition.
type WebRTCConnectionSnapshot struct {
	SessionID       string
	ConnectionID    string
	ConnectionState string
	UpdatedAt       time.Time
}

// SessionSnapshot is the minimal read model exposed to realtime-audio.
type SessionSnapshot struct {
	SessionID    string
	AccountID    string
	Status       Status
	AudioConfig  json.RawMessage
	Capabilities json.RawMessage
	StartedAt    *time.Time
	EndedAt      *time.Time
}

// VoiceSessionDetail combines business state with a live runtime snapshot.
type VoiceSessionDetail struct {
	VoiceSession
	RuntimeState      RuntimeState `json:"runtime_state"`
	CurrentTurnID     *string      `json:"current_turn_id"`
	CurrentPlaybackID *string      `json:"current_playback_id"`
	LastErrorCode     *string      `json:"last_error_code"`
	Retryable         bool         `json:"retryable"`
	RuntimeUpdatedAt  time.Time    `json:"runtime_updated_at"`
}

// StateSnapshot is the compact response model for high-frequency polling.
type StateSnapshot struct {
	SessionID         string       `json:"session_id"`
	Status            Status       `json:"status"`
	RuntimeState      RuntimeState `json:"runtime_state"`
	CurrentTurnID     *string      `json:"current_turn_id"`
	CurrentPlaybackID *string      `json:"current_playback_id"`
	LastErrorCode     *string      `json:"last_error_code"`
	Retryable         bool         `json:"retryable"`
	RuntimeUpdatedAt  time.Time    `json:"runtime_updated_at"`
}

// Retryable reports the only retryable state combination defined by issue #86.
func Retryable(status Status, runtime RuntimeState) bool {
	return status == StatusCreated && runtime == RuntimeFailed
}

// Valid reports whether the status belongs to the persisted lifecycle.
func (s Status) Valid() bool {
	switch s {
	case StatusCreated, StatusActive, StatusEnded, StatusFailed:
		return true
	default:
		return false
	}
}

// Valid reports whether the state belongs to the media-plane lifecycle.
func (s RuntimeState) Valid() bool {
	switch s {
	case RuntimeStopped,
		RuntimeStarting,
		RuntimeListening,
		RuntimeASRProcessing,
		RuntimeTranslating,
		RuntimeTTSProcessing,
		RuntimePlaying,
		RuntimeStopping,
		RuntimeFailed:
		return true
	default:
		return false
	}
}

// Valid reports whether the reason is accepted by the end-session use case.
func (r EndReason) Valid() bool {
	switch r {
	case EndReasonUserRequested, EndReasonOperatorCancelled, EndReasonClientDisconnected:
		return true
	default:
		return false
	}
}
