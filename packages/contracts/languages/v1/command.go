package languagesv1

import (
	"errors"
	"strings"
)

var (
	ErrInvalidCommandConfigRequest  = errors.New("invalid command language configuration request")
	ErrInvalidCommandConfigSnapshot = errors.New("invalid command language configuration snapshot")
)

// MaxCommandIDLength matches the 128-character realtime command ID bound. The
// API scopes its idempotency key with a fixed-length hash, so no prefix budget
// is deducted from this contract limit.
const MaxCommandIDLength = 128

// CommandConfigRequest is the internal control-plane contract used by realtime after a semantic
// command has passed deterministic validation. CommandID is the stable idempotency identity.
type CommandConfigRequest struct {
	SessionID       string                   `json:"session_id"`
	CommandID       string                   `json:"command_id"`
	SourceLanguage  string                   `json:"source_language"`
	TargetLanguage  string                   `json:"target_language"`
	OutputMode      InterpretationOutputMode `json:"output_mode"`
	ExpectedVersion *int                     `json:"expected_version,omitempty"`
}

// Validate rejects partial language directions before they cross the service boundary.
func (r CommandConfigRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" || strings.TrimSpace(r.CommandID) == "" || len(r.CommandID) > MaxCommandIDLength ||
		strings.TrimSpace(r.SourceLanguage) == "" || strings.TrimSpace(r.TargetLanguage) == "" ||
		strings.EqualFold(strings.TrimSpace(r.SourceLanguage), strings.TrimSpace(r.TargetLanguage)) ||
		(r.OutputMode != "" && !r.OutputMode.Valid()) || (r.ExpectedVersion != nil && *r.ExpectedVersion <= 0) {
		return ErrInvalidCommandConfigRequest
	}
	return nil
}

// CommandConfigResult identifies the active API-owned snapshot created or replayed
// while the command's configuration remains current. A stale replay is rejected.
type CommandConfigResult struct {
	SessionID      string                   `json:"session_id"`
	CommandID      string                   `json:"command_id"`
	SourceLanguage string                   `json:"source_language"`
	TargetLanguage string                   `json:"target_language"`
	OutputMode     InterpretationOutputMode `json:"output_mode"`
	Version        int                      `json:"version"`
}

// CommandConfigSnapshot is the API-owned active language direction read by
// realtime before it applies a voice command.
type CommandConfigSnapshot struct {
	SessionID      string                   `json:"session_id"`
	SourceLanguage string                   `json:"source_language"`
	TargetLanguage string                   `json:"target_language"`
	OutputMode     InterpretationOutputMode `json:"output_mode"`
	Version        int                      `json:"version"`
}

// Validate rejects incomplete command snapshots at the service boundary.
func (s CommandConfigSnapshot) Validate() error {
	if strings.TrimSpace(s.SessionID) == "" || strings.TrimSpace(s.SourceLanguage) == "" ||
		strings.TrimSpace(s.TargetLanguage) == "" ||
		strings.EqualFold(strings.TrimSpace(s.SourceLanguage), strings.TrimSpace(s.TargetLanguage)) ||
		!s.OutputMode.Valid() || s.Version <= 0 {
		return ErrInvalidCommandConfigSnapshot
	}
	return nil
}
