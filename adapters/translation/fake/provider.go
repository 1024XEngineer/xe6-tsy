package fake

import (
	"context"
	"sync"

	"github.com/1024XEngineer/xe6-tsy/apps/api/pkg/speechport"
)

// Provider returns a configured translation result for isolated tests and
// records invocations without contacting a model service.
type Provider struct {
	mu     sync.Mutex
	result speechport.TranslateResult
	err    error
	calls  int
}

// NewProvider creates a deterministic translation adapter for one test setup.
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
