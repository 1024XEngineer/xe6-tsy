package tts

import (
	"context"
	"time"
)

// Request identifies the Turn and playback produced from translated text.
type Request struct {
	SessionID      string
	TurnID         string
	PlaybackID     string
	Text           string
	TargetLanguage string
	VoiceID        string
}

// AudioChunk is one ordered unit of synthesized audio.
type AudioChunk struct {
	SequenceNo int64
	Data       []byte
}

// Result contains synthesis provider and usage metadata.
type Result struct {
	Provider      string
	Model         string
	AudioDuration time.Duration
	CostAmount    string
	Currency      string
}

// Provider starts one synthesis stream.
type Provider interface {
	StartStream(ctx context.Context, request Request) (Stream, error)
}

// Stream exposes synthesized audio and its completed usage result.
type Stream interface {
	Chunks() <-chan AudioChunk
	Finish(ctx context.Context) (Result, error)
	Close() error
}
