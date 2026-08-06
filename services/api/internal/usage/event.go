package usage

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// ParseRecordInput decodes a usage.recorded v1 JSON payload into RecordInput.
func ParseRecordInput(payload []byte) (RecordInput, error) {
	var raw struct {
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
	if err := json.Unmarshal(payload, &raw); err != nil {
		return RecordInput{}, fmt.Errorf("%w: decode usage.recorded payload", domain.ErrInvalidArgument)
	}
	input := RecordInput{
		EventVersion:    raw.EventVersion,
		ID:              raw.ID,
		TraceID:         raw.TraceID,
		IdempotencyKey:  raw.IdempotencyKey,
		AccountID:       raw.AccountID,
		SessionID:       raw.SessionID,
		TurnID:          raw.TurnID,
		ServiceType:     Stage(raw.ServiceType),
		Provider:        raw.Provider,
		Model:           raw.Model,
		InputTokens:     raw.InputTokens,
		OutputTokens:    raw.OutputTokens,
		AudioDurationMS: raw.AudioDurationMS,
		CostAmount:      raw.CostAmount,
		Currency:        raw.Currency,
		OccurredAt:      raw.OccurredAt,
	}
	if err := validate(input); err != nil {
		return RecordInput{}, err
	}
	return input, nil
}

// MarshalRecordInput encodes a RecordInput as usage.recorded v1 JSON.
func MarshalRecordInput(input RecordInput) ([]byte, error) {
	if err := validate(input); err != nil {
		return nil, err
	}
	return json.Marshal(input)
}
