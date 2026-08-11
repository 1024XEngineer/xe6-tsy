package runtime

import (
	"context"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

// modeRouter owns an immutable mode-to-handler registry. The default preserves
// legacy interpretation until a later stage captures mode on each opened Turn.
type modeRouter struct {
	defaultMode realtimev1.Mode
	handlers    map[realtimev1.Mode]pipeline.ASRFinalHandler
}

func newModeRouter(
	defaultMode realtimev1.Mode,
	handlers map[realtimev1.Mode]pipeline.ASRFinalHandler,
) (*modeRouter, error) {
	if !defaultMode.Valid() {
		return nil, ErrModeNotAvailable
	}
	registered := make(map[realtimev1.Mode]pipeline.ASRFinalHandler, len(handlers))
	for mode, handler := range handlers {
		if !mode.Valid() {
			return nil, ErrModeNotAvailable
		}
		if handler == nil {
			return nil, ErrDependencyRequired
		}
		registered[mode] = handler
	}
	if _, ok := registered[defaultMode]; !ok {
		return nil, ErrModeNotAvailable
	}
	return &modeRouter{defaultMode: defaultMode, handlers: registered}, nil
}

// HandleASRFinal satisfies the shared pipeline boundary using the legacy
// default. Dispatch is the explicit boundary used once Turns carry mode state.
func (r *modeRouter) HandleASRFinal(
	ctx context.Context,
	turn pipeline.TurnContext,
	result asr.FinalResult,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return ErrDependencyRequired
	}
	return r.Dispatch(ctx, r.defaultMode, turn, result)
}

func (r *modeRouter) Dispatch(
	ctx context.Context,
	mode realtimev1.Mode,
	turn pipeline.TurnContext,
	result asr.FinalResult,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return ErrDependencyRequired
	}
	handler, ok := r.handlers[mode]
	if !mode.Valid() || !ok {
		return ErrModeNotAvailable
	}
	return handler.HandleASRFinal(ctx, turn, result)
}

func (r *modeRouter) availableModes() []realtimev1.Mode {
	if r == nil {
		return nil
	}
	modes := make([]realtimev1.Mode, 0, len(r.handlers))
	for mode := range r.handlers {
		modes = append(modes, mode)
	}
	return modes
}
