package languagesv1

import (
	"errors"
	"strings"
)

var ErrInvalidCommandConfigRequest = errors.New("invalid command language configuration request")

const MaxCommandIDLength = 120

// CommandConfigRequest is the internal control-plane contract used by realtime after a semantic
// command has passed deterministic validation. CommandID is the stable idempotency identity.
type CommandConfigRequest struct {
	SessionID      string `json:"session_id"`
	CommandID      string `json:"command_id"`
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
}

// Validate rejects partial language directions before they cross the service boundary.
func (r CommandConfigRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" || strings.TrimSpace(r.CommandID) == "" || len(r.CommandID) > MaxCommandIDLength ||
		strings.TrimSpace(r.SourceLanguage) == "" || strings.TrimSpace(r.TargetLanguage) == "" ||
		strings.EqualFold(strings.TrimSpace(r.SourceLanguage), strings.TrimSpace(r.TargetLanguage)) {
		return ErrInvalidCommandConfigRequest
	}
	return nil
}

// CommandConfigResult identifies the active API-owned snapshot created or replayed
// while the command's configuration remains current. A stale replay is rejected.
type CommandConfigResult struct {
	SessionID string `json:"session_id"`
	CommandID string `json:"command_id"`
	Version   int    `json:"version"`
}
