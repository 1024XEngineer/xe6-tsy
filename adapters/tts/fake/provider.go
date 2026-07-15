package fake

import (
	"context"
	"sync"

	"github.com/1024XEngineer/xe6-tsy/apps/api/pkg/speechport"
)

type Provider struct {
	mu     sync.Mutex
	result speechport.SynthesizeResult
	err    error
	calls  int
}

func NewProvider(result speechport.SynthesizeResult, err error) *Provider {
	return &Provider{result: result, err: err}
}

func (p *Provider) Synthesize(context.Context, speechport.SynthesizeRequest) (speechport.SynthesizeResult, error) {
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
