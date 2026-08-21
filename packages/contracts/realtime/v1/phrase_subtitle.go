package realtimev1

import (
	"errors"
	"fmt"
	"time"
)

const (
	// PhraseSubtitleTopic is the ephemeral browser-facing stable phrase topic.
	PhraseSubtitleTopic = "phrase.subtitle"
	// PhraseSubtitleEventVersion is the only phrase subtitle schema accepted by v1 consumers.
	PhraseSubtitleEventVersion = 1
)

// ErrInvalidPhraseSubtitleEvent identifies malformed ephemeral subtitle updates.
var ErrInvalidPhraseSubtitleEvent = errors.New("invalid phrase subtitle event")

// PhraseSubtitleStatus describes the lifecycle of one immutable source phrase.
type PhraseSubtitleStatus string

const (
	PhraseSubtitleSourceStable      PhraseSubtitleStatus = "source_stable"
	PhraseSubtitleTranslated        PhraseSubtitleStatus = "translated"
	PhraseSubtitleTranslationFailed PhraseSubtitleStatus = "translation_failed"
)

// Valid reports whether the status belongs to the phrase subtitle v1 lifecycle.
func (status PhraseSubtitleStatus) Valid() bool {
	switch status {
	case PhraseSubtitleSourceStable, PhraseSubtitleTranslated, PhraseSubtitleTranslationFailed:
		return true
	default:
		return false
	}
}

// PhraseSubtitleEvent is one immutable, best-effort subtitle update within an ASR utterance.
// It is never a FinalTurn and must not directly trigger persistence, billing, translation, or TTS.
type PhraseSubtitleEvent struct {
	Type           string               `json:"type"`
	EventVersion   int                  `json:"event_version"`
	SessionID      string               `json:"session_id"`
	UtteranceID    string               `json:"utterance_id"`
	PhraseSequence int64                `json:"phrase_sequence"`
	SourceText     string               `json:"source_text"`
	TranslatedText string               `json:"translated_text,omitempty"`
	Status         PhraseSubtitleStatus `json:"status"`
	OccurredAt     time.Time            `json:"occurred_at"`
}

// Validate rejects malformed subtitle events before they cross the DataChannel boundary.
func (event PhraseSubtitleEvent) Validate() error {
	switch {
	case event.Type != PhraseSubtitleTopic:
		return invalidPhraseSubtitleField("type")
	case event.EventVersion != PhraseSubtitleEventVersion:
		return invalidPhraseSubtitleField("event_version")
	case !validASRPartialText(event.SessionID, maxASRPartialIDLength):
		return invalidPhraseSubtitleField("session_id")
	case !validASRPartialText(event.UtteranceID, maxASRPartialIDLength):
		return invalidPhraseSubtitleField("utterance_id")
	case event.PhraseSequence < 1:
		return invalidPhraseSubtitleField("phrase_sequence")
	case !validASRPartialText(event.SourceText, maxASRPartialTextLength):
		return invalidPhraseSubtitleField("source_text")
	case !event.Status.Valid():
		return invalidPhraseSubtitleField("status")
	case event.Status == PhraseSubtitleTranslated && !validASRPartialText(event.TranslatedText, maxASRPartialTextLength):
		return invalidPhraseSubtitleField("translated_text")
	case event.Status != PhraseSubtitleTranslated && event.TranslatedText != "":
		return invalidPhraseSubtitleField("translated_text")
	case event.OccurredAt.IsZero():
		return invalidPhraseSubtitleField("occurred_at")
	default:
		return nil
	}
}

func invalidPhraseSubtitleField(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalidPhraseSubtitleEvent, field)
}
