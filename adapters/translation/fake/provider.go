package fake

import (
	"context"
	"sync"

	"github.com/1024XEngineer/xe6-tsy/apps/api/pkg/speechport"
)

type Provider struct {
	mu     sync.Mutex
	result speechport.TranslateResult
	err    error
	calls  int
}

func NewProvider(result speechport.TranslateResult, err error) *Provider {
	return &Provider{result: result, err: err}
}

func (p *Provider) Translate(context.Context, speechport.TranslateRequest) (speechport.TranslateResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.result, p.err
}

func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}
