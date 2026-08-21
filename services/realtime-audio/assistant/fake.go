package assistant

import (
	"context"
	"sync"
)

// FakeProviderConfig controls the deterministic offline assistant provider.
type FakeProviderConfig struct {
	Result Result
	Err    error
}

// FakeProvider records copied requests and returns a configured result.
type FakeProvider struct {
	mu       sync.Mutex
	config   FakeProviderConfig
	requests []Request
}

func NewFakeProvider(config FakeProviderConfig) *FakeProvider {
	return &FakeProvider{config: config}
}

func (p *FakeProvider) Reply(ctx context.Context, request Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	p.mu.Lock()
	p.requests = append(p.requests, request)
	result, err := p.config.Result, p.config.Err
	p.mu.Unlock()
	return result, err
}

func (p *FakeProvider) Requests() []Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Request(nil), p.requests...)
}

var _ Provider = (*FakeProvider)(nil)
