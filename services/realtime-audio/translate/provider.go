package translate

import "context"

// Request contains the final ASR text and the captured language direction.
type Request struct {
	SessionID      string
	TurnID         string
	Text           string
	SourceLanguage string
	TargetLanguage string
}

// Result contains translated text and provider usage metadata.
type Result struct {
	Text         string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	LatencyMS    int64
	CostAmount   string
	Currency     string
}

// Provider translates one final ASR result.
type Provider interface {
	Translate(ctx context.Context, request Request) (Result, error)
}
