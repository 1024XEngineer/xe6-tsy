package usage

import "time"

// UsageEventVersion is the only usage.recorded schema version accepted by this module.
const UsageEventVersion = 1

// Stage identifies the provider stage responsible for a usage fact.
type Stage string

const (
	// StageASR records speech-recognition usage.
	StageASR Stage = "asr"
	// StageTranslation records text-translation usage.
	StageTranslation Stage = "translation"
	// StageTTS records speech-synthesis usage.
	StageTTS Stage = "tts"
	// StageDiarization records speaker-diarization usage.
	StageDiarization Stage = "diarization"
)

// RecordInput is the versioned usage.recorded fact accepted from realtime processing.
type RecordInput struct {
	EventVersion    int       `json:"event_version"`
	ID              string    `json:"id"`
	TraceID         string    `json:"trace_id"`
	IdempotencyKey  string    `json:"idempotency_key"`
	AccountID       string    `json:"account_id"`
	SessionID       string    `json:"session_id"`
	TurnID          string    `json:"turn_id"`
	ServiceType     Stage     `json:"service_type"`
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	InputTokens     int64     `json:"input_tokens"`
	OutputTokens    int64     `json:"output_tokens"`
	AudioDurationMS int64     `json:"audio_duration_ms"`
	CostAmount      string    `json:"cost_amount"`
	Currency        string    `json:"currency"`
	OccurredAt      time.Time `json:"occurred_at"`
}

// Detail is the immutable persisted usage fact with its server recording time.
type Detail struct {
	RecordInput
	RecordedAt time.Time `json:"recorded_at"`
}

// StageTotal aggregates billable dimensions for one service stage.
type StageTotal struct {
	ServiceType     Stage  `json:"service_type"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	AudioDurationMS int64  `json:"audio_duration_ms"`
	CostAmount      string `json:"cost_amount"`
	Currency        string `json:"currency"`
}

// Summary reports usage for either one session or an account time period.
type Summary struct {
	AccountID   string       `json:"account_id"`
	SessionID   string       `json:"session_id,omitempty"`
	PeriodStart time.Time    `json:"period_start"`
	PeriodEnd   time.Time    `json:"period_end"`
	Totals      []StageTotal `json:"totals"`
}
