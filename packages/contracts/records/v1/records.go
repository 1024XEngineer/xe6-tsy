// Package recordsv1 defines version 1 contracts for speaker attribution and final translation records.
package recordsv1

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

const (
	// PendingSpeakerCode identifies a FinalTurn whose speaker cannot yet be attributed.
	PendingSpeakerCode = "speaker_pending"
	// FinalTurnTopic is the durable AsyncAPI topic for completed translations.
	FinalTurnTopic = "translation.final"
	// FinalTurnEventVersion is the only FinalTurnEvent schema version accepted by v1 consumers.
	FinalTurnEventVersion = 1
	// MaxFinalTurnBatchSize bounds one immutable outbound-message snapshot request.
	MaxFinalTurnBatchSize = 100
)

var ErrInvalidFinalTurnEvent = errors.New("invalid final turn event")

// FinalTurnPayloadHash is the fixed-size digest stored for FinalTurn idempotency checks.
type FinalTurnPayloadHash [sha256.Size]byte

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

// FinalTurnEvent is emitted after translation final succeeds, without waiting for TTS acceptance
// or playback. Partial ASR or translation data must never be published as a FinalTurnEvent.
//
// EventID, TurnID, and (SessionID, SequenceNo) are independent idempotency keys. A retry keeps
// all three values and its payload unchanged; an existing key with a different payload is a
// conflict rather than an overwrite.
type FinalTurnEvent struct {
	EventVersion          int               `json:"event_version"`
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
	TTSEnabled            bool              `json:"tts_enabled"`
	DeliveryEnabled       bool              `json:"delivery_enabled"`
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
	case event.EventVersion != FinalTurnEventVersion:
		return invalidFinalTurnField("event_version")
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
	case event.SpeakerCode == "":
		return invalidFinalTurnField("speaker_code")
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

// FinalTurnEventPayloadHash returns SHA-256 over JSON encoding every FinalTurnEvent field.
// Record adapters store this value and compare it whenever an idempotency key already exists.
func FinalTurnEventPayloadHash(event FinalTurnEvent) (FinalTurnPayloadHash, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return FinalTurnPayloadHash{}, fmt.Errorf("marshal final turn payload: %w", err)
	}
	return sha256.Sum256(payload), nil
}

// SpeakerObservation carries the session-scoped stable key generated by a provider or diarization
// cluster. It never carries a raw voiceprint. Without the key, attribution remains pending and no
// participant is created.
type SpeakerObservation struct {
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`
	// ProviderSpeakerID is the stable provider or cluster key for this session, not a global identity.
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

// FinalTurnSnapshot is the immutable data needed to create an outbound message snapshot. New
// records always retain the language configuration version that realtime fixed for the turn.
type FinalTurnSnapshot struct {
	TurnID                string    `json:"turn_id"`
	SessionID             string    `json:"session_id"`
	ParticipantID         *string   `json:"participant_id"`
	SpeakerLabelSnapshot  *string   `json:"speaker_label_snapshot"`
	SourceLanguage        string    `json:"source_language"`
	TargetLanguage        string    `json:"target_language"`
	LanguageConfigVersion int64     `json:"language_config_version"`
	SourceText            string    `json:"source_text"`
	TranslatedText        string    `json:"translated_text"`
	CreatedAt             time.Time `json:"created_at"`
}

// FinalTurnSink accepts FinalTurnEvent messages durably. Publish returns nil only after a durable
// outbox or equivalent reliable transport accepts the event for later delivery.
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
