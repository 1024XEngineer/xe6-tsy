package runtime

import (
	"context"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

// modeRouter 持有不可变的“业务模式 -> final Handler”注册表。
// 注册表在构造时复制，运行期间不允许临时追加或覆盖，避免不同请求看到
// 不一致的 Handler 能力。
type modeRouter struct {
	handlers map[realtimev1.Mode]pipeline.ASRFinalHandler
}

func newModeRouter(
	handlers map[realtimev1.Mode]pipeline.ASRFinalHandler,
) (*modeRouter, error) {
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
	if len(registered) == 0 {
		return nil, ErrModeNotAvailable
	}
	return &modeRouter{handlers: registered}, nil
}

// HandleASRFinal routes by the mode captured when the Turn opened. It must not
// read the coordinator again because a concurrent switch belongs to later Turns.
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
	return r.Dispatch(ctx, turn.Mode.Mode, turn, result)
}

// HandleASRFinalAsync preserves streaming final settlement through the mode
// boundary. Modes without an async handler keep their existing synchronous
// behavior.
func (r *modeRouter) HandleASRFinalAsync(
	ctx context.Context,
	turn pipeline.TurnContext,
	result asr.FinalResult,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	handler, err := r.handlerFor(turn.Mode.Mode)
	if err != nil {
		return err
	}
	if asyncHandler, ok := handler.(pipeline.AsyncASRFinalHandler); ok {
		return asyncHandler.HandleASRFinalAsync(ctx, turn, result)
	}
	return handler.HandleASRFinal(ctx, turn, result)
}

func (r *modeRouter) Dispatch(
	ctx context.Context,
	mode realtimev1.Mode,
	turn pipeline.TurnContext,
	result asr.FinalResult,
) error {
	// 在调用 Handler 前再次检查 context，保证取消的 Turn 不会继续产生翻译、
	// FinalTurn 或播放副作用。Handler 自身仍需继续处理下游依赖取消。
	if err := ctx.Err(); err != nil {
		return err
	}
	handler, err := r.handlerFor(mode)
	if err != nil {
		return err
	}
	return handler.HandleASRFinal(ctx, turn, result)
}

func (r *modeRouter) handlerFor(mode realtimev1.Mode) (pipeline.ASRFinalHandler, error) {
	if r == nil {
		return nil, ErrDependencyRequired
	}
	handler, ok := r.handlers[mode]
	if !mode.Valid() || !ok {
		// 未注册模式必须明确失败，不能为了兼容而回退到同传，否则会把
		// 未来模式的输入误当成翻译内容。
		return nil, ErrModeNotAvailable
	}
	return handler, nil
}

func (r *modeRouter) availableModes() []realtimev1.Mode {
	if r == nil {
		return nil
	}
	// 只返回值类型的模式列表；调用方修改返回 slice 不会改变 Router 注册表。
	modes := make([]realtimev1.Mode, 0, len(r.handlers))
	for mode := range r.handlers {
		modes = append(modes, mode)
	}
	return modes
}
