// Package recordsv1 defines version 1 contracts for speaker attribution and final translation records.
package recordsv1

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type AttributionStatus string

const (
	AttributionPending     AttributionStatus = "pending"
	AttributionProvisional AttributionStatus = "provisional"
	AttributionConfirmed   AttributionStatus = "confirmed"
	AttributionCorrected   AttributionStatus = "corrected"
)

const CorrectedBySystem = "system"

var ErrInvalidFinalTurnEvent = errors.New("invalid final turn event")

type ErrorCode string

const (
	ErrorInvalidRequest     ErrorCode = "invalid_request"
	ErrorUnauthenticated    ErrorCode = "unauthenticated"
	ErrorForbidden          ErrorCode = "forbidden"
	ErrorVoiceSessionAbsent ErrorCode = "voice_session_not_found"
	ErrorParticipantAbsent  ErrorCode = "participant_not_found"
	ErrorVoiceTurnAbsent    ErrorCode = "voice_turn_not_found"
	ErrorInvalidAttribution ErrorCode = "invalid_attribution"
	ErrorNotImplemented     ErrorCode = "not_implemented"
	ErrorInternal           ErrorCode = "internal_error"
)

// APIError is the shared error body returned by public HTTP endpoints.
type APIError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	RequestID string    `json:"request_id"`
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}

// CursorPage is embedded in list responses that use cursor pagination.
type CursorPage struct {
	NextCursor *string `json:"next_cursor"`
}

// Participant is the persistent, session-scoped representation of a temporary speaker.
type Participant struct {
	ID                string    `json:"participant_id"`
	SessionID         string    `json:"session_id"`
	SpeakerCode       string    `json:"speaker_code"`
	DisplayName       *string   `json:"display_name"`
	ProviderSpeakerID *string   `json:"provider_speaker_id"`
	VoiceProfileID    *string   `json:"voice_profile_id"`
	Confidence        *float64  `json:"confidence"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// VoiceTurn is an immutable final translation record except for attribution fields.
type VoiceTurn struct {
	ID                    string            `json:"id"`
	SessionID             string            `json:"session_id"`
	ParticipantID         *string           `json:"participant_id"`
	SpeakerCode           string            `json:"speaker_code"`
	DisplayName           *string           `json:"display_name"`
	ProviderSpeakerID     *string           `json:"provider_speaker_id"`
	VoiceProfileID        *string           `json:"voice_profile_id"`
	SequenceNo            int64             `json:"sequence_no"`
	SourceLanguage        string            `json:"source_language"`
	TargetLanguage        string            `json:"target_language"`
	LanguageConfigVersion int64             `json:"language_config_version"`
	SourceText            string            `json:"source_text"`
	TranslatedText        string            `json:"translated_text"`
	SpeakerConfidence     *float64          `json:"speaker_confidence"`
	AttributionStatus     AttributionStatus `json:"attribution_status"`
	CorrectedBy           *string           `json:"corrected_by"`
	StartedAt             time.Time         `json:"started_at"`
	EndedAt               time.Time         `json:"ended_at"`
	CorrectedAt           *time.Time        `json:"corrected_at"`
	CreatedAt             time.Time         `json:"created_at"`
}

type ParticipantListResponse struct {
	Items []Participant `json:"items"`
	CursorPage
}

type VoiceTurnListResponse struct {
	Items []VoiceTurn `json:"items"`
	CursorPage
}

type UpdateParticipantRequest struct {
	DisplayName       *string `json:"display_name"`
	ProviderSpeakerID *string `json:"provider_speaker_id"`
	VoiceProfileID    *string `json:"voice_profile_id"`
}

type UpdateAttributionRequest struct {
	ParticipantID     string            `json:"participant_id"`
	AttributionStatus AttributionStatus `json:"attribution_status"`
	SpeakerConfidence *float64          `json:"speaker_confidence"`
}

type ListParticipantsQuery struct {
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

type ListTurnsQuery struct {
	Cursor            string            `json:"cursor"`
	Limit             int               `json:"limit"`
	SessionID         string            `json:"session_id"`
	ParticipantID     string            `json:"participant_id"`
	SpeakerCode       string            `json:"speaker_code"`
	AttributionStatus AttributionStatus `json:"attribution_status"`
	SourceLanguage    string            `json:"source_language"`
	TargetLanguage    string            `json:"target_language"`
	CreatedFrom       *time.Time        `json:"created_from"`
	CreatedTo         *time.Time        `json:"created_to"`
}

// FinalTurnEvent is the durable, at-least-once event emitted after translation is final.
type FinalTurnEvent struct {
	EventID               string            `json:"event_id"`
	TraceID               string            `json:"trace_id"`
	TurnID                string            `json:"turn_id"`
	SessionID             string            `json:"session_id"`
	ParticipantID         *string           `json:"participant_id"`
	SequenceNo            int64             `json:"sequence_no"`
	SourceLanguage        string            `json:"source_language"`
	TargetLanguage        string            `json:"target_language"`
	LanguageConfigVersion int64             `json:"language_config_version"`
	SourceText            string            `json:"source_text"`
	TranslatedText        string            `json:"translated_text"`
	SpeakerCode           string            `json:"speaker_code"`
	SpeakerLabelSnapshot  *string           `json:"speaker_label_snapshot"`
	SpeakerConfidence     *float64          `json:"speaker_confidence"`
	AttributionStatus     AttributionStatus `json:"attribution_status"`
	StartedAt             time.Time         `json:"started_at"`
	EndedAt               time.Time         `json:"ended_at"`
	OccurredAt            time.Time         `json:"occurred_at"`
}

// Validate enforces the required v1 fields before a FinalTurn enters durable delivery.
func (event FinalTurnEvent) Validate() error {
	switch {
	case event.EventID == "":
		return invalidFinalTurnField("event_id")
	case event.TraceID == "":
		return invalidFinalTurnField("trace_id")
	case event.TurnID == "":
		return invalidFinalTurnField("turn_id")
	case event.SessionID == "":
		return invalidFinalTurnField("session_id")
	case event.SequenceNo <= 0:
		return invalidFinalTurnField("sequence_no")
	case event.SourceLanguage == "":
		return invalidFinalTurnField("source_language")
	case event.TargetLanguage == "":
		return invalidFinalTurnField("target_language")
	case event.SourceText == "":
		return invalidFinalTurnField("source_text")
	case event.TranslatedText == "":
		return invalidFinalTurnField("translated_text")
	case event.LanguageConfigVersion <= 0:
		return invalidFinalTurnField("language_config_version")
	case event.StartedAt.IsZero():
		return invalidFinalTurnField("started_at")
	case event.EndedAt.IsZero() || event.EndedAt.Before(event.StartedAt):
		return invalidFinalTurnField("ended_at")
	case event.OccurredAt.IsZero():
		return invalidFinalTurnField("occurred_at")
	}

	switch event.AttributionStatus {
	case AttributionPending, AttributionProvisional, AttributionConfirmed, AttributionCorrected:
		return nil
	default:
		return invalidFinalTurnField("attribution_status")
	}
}

func invalidFinalTurnField(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalidFinalTurnEvent, field)
}

type SpeakerObservation struct {
	SessionID         string    `json:"session_id"`
	TurnID            string    `json:"turn_id"`
	ProviderSpeakerID string    `json:"provider_speaker_id"`
	StartedAt         time.Time `json:"started_at"`
	EndedAt           time.Time `json:"ended_at"`
	AudioStartMS      int64     `json:"audio_start_ms"`
	AudioEndMS        int64     `json:"audio_end_ms"`
}

type SpeakerAttribution struct {
	ParticipantID     *string           `json:"participant_id"`
	SpeakerCode       string            `json:"speaker_code"`
	DisplayName       *string           `json:"display_name"`
	Confidence        *float64          `json:"confidence"`
	AttributionStatus AttributionStatus `json:"attribution_status"`
}

// FinalTurnSnapshot is the immutable data needed to create an outbound message snapshot.
type FinalTurnSnapshot struct {
	TurnID                string    `json:"turn_id"`
	SessionID             string    `json:"session_id"`
	ParticipantID         *string   `json:"participant_id"`
	SpeakerLabelSnapshot  *string   `json:"speaker_label_snapshot"`
	SourceLanguage        string    `json:"source_language"`
	TargetLanguage        string    `json:"target_language"`
	LanguageConfigVersion *int64    `json:"language_config_version"`
	SourceText            string    `json:"source_text"`
	TranslatedText        string    `json:"translated_text"`
	CreatedAt             time.Time `json:"created_at"`
}

// FinalTurnSink is the producer-side port used by realtime translation to enqueue final events.
type FinalTurnSink interface {
	Publish(ctx context.Context, event FinalTurnEvent) error
}

// FinalTurnConsumer is the record module's idempotent final-event consumption port.
type FinalTurnConsumer interface {
	ConsumeFinalTurn(ctx context.Context, event FinalTurnEvent) error
}

// SpeakerAttributionReader resolves the best available temporary speaker attribution without blocking realtime work.
type SpeakerAttributionReader interface {
	GetProvisionalAttribution(ctx context.Context, observation SpeakerObservation) (SpeakerAttribution, error)
}

// TurnReader returns final turns only after enforcing account ownership.
type TurnReader interface {
	ReadFinalTurns(ctx context.Context, accountID string, turnIDs []string) ([]FinalTurnSnapshot, error)
}

// SessionOwnerReader supplies the authoritative account that owns a voice session.
type SessionOwnerReader interface {
	AccountIDForSession(ctx context.Context, sessionID string) (string, error)
}
