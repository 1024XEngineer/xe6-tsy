package fake

import (
	"context"
	"sync"

	"github.com/1024XEngineer/xe6-tsy/apps/api/pkg/speechport"
)

type Provider struct {
	mu     sync.Mutex
	events []speechport.ASREvent
	err    error
	calls  int
}

func NewProvider(events []speechport.ASREvent, err error) *Provider {
	return &Provider{events: append([]speechport.ASREvent(nil), events...), err: err}
}

func (p *Provider) StartStream(context.Context, speechport.StartASRStreamRequest) (speechport.ASRStream, error) {
	p.mu.Lock()
	p.calls++
	events := append([]speechport.ASREvent(nil), p.events...)
	err := p.err
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}

	eventChannel := make(chan speechport.ASREvent, len(events))
	for _, event := range events {
		eventChannel <- event
	}
	close(eventChannel)
	return &Stream{events: eventChannel}, nil
}

func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type Stream struct {
	mu     sync.Mutex
	events <-chan speechport.ASREvent
	closed bool
}

func (s *Stream) Events() <-chan speechport.ASREvent {
	return s.events
}

func (s *Stream) Close(context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *Stream) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
