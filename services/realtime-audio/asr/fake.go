package asr

import (
	"context"
	"sync"
)

// FakeProviderConfig controls deterministic offline ASR behavior.
type FakeProviderConfig struct {
	Partial   Event
	Final     FinalResult
	StartErr  error
	FinishErr error
}

// FakeProvider records requests and returns configured in-memory streams.
type FakeProvider struct {
	mu       sync.Mutex
	config   FakeProviderConfig
	requests []StreamRequest
}

// NewFakeProvider constructs an offline ASR provider.
func NewFakeProvider(config FakeProviderConfig) *FakeProvider {
	return &FakeProvider{config: config}
}

// StartStream records the request and preloads its buffered event channel.
func (p *FakeProvider) StartStream(ctx context.Context, request StreamRequest) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config.StartErr != nil {
		return nil, p.config.StartErr
	}
	p.requests = append(p.requests, request)
	events := make(chan Event, 2)
	if p.config.Partial.Type != "" || p.config.Partial.Text != "" {
		partial := p.config.Partial
		partial.Type = EventPartial
		events <- partial
	}
	final := p.config.Final
	events <- Event{Type: EventFinal, Text: final.Text, Final: &final}
	close(events)
	return &fakeStream{events: events, result: final, finishErr: p.config.FinishErr}, nil
}

// Requests returns a copy of requests observed by the fake.
func (p *FakeProvider) Requests() []StreamRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]StreamRequest(nil), p.requests...)
}

type fakeStream struct {
	mu        sync.Mutex
	events    <-chan Event
	result    FinalResult
	finishErr error
	audio     [][]byte
	closed    bool
}

func (s *fakeStream) PushAudio(ctx context.Context, audio []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audio = append(s.audio, append([]byte(nil), audio...))
	return nil
}

func (s *fakeStream) Events() <-chan Event {
	return s.events
}

func (s *fakeStream) Finish(ctx context.Context) (FinalResult, error) {
	if err := ctx.Err(); err != nil {
		return FinalResult{}, err
	}
	if s.finishErr != nil {
		return FinalResult{}, s.finishErr
	}
	return s.result, nil
}

func (s *fakeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

var _ Stream = (*fakeStream)(nil)
