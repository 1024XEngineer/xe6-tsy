// Package assistant defines the vendor-neutral LLM boundary for voice replies.
package assistant

import "context"

// Request contains one finalized user utterance. Conversation persistence is
// intentionally outside this first realtime boundary.
type Request struct {
	SessionID string
	TurnID    string
	Text      string
	Language  string
}

// Result contains one finalized reply and provider usage metadata.
type Result struct {
	Text         string
	Language     string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	LatencyMS    int64
	CostAmount   string
	Currency     string
}

// Provider generates one assistant reply from a finalized ASR utterance.
type Provider interface {
	Reply(ctx context.Context, request Request) (Result, error)
}
