package localruntime

import (
	"context"
	"sync"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/runtime"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

// LifecycleRuntimeBridge breaks the Manager ↔ LifecycleService init cycle by
// forwarding runtime progress/failure reports after LifecycleService exists.
type LifecycleRuntimeBridge struct {
	mu       sync.RWMutex
	reporter runtime.RuntimeReporter
}

func (b *LifecycleRuntimeBridge) Set(reporter runtime.RuntimeReporter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reporter = reporter
}

func (b *LifecycleRuntimeBridge) SetProcessingState(
	ctx context.Context,
	update session.ProcessingStateUpdate,
) error {
	b.mu.RLock()
	reporter := b.reporter
	b.mu.RUnlock()
	if reporter == nil {
		return runtime.ErrDependencyRequired
	}
	return reporter.SetProcessingState(ctx, update)
}

func (b *LifecycleRuntimeBridge) SetRuntimeFailed(ctx context.Context, sessionID string) error {
	b.mu.RLock()
	reporter := b.reporter
	b.mu.RUnlock()
	if reporter == nil {
		return runtime.ErrDependencyRequired
	}
	return reporter.SetRuntimeFailed(ctx, sessionID)
}

var _ runtime.RuntimeReporter = (*LifecycleRuntimeBridge)(nil)
