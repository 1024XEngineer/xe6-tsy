package realtimev1

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// WakeWordDetectedType is the only hardware-originated signal accepted by the
	// first bounded voice-command channel.
	WakeWordDetectedType = "wake_word.detected"
	// WakeWordDetectedEventVersion identifies the JSON shape of WakeWordDetectedSignal.
	WakeWordDetectedEventVersion = 1

	maxWakeWordSignalIDLength = 128
)

var ErrInvalidWakeWordDetectedSignal = errors.New("invalid wake-word detected signal")

// WakeWordDetectedSignal reports a local hardware wake-word decision. It opens
// a server-side command window but does not itself contain audio or a mode command.
type WakeWordDetectedSignal struct {
	Type         string    `json:"type"`
	EventVersion int       `json:"event_version"`
	SignalID     string    `json:"signal_id"`
	DetectedAt   time.Time `json:"detected_at"`
}

// Validate rejects unsupported signal kinds and ambiguous identities before the
// signal can affect realtime input routing.
func (signal WakeWordDetectedSignal) Validate() error {
	switch {
	case signal.Type != WakeWordDetectedType:
		return invalidWakeWordDetectedField("type")
	case signal.EventVersion != WakeWordDetectedEventVersion:
		return invalidWakeWordDetectedField("event_version")
	case signal.SignalID == "" || strings.TrimSpace(signal.SignalID) != signal.SignalID || len(signal.SignalID) > maxWakeWordSignalIDLength:
		return invalidWakeWordDetectedField("signal_id")
	case signal.DetectedAt.IsZero():
		return invalidWakeWordDetectedField("detected_at")
	default:
		return nil
	}
}

func invalidWakeWordDetectedField(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalidWakeWordDetectedSignal, field)
}
