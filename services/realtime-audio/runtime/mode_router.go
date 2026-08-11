package runtime

import (
	"context"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

// modeRouter 持有不可变的“业务模式 -> final Handler”注册表。
// 注册表在构造时复制，运行期间不允许临时追加或覆盖，避免不同请求看到
// 不一致的 Handler 能力。当前默认模式保持 interpretation，直到后续阶段
// 在 Turn 打开时固定模式快照。
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

// HandleASRFinal 实现公共 ASR final 接口，并沿用旧调用的 interpretation 默认值。
// 目前 Turn 还没有携带模式快照，因此所有普通 final 都从这里进入同传 Handler；
// 后续接入快照后，应由调用方使用 Dispatch 传入明确模式，不能依赖当前状态临时读取。
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
	// 在调用 Handler 前再次检查 context，保证取消的 Turn 不会继续产生翻译、
	// FinalTurn 或播放副作用。Handler 自身仍需继续处理下游依赖取消。
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return ErrDependencyRequired
	}
	handler, ok := r.handlers[mode]
	if !mode.Valid() || !ok {
		// 未注册模式必须明确失败，不能为了兼容而回退到同传，否则会把
		// 未来模式的输入误当成翻译内容。
		return ErrModeNotAvailable
	}
	return handler.HandleASRFinal(ctx, turn, result)
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
