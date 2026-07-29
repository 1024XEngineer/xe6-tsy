package sessions

import "errors"

// ErrorCode is a stable machine-readable session module failure.
type ErrorCode string

const (
	CodeInvalidDependency      ErrorCode = "invalid_dependency"
	CodeInvalidRequest         ErrorCode = "invalid_request"
	CodeUnauthorized           ErrorCode = "unauthorized"
	CodeVoiceSessionNotFound   ErrorCode = "voice_session_not_found"
	CodeEndIntentNotFound      ErrorCode = "end_intent_not_found"
	CodeSessionStateConflict   ErrorCode = "session_state_conflict"
	CodeIdempotencyKeyConflict ErrorCode = "idempotency_key_conflict"
	CodeSessionStartInProgress ErrorCode = "session_start_in_progress"
	CodeLanguageConfigNotReady ErrorCode = "language_config_not_ready"
	CodeWebRTCNotReady         ErrorCode = "webrtc_not_ready"
	CodeRealtimeAlreadyRunning ErrorCode = "realtime_already_running"
	CodeUnsupportedAudio       ErrorCode = "unsupported_audio_config"
	CodeRealtimeStartFailed    ErrorCode = "realtime_start_failed"
	CodeRealtimeStopFailed     ErrorCode = "realtime_stop_failed"
	CodeRuntimeUnavailable     ErrorCode = "runtime_state_unavailable"
	CodeWebRTCUnavailable      ErrorCode = "webrtc_state_unavailable"
	CodeNotImplemented         ErrorCode = "not_implemented"
)

var (
	ErrInvalidDependency      = errors.New(string(CodeInvalidDependency))
	ErrInvalidRequest         = errors.New(string(CodeInvalidRequest))
	ErrUnauthorized           = errors.New(string(CodeUnauthorized))
	ErrVoiceSessionNotFound   = errors.New(string(CodeVoiceSessionNotFound))
	ErrEndIntentNotFound      = errors.New(string(CodeEndIntentNotFound))
	ErrSessionStateConflict   = errors.New(string(CodeSessionStateConflict))
	ErrConcurrentTransition   = ErrSessionStateConflict
	ErrIdempotencyKeyConflict = errors.New(string(CodeIdempotencyKeyConflict))
	ErrSessionStartInProgress = errors.New(string(CodeSessionStartInProgress))
	ErrLanguageConfigNotReady = errors.New(string(CodeLanguageConfigNotReady))
	ErrWebRTCNotReady         = errors.New(string(CodeWebRTCNotReady))
	ErrRealtimeAlreadyRunning = errors.New(string(CodeRealtimeAlreadyRunning))
	ErrUnsupportedAudio       = errors.New(string(CodeUnsupportedAudio))
	ErrRealtimeStartFailed    = errors.New(string(CodeRealtimeStartFailed))
	ErrRealtimeStopFailed     = errors.New(string(CodeRealtimeStopFailed))
	ErrRuntimeUnavailable     = errors.New(string(CodeRuntimeUnavailable))
	ErrWebRTCUnavailable      = errors.New(string(CodeWebRTCUnavailable))
	// ErrRuntimeSnapshotNotFound is an adapter-only signal whose query meaning
	// depends on the persisted business state.
	ErrRuntimeSnapshotNotFound = errors.New("sessions: runtime snapshot not found")
	// ErrStartOperationNotFound is an internal repository signal used to
	// distinguish a new Start request from recovery of a durable operation.
	ErrStartOperationNotFound = errors.New("sessions: start operation not found")
	ErrNotImplemented         = errors.New(string(CodeNotImplemented))
)
