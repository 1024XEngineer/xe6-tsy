package translate

import (
	"context"
	"errors"
)

// ErrUnexpectedBehavior is returned when the model abandons translation
// (for example after prompt injection) and a reinforced retry still fails.
// Callers should treat this as a terminal, user-visible rejection rather than
// a generic pipeline fault.
var ErrUnexpectedBehavior = errors.New("translation rejected due to unexpected model behavior")

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

// StreamProvider is an optional low-latency translation boundary. Providers
// that implement it emit model deltas as soon as they arrive while still
// returning the complete result for usage accounting and ordered playback.
type StreamProvider interface {
	Provider
	TranslateStream(ctx context.Context, request Request, onDelta func(string)) (Result, error)
}
