package realtimev1

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// ASRPartialTopic is the ephemeral browser-facing transcription snapshot topic.
	ASRPartialTopic = "asr.partial"
	// ASRPartialEventVersion is the only partial ASR event schema accepted by v1 consumers.
	ASRPartialEventVersion = 1

	maxASRPartialIDLength   = 128
	maxASRPartialTextLength = 4096
)

// ErrInvalidASRPartialEvent identifies malformed ephemeral ASR subtitle updates.
var ErrInvalidASRPartialEvent = errors.New("invalid ASR partial event")

// ASRPartialEvent is a replaceable recognition snapshot for one ordinary Turn.
// It is best-effort only and must never be persisted, translated, synthesized, or billed.
// SourceLanguage is omitted while the provider is still auto-detecting the language.
type ASRPartialEvent struct {
	Type           string    `json:"type"`
	EventVersion   int       `json:"event_version"`
	SessionID      string    `json:"session_id"`
	TurnID         string    `json:"turn_id"`
	Text           string    `json:"text"`
	Stash          string    `json:"stash,omitempty"`
	SourceLanguage string    `json:"source_language,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// Validate rejects malformed partial events before they cross the DataChannel boundary.
func (event ASRPartialEvent) Validate() error {
	switch {
	case event.Type != ASRPartialTopic:
		return invalidASRPartialField("type")
	case event.EventVersion != ASRPartialEventVersion:
		return invalidASRPartialField("event_version")
	case !validASRPartialText(event.SessionID, maxASRPartialIDLength):
		return invalidASRPartialField("session_id")
	case !validASRPartialText(event.TurnID, maxASRPartialIDLength):
		return invalidASRPartialField("turn_id")
	case event.Text == "" && event.Stash == "":
		return invalidASRPartialField("text")
	case event.Text != "" && !validASRPartialText(event.Text, maxASRPartialTextLength):
		return invalidASRPartialField("text")
	case event.Stash != "" && !validASRPartialText(event.Stash, maxASRPartialTextLength):
		return invalidASRPartialField("stash")
	case event.SourceLanguage != "" && !validASRPartialText(event.SourceLanguage, maxASRPartialIDLength):
		return invalidASRPartialField("source_language")
	case event.OccurredAt.IsZero():
		return invalidASRPartialField("occurred_at")
	default:
		return nil
	}
}

func validASRPartialText(value string, maxLength int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) == value && value != "" &&
		utf8.RuneCountInString(value) <= maxLength && !strings.ContainsAny(value, "\r\n\t")
}

func invalidASRPartialField(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalidASRPartialEvent, field)
}
