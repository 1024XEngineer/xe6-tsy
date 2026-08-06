package sessions

import (
	"encoding/json"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
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

// StartOperationStatus is the repository-owned lifecycle for one durable Start
// attempt. It is the cross-instance authority for activation and compensation.
type StartOperationStatus string

const (
	StartOperationPending            StartOperationStatus = "pending"
	StartOperationCompensating       StartOperationStatus = "compensating"
	StartOperationCompleted          StartOperationStatus = "completed"
	StartOperationCompensated        StartOperationStatus = "compensated"
	StartOperationCompensationFailed StartOperationStatus = "compensation_failed"
)

// StartCompensationClaimReason explains why a repository denied compensation.
// Service code must treat every denied reason as a strict prohibition on Stop.
type StartCompensationClaimReason string

const (
	StartCompensationSessionNotCreated   StartCompensationClaimReason = "session_not_created"
	StartCompensationOperationMismatch   StartCompensationClaimReason = "operation_mismatch"
	StartCompensationOperationNotPending StartCompensationClaimReason = "operation_not_pending"
)

// RuntimeState is the shared media-plane lifecycle state returned by realtime-audio.
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

// ConnectionState is the WebRTC connection lifecycle owned by the connection
// manager. It must not be used as a substitute for RuntimeState.
type ConnectionState string

const (
	ConnectionNew          ConnectionState = "new"
	ConnectionConnecting   ConnectionState = "connecting"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionDisconnected ConnectionState = "disconnected"
	ConnectionFailed       ConnectionState = "failed"
	ConnectionClosed       ConnectionState = "closed"
)

// LanguageConfigStatus is the persisted language-config lifecycle exposed by
// the language module through an adapter.
type LanguageConfigStatus string

const (
	LanguageConfigActive     LanguageConfigStatus = "active"
	LanguageConfigSuperseded LanguageConfigStatus = "superseded"
	LanguageConfigExpired    LanguageConfigStatus = "expired"
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

// StartOperation binds one idempotent Start request to its activation and
// compensation state. CompensationClaimID identifies the request that alone
// may stop realtime while the operation is compensating.
type StartOperation struct {
	ID                  string
	SessionID           string
	AccountID           string
	IdempotencyKey      string
	RequestHash         string
	Status              StartOperationStatus
	CompensationClaimID *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// MatchesRequest reports whether a repeated Start request has the same stable
// idempotency identity.
func (o StartOperation) MatchesRequest(idempotencyKey string, requestHash string) bool {
	return o.IdempotencyKey == idempotencyKey && o.RequestHash == requestHash
}

// VoiceSessionListItem is the persistent-only list projection. Keeping this
// separate prevents list queries from loading large configuration snapshots or
// reaching into realtime and WebRTC providers.
type VoiceSessionListItem struct {
	ID        string     `json:"id"`
	AccountID string     `json:"account_id"`
	Status    Status     `json:"status"`
	StartedAt *time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// RuntimeSnapshot is the consumer-owned media-plane projection used by session
// management. StartOperationID binds a running instance to the durable Start
// operation that created it; adapters must map it explicitly.
type RuntimeSnapshot struct {
	SessionID         string       `json:"session_id"`
	StartOperationID  string       `json:"start_operation_id"`
	RuntimeState      RuntimeState `json:"runtime_state"`
	CurrentTurnID     *string      `json:"current_turn_id"`
	CurrentPlaybackID *string      `json:"current_playback_id"`
	LastErrorCode     *string      `json:"last_error_code"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

// WebRTCConnectionSnapshot is independent from RuntimeSnapshot and is used
// only to enforce the startup readiness precondition.
type WebRTCConnectionSnapshot struct {
	SessionID       string
	ConnectionID    string
	ConnectionState ConnectionState
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

// Valid reports whether the status belongs to the durable Start operation
// lifecycle.
func (s StartOperationStatus) Valid() bool {
	switch s {
	case StartOperationPending,
		StartOperationCompensating,
		StartOperationCompleted,
		StartOperationCompensated,
		StartOperationCompensationFailed:
		return true
	default:
		return false
	}
}

// Valid reports whether the state belongs to the WebRTC connection lifecycle.
func (s ConnectionState) Valid() bool {
	switch s {
	case ConnectionNew,
		ConnectionConnecting,
		ConnectionConnected,
		ConnectionDisconnected,
		ConnectionFailed,
		ConnectionClosed:
		return true
	default:
		return false
	}
}

// Ready reports whether WebRTC satisfies the session-start precondition.
func (s ConnectionState) Ready() bool {
	return s == ConnectionConnected
}

// Valid reports whether the status belongs to the language-config lifecycle
// defined by Issue #88.
func (s LanguageConfigStatus) Valid() bool {
	switch s {
	case LanguageConfigActive, LanguageConfigSuperseded, LanguageConfigExpired:
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
