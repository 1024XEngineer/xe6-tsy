package pipeline

import (
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// FinalTurnEvent is the shared durable record sent to member 4.
type FinalTurnEvent = recordsv1.FinalTurnEvent

// UsageEventVersion identifies the usage.recorded payload accepted by member 5.
const UsageEventVersion = 1

// UsageFact is the v1 usage event sent to member 5.
type UsageFact struct {
	EventVersion    int       `json:"event_version"`
	ID              string    `json:"id"`
	TraceID         string    `json:"trace_id"`
	IdempotencyKey  string    `json:"idempotency_key"`
	AccountID       string    `json:"account_id"`
	SessionID       string    `json:"session_id"`
	TurnID          string    `json:"turn_id"`
	ServiceType     string    `json:"service_type"`
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	InputTokens     int64     `json:"input_tokens"`
	OutputTokens    int64     `json:"output_tokens"`
	AudioDurationMS int64     `json:"audio_duration_ms"`
	CostAmount      string    `json:"cost_amount"`
	Currency        string    `json:"currency"`
	OccurredAt      time.Time `json:"occurred_at"`
}
