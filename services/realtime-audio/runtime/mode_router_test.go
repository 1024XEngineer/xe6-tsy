package runtime

import (
	"context"
	"errors"
	"testing"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

func TestModeRouterUsesConfiguredDefaultHandler(t *testing.T) {
	interpretation := &recordingModeHandler{}
	router := mustModeRouter(t, realtimev1.ModeInterpretation, map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeInterpretation: interpretation,
	})
	turn := pipeline.TurnContext{ID: "turn-1", SessionID: "session-1"}
	result := asr.FinalResult{Text: "hello", SourceLanguage: "en-US"}

	if err := router.HandleASRFinal(t.Context(), turn, result); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if interpretation.calls != 1 || interpretation.turn.ID != turn.ID ||
		interpretation.turn.SessionID != turn.SessionID || interpretation.result != result {
		t.Fatalf("default handler call = %#v", interpretation)
	}
}

func TestModeRouterDispatchesExplicitRegisteredMode(t *testing.T) {
	interpretation := &recordingModeHandler{}
	assistant := &recordingModeHandler{}
	router := mustModeRouter(t, realtimev1.ModeInterpretation, map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeInterpretation: interpretation,
		realtimev1.ModeAssistant:      assistant,
	})

	if err := router.Dispatch(t.Context(), realtimev1.ModeAssistant, pipeline.TurnContext{ID: "turn-1"}, asr.FinalResult{Text: "hello"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if assistant.calls != 1 || interpretation.calls != 0 {
		t.Fatalf("handler calls = assistant %d, interpretation %d", assistant.calls, interpretation.calls)
	}
}

func TestModeRouterRejectsUnavailableModeWithoutFallback(t *testing.T) {
	interpretation := &recordingModeHandler{}
	registrations := map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeInterpretation: interpretation,
	}
	router := mustModeRouter(t, realtimev1.ModeInterpretation, registrations)
	registrations[realtimev1.ModeAssistant] = &recordingModeHandler{}

	if err := router.Dispatch(t.Context(), realtimev1.ModeAssistant, pipeline.TurnContext{}, asr.FinalResult{}); !errors.Is(err, ErrModeNotAvailable) {
		t.Fatalf("Dispatch() error = %v, want ErrModeNotAvailable", err)
	}
	if interpretation.calls != 0 {
		t.Fatalf("unavailable mode fell back to interpretation %d times", interpretation.calls)
	}
}

func TestModeRouterValidatesRegistrations(t *testing.T) {
	tests := []struct {
		name        string
		defaultMode realtimev1.Mode
		handlers    map[realtimev1.Mode]pipeline.ASRFinalHandler
		wantErr     error
	}{
		{
			name:        "invalid default",
			defaultMode: realtimev1.Mode("unknown"),
			wantErr:     ErrModeNotAvailable,
		},
		{
			name:        "default not registered",
			defaultMode: realtimev1.ModeInterpretation,
			handlers:    map[realtimev1.Mode]pipeline.ASRFinalHandler{},
			wantErr:     ErrModeNotAvailable,
		},
		{
			name:        "invalid registered mode",
			defaultMode: realtimev1.ModeInterpretation,
			handlers: map[realtimev1.Mode]pipeline.ASRFinalHandler{
				realtimev1.ModeInterpretation: &recordingModeHandler{},
				realtimev1.Mode("unknown"):    &recordingModeHandler{},
			},
			wantErr: ErrModeNotAvailable,
		},
		{
			name:        "nil handler",
			defaultMode: realtimev1.ModeInterpretation,
			handlers: map[realtimev1.Mode]pipeline.ASRFinalHandler{
				realtimev1.ModeInterpretation: nil,
			},
			wantErr: ErrDependencyRequired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newModeRouter(test.defaultMode, test.handlers); !errors.Is(err, test.wantErr) {
				t.Fatalf("newModeRouter() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestModeRouterPropagatesHandlerAndContextErrors(t *testing.T) {
	wantErr := errors.New("handler unavailable")
	handler := &recordingModeHandler{err: wantErr}
	router := mustModeRouter(t, realtimev1.ModeInterpretation, map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeInterpretation: handler,
	})

	if err := router.HandleASRFinal(t.Context(), pipeline.TurnContext{}, asr.FinalResult{}); !errors.Is(err, wantErr) {
		t.Fatalf("HandleASRFinal() error = %v, want %v", err, wantErr)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := router.HandleASRFinal(canceled, pipeline.TurnContext{}, asr.FinalResult{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled HandleASRFinal() error = %v, want context.Canceled", err)
	}
	if handler.calls != 1 {
		t.Fatalf("handler calls after canceled dispatch = %d, want 1", handler.calls)
	}
}

func mustModeRouter(
	t *testing.T,
	defaultMode realtimev1.Mode,
	handlers map[realtimev1.Mode]pipeline.ASRFinalHandler,
) *modeRouter {
	t.Helper()
	router, err := newModeRouter(defaultMode, handlers)
	if err != nil {
		t.Fatalf("newModeRouter() error = %v", err)
	}
	return router
}

type recordingModeHandler struct {
	calls  int
	turn   pipeline.TurnContext
	result asr.FinalResult
	err    error
}

func (h *recordingModeHandler) HandleASRFinal(_ context.Context, turn pipeline.TurnContext, result asr.FinalResult) error {
	h.calls++
	h.turn = turn
	h.result = result
	return h.err
}
