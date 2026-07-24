package usage

import "time"

type Stage string

const (
	StageASR         Stage = "asr"
	StageTranslation Stage = "translation"
	StageTTS         Stage = "tts"
	StageDiarization Stage = "diarization"
)

type Unit string

const (
	UnitAudioMillisecond Unit = "audio_millisecond"
	UnitCharacter        Unit = "character"
	UnitToken            Unit = "token"
)

type RecordInput struct {
	ID             string    `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	AccountID      string    `json:"account_id"`
	SessionID      string    `json:"session_id"`
	TurnID         string    `json:"turn_id"`
	Stage          Stage     `json:"stage"`
	Quantity       int64     `json:"quantity"`
	Unit           Unit      `json:"unit"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type Detail struct {
	RecordInput
	RecordedAt time.Time `json:"recorded_at"`
}

type StageTotal struct {
	ServiceType     Stage  `json:"service_type"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	AudioDurationMS int64  `json:"audio_duration_ms"`
	CostAmount      string `json:"cost_amount"`
	Currency        string `json:"currency"`
}

type Summary struct {
	AccountID   string       `json:"account_id"`
	SessionID   string       `json:"session_id,omitempty"`
	PeriodStart time.Time    `json:"period_start"`
	PeriodEnd   time.Time    `json:"period_end"`
	Totals      []StageTotal `json:"totals"`
}
