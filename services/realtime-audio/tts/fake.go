package tts

import (
	"context"
	"sync"
)

// FakeProviderConfig controls deterministic offline TTS behavior.
type FakeProviderConfig struct {
	Chunks    []AudioChunk
	Result    Result
	StartErr  error
	FinishErr error
}

// FakeProvider records requests and returns configured in-memory streams.
type FakeProvider struct {
	mu       sync.Mutex
	config   FakeProviderConfig
	requests []Request
}

// NewFakeProvider constructs an offline TTS provider.
func NewFakeProvider(config FakeProviderConfig) *FakeProvider {
	return &FakeProvider{config: config}
}

// StartStream returns a stream whose chunks are already buffered.
func (p *FakeProvider) StartStream(ctx context.Context, request Request) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config.StartErr != nil {
		return nil, p.config.StartErr
	}
	p.requests = append(p.requests, request)
	chunks := make(chan AudioChunk, len(p.config.Chunks))
	for _, configured := range p.config.Chunks {
		chunk := configured
		chunk.Data = append([]byte(nil), configured.Data...)
		chunks <- chunk
	}
	close(chunks)
	return &fakeStream{chunks: chunks, result: p.config.Result, finishErr: p.config.FinishErr}, nil
}

// Requests returns a copy of requests observed by the fake.
func (p *FakeProvider) Requests() []Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Request(nil), p.requests...)
}

type fakeStream struct {
	mu        sync.Mutex
	chunks    <-chan AudioChunk
	result    Result
	finishErr error
	closed    bool
}

func (s *fakeStream) Chunks() <-chan AudioChunk {
	return s.chunks
}

func (s *fakeStream) Finish(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if s.finishErr != nil {
		return Result{}, s.finishErr
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
