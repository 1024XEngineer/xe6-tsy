package realtimev1

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	CommandResultTopic        = "command.result"
	CommandResultEventVersion = 1

	maxCommandResultIDLength      = 128
	maxCommandResultActionLength  = 64
	maxCommandResultMessageLength = 512
)

var ErrInvalidCommandResultEvent = errors.New("invalid command result event")

// CommandResultStatus identifies the terminal outcome of one wake-word command attempt.
// Delivery is informational: consumers must never replay a command from this status.
type CommandResultStatus string

const (
	CommandResultApplied               CommandResultStatus = "applied"
	CommandResultUnchanged             CommandResultStatus = "unchanged"
	CommandResultClarificationRequired CommandResultStatus = "clarification_required"
	CommandResultUnsupported           CommandResultStatus = "unsupported"
	CommandResultFailed                CommandResultStatus = "failed"
)

func (status CommandResultStatus) Valid() bool {
	switch status {
	case CommandResultApplied, CommandResultUnchanged, CommandResultClarificationRequired,
		CommandResultUnsupported, CommandResultFailed:
		return true
	default:
		return false
	}
}

// CommandResultEvent is the browser- and device-facing acknowledgement for one command ID.
// Runtime fields are absent when interpretation or validation failed before mode execution.
type CommandResultEvent struct {
	Type              string              `json:"type"`
	EventVersion      int                 `json:"event_version"`
	CommandID         string              `json:"command_id"`
	SessionID         string              `json:"session_id"`
	RuntimeInstanceID string              `json:"runtime_instance_id,omitempty"`
	Generation        int64               `json:"generation,omitempty"`
	Status            CommandResultStatus `json:"status"`
	Action            string              `json:"action,omitempty"`
	TargetMode        Mode                `json:"target_mode,omitempty"`
	Message           string              `json:"message"`
	OccurredAt        time.Time           `json:"occurred_at"`
}

// Validate keeps success acknowledgements tied to an authoritative mode snapshot while allowing
// pre-execution failures to omit fields that could not yet be determined.
func (event CommandResultEvent) Validate() error {
	switch {
	case event.Type != CommandResultTopic:
		return invalidCommandResultField("type")
	case event.EventVersion != CommandResultEventVersion:
		return invalidCommandResultField("event_version")
	case !validCommandResultText(event.CommandID, maxCommandResultIDLength):
		return invalidCommandResultField("command_id")
	case !validCommandResultText(event.SessionID, maxCommandResultIDLength):
		return invalidCommandResultField("session_id")
	case !event.Status.Valid():
		return invalidCommandResultField("status")
	case !validCommandResultText(event.Message, maxCommandResultMessageLength):
		return invalidCommandResultField("message")
	case event.OccurredAt.IsZero():
		return invalidCommandResultField("occurred_at")
	}
	if event.Action != "" && !validCommandResultText(event.Action, maxCommandResultActionLength) {
		return invalidCommandResultField("action")
	}
	if event.TargetMode != "" && !event.TargetMode.Valid() {
		return invalidCommandResultField("target_mode")
	}
	if event.Status == CommandResultApplied || event.Status == CommandResultUnchanged {
		if event.RuntimeInstanceID == "" || event.Generation < 1 || event.Action == "" || !event.TargetMode.Valid() {
			return invalidCommandResultField("successful_result")
		}
		return nil
	}
	if event.Generation < 0 || (event.RuntimeInstanceID == "") != (event.Generation == 0) {
		return invalidCommandResultField("runtime_state")
	}
	return nil
}

func validCommandResultText(value string, maxLength int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) == value && value != "" &&
		utf8.RuneCountInString(value) <= maxLength && !strings.ContainsAny(value, "\r\n\t")
}

func invalidCommandResultField(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalidCommandResultEvent, field)
}
