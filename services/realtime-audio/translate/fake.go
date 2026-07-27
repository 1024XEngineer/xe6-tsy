package translate

import (
	"context"
	"sync"
)

// FakeProvider records requests and returns a deterministic result.
type FakeProvider struct {
	mu       sync.Mutex
	Result   Result
	Err      error
	requests []Request
}

// Translate implements Provider without any external call.
func (p *FakeProvider) Translate(ctx context.Context, request Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	return p.Result, p.Err
}

// Requests returns a copy of requests observed by the fake.
func (p *FakeProvider) Requests() []Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Request(nil), p.requests...)
}

var _ Provider = (*FakeProvider)(nil)
