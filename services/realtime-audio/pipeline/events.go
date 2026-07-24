package pipeline

import "time"

// FinalTurnEvent is the durable record sent to member 4 after translation.
type FinalTurnEvent struct {
	EventID               string
	TraceID               string
	SessionID             string
	TurnID                string
	ParticipantID         *string
	SequenceNo            int64
	SourceLanguage        string
	TargetLanguage        string
	SourceText            string
	TranslatedText        string
	ProviderSpeakerID     string
	SpeakerConfidence     float64
	AttributionStatus     string
	LanguageConfigVersion int64
	OccurredAt            time.Time
}

// UsageFact is the v1 usage event sent to member 5.
type UsageFact struct {
	EventVersion    string
	ID              string
	TraceID         string
	IdempotencyKey  string
	AccountID       string
	SessionID       string
	TurnID          string
	ServiceType     string
	Provider        string
	Model           string
	InputTokens     int64
	OutputTokens    int64
	AudioDurationMS int64
	CostAmount      string
	Currency        string
	OccurredAt      time.Time
}
