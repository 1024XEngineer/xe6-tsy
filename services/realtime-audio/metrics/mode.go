package metrics

import (
	"context"
	"errors"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/runtime"
)

// ModeChangedSink accepts an immutable transition event before state commit.
type ModeChangedSink interface {
	Publish(context.Context, realtimev1.ModeChangedEvent) error
}

// RecordModeCommand classifies one command that reached a runtime coordinator.
// Manager-level lookup, cancellation, and dependency failures are deliberately
// excluded so Total remains the denominator for coordinator decisions only.
func (r *Registry) RecordModeCommand(result realtimev1.SwitchModeResult, err error) {
	if r == nil {
		return
	}
	r.modeCommandsTotal.Add(1)
	switch {
	case err == nil && result.Status == realtimev1.ModeSwitchApplied:
		r.modeCommandsAppliedResponse.Add(1)
	case err == nil && result.Status == realtimev1.ModeSwitchUnchanged:
		r.modeCommandsUnchangedResponse.Add(1)
	case errors.Is(err, runtime.ErrModeGenerationConflict):
		r.modeCommandsGenerationConflict.Add(1)
	case errors.Is(err, runtime.ErrModeRuntimeInstanceMismatch):
		r.modeCommandsRuntimeMismatch.Add(1)
	case errors.Is(err, runtime.ErrModeOperationConflict):
		r.modeCommandsOperationConflict.Add(1)
	case errors.Is(err, runtime.ErrModeNotAvailable):
		r.modeCommandsModeUnavailable.Add(1)
	case errors.Is(err, runtime.ErrModeEventUnavailable):
		r.modeCommandsEventUnavailable.Add(1)
	default:
		r.modeCommandsOtherFailure.Add(1)
	}
}

type observedModeChangedSink struct {
	next     ModeChangedSink
	registry *Registry
}

// ObserveModeChangedSink counts only transitions that reach durable event
// publication. A retry after an uncertain append is another attempt, while an
// accepted append represents the transition that the coordinator may commit.
func ObserveModeChangedSink(next ModeChangedSink, registry *Registry) ModeChangedSink {
	if next == nil || registry == nil {
		return next
	}
	return &observedModeChangedSink{next: next, registry: registry}
}

func (o *observedModeChangedSink) Publish(
	ctx context.Context,
	event realtimev1.ModeChangedEvent,
) error {
	o.registry.modeChangePublicationsAttempted.Add(1)
	if err := o.next.Publish(ctx, event); err != nil {
		o.registry.modeChangePublicationsFailed.Add(1)
		return err
	}
	o.registry.modeChangePublicationsAccepted.Add(1)
	return nil
}

var _ runtime.ModeCommandObserver = (*Registry)(nil)
var _ ModeChangedSink = (*observedModeChangedSink)(nil)
