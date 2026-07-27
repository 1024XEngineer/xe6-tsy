package pipeline

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// FinalTurnEvent is the shared durable record sent to member 4.
type FinalTurnEvent = recordsv1.FinalTurnEvent

// UsageEventVersion identifies the usage.recorded payload accepted by member 5.
const UsageEventVersion = 1

var (
	// ErrInvalidUsageFact indicates that a usage.recorded payload violates its v1 contract.
	ErrInvalidUsageFact  = errors.New("invalid usage fact")
	usageCostPattern     = regexp.MustCompile(`^(?:|(?:0|[1-9][0-9]*)(?:\.[0-9]+)?)$`)
	usageCurrencyPattern = regexp.MustCompile(`^(?:|[A-Z]{3})$`)
)

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

// Validate enforces the usage.recorded v1 schema before durable publication.
func (fact UsageFact) Validate() error {
	switch {
	case fact.EventVersion != UsageEventVersion:
		return invalidUsageField("event_version")
	case fact.ID == "":
		return invalidUsageField("id")
	case fact.TraceID == "":
		return invalidUsageField("trace_id")
	case fact.IdempotencyKey == "" || len(fact.IdempotencyKey) > 200:
		return invalidUsageField("idempotency_key")
	case fact.AccountID == "":
		return invalidUsageField("account_id")
	case fact.SessionID == "":
		return invalidUsageField("session_id")
	case fact.TurnID == "":
		return invalidUsageField("turn_id")
	case fact.Provider == "":
		return invalidUsageField("provider")
	case fact.Model == "":
		return invalidUsageField("model")
	case fact.InputTokens < 0:
		return invalidUsageField("input_tokens")
	case fact.OutputTokens < 0:
		return invalidUsageField("output_tokens")
	case fact.AudioDurationMS < 0:
		return invalidUsageField("audio_duration_ms")
	case !usageCostPattern.MatchString(fact.CostAmount):
		return invalidUsageField("cost_amount")
	case !usageCurrencyPattern.MatchString(fact.Currency):
		return invalidUsageField("currency")
	case fact.OccurredAt.IsZero():
		return invalidUsageField("occurred_at")
	}

	switch fact.ServiceType {
	case "asr", "translation", "tts", "diarization":
		return nil
	default:
		return invalidUsageField("service_type")
	}
}

func invalidUsageField(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalidUsageFact, field)
}
