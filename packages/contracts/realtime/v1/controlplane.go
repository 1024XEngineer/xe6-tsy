package realtimev1

import "time"

// ControlPlaneErrorCode is a stable cross-service error identifier returned
// by realtime control-plane lifecycle operations.
type ControlPlaneErrorCode string

const (
	ErrorRuntimeOperationConflict    ControlPlaneErrorCode = "runtime_operation_conflict"
	ErrorModeNotAvailable            ControlPlaneErrorCode = "mode_not_available"
	ErrorModeGenerationConflict      ControlPlaneErrorCode = "mode_generation_conflict"
	ErrorModeRuntimeInstanceMismatch ControlPlaneErrorCode = "mode_runtime_instance_mismatch"
	ErrorModeOperationConflict       ControlPlaneErrorCode = "mode_operation_conflict"
)

// FallbackPlaybackReceiptStatus distinguishes a newly accepted command from
// an idempotent replay of an operation accepted earlier.
type FallbackPlaybackReceiptStatus string

const (
	FallbackPlaybackAccepted        FallbackPlaybackReceiptStatus = "accepted"
	FallbackPlaybackAlreadyAccepted FallbackPlaybackReceiptStatus = "already_accepted"
)

// StartRequest binds a durable control-plane operation to one media runtime.
// OperationID is generated and persisted by the Session service before this
// request crosses the realtime boundary.
type StartRequest struct {
	OperationID string `json:"operation_id"`
	TraceID     string `json:"trace_id"`
	StartedBy   string `json:"started_by"`
	// InitialMode is optional for rolling compatibility. Empty means interpretation.
	InitialMode Mode `json:"initial_mode,omitempty"`
}

// StopRequest binds a business End intent to realtime cleanup confirmation.
type StopRequest struct {
	TraceID string    `json:"trace_id"`
	Reason  string    `json:"reason"`
	EndedAt time.Time `json:"ended_at"`
}

// FallbackPlaybackRequest asks realtime to play one immutable translated-text
// snapshot after every initial automatic delivery target failed.
type FallbackPlaybackRequest struct {
	OperationID           string `json:"operation_id"`
	SessionID             string `json:"session_id"`
	TurnID                string `json:"turn_id"`
	TargetLanguage        string `json:"target_language"`
	TranslatedText        string `json:"translated_text"`
	LanguageConfigVersion int    `json:"language_config_version"`
	TraceID               string `json:"trace_id"`
}

// FallbackPlaybackReceipt confirms durable acceptance. Repeating the same
// operation ID must return AlreadyAccepted without starting playback again.
type FallbackPlaybackReceipt struct {
	OperationID string                        `json:"operation_id"`
	Status      FallbackPlaybackReceiptStatus `json:"status"`
}
