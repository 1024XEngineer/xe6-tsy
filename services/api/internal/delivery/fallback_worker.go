package delivery

import (
	"context"
	"errors"
	"time"
)

// AutomaticTurnFallbackWorker retries automatic targets and coordinates fallback playback.
type AutomaticTurnFallbackWorker struct {
	service  *UseCases
	interval time.Duration
}

// NewAutomaticTurnFallbackWorker creates a periodic automatic-delivery recovery worker.
func NewAutomaticTurnFallbackWorker(service *UseCases, interval time.Duration) *AutomaticTurnFallbackWorker {
	if interval <= 0 {
		interval = time.Second
	}
	return &AutomaticTurnFallbackWorker{service: service, interval: interval}
}

func (w *AutomaticTurnFallbackWorker) Run(ctx context.Context) error {
	if w == nil || w.service == nil {
		return ErrWorkerNotConfigured
	}
	if err := w.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.runOnce(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func (w *AutomaticTurnFallbackWorker) runOnce(ctx context.Context) error {
	if err := w.service.RetryAutomaticTurns(ctx, 20); err != nil {
		return err
	}
	if err := w.service.RecoverAutomaticTurns(ctx, 20); err != nil {
		return err
	}
	return w.service.RestoreAutomaticTurns(ctx, 20)
}
