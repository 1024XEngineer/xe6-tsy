package asr

import (
	"context"
	"time"
)

// StreamRequest identifies the member-3-owned Turn being transcribed.
type StreamRequest struct {
	SessionID      string
	TurnID         string
	SourceLanguage string
}

// EventType distinguishes provisional text from a completed recognition result.
type EventType string

const (
	EventPartial EventType = "partial"
	EventFinal   EventType = "final"
)

// Event is an ephemeral recognition update emitted by an ASR stream.
type Event struct {
	Type EventType
	// Text is the provider-confirmed prefix. It is the only partial text that
	// may enter phrase translation before the VAD final.
	Text string
	// Stash is the replaceable provider tail. It is display-only until Qwen
	// promotes it into Text or the final transcript arrives.
	Stash string
	// Language is the provider-detected language for this snapshot. It is
	// intentionally only authoritative once Text contains confirmed content.
	Language string
	Final    *FinalResult
}

// FinalResult carries the provider result and usage data for one Turn.
type FinalResult struct {
	Text              string
	SourceLanguage    string
	Confidence        float64
	ProviderSpeakerID string
	AudioStart        time.Duration
	AudioEnd          time.Duration
	Provider          string
	Model             string
	AudioDuration     time.Duration
	CostAmount        string
	Currency          string
}

// Provider starts one recognition stream for a Turn.
type Provider interface {
	StartStream(ctx context.Context, request StreamRequest) (Stream, error)
}

// Stream accepts audio and exposes provisional and final recognition data.
// Finish finalizes the stream and closes Events before it returns.
type Stream interface {
	PushAudio(ctx context.Context, audio []byte) error
	Events() <-chan Event
	Finish(ctx context.Context) (FinalResult, error)
	Close() error
}
