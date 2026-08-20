package pipeline

import (
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestAssistantHandlerRejectsEveryMissingDependency(t *testing.T) {
	valid := func() AssistantHandlerDependencies {
		runtime := &recordingRuntimeReporter{}
		return AssistantHandlerDependencies{
			LLM: successfulAssistantLLM(), Replies: &recordingAssistantReplySink{}, Gate: acceptingAssistantReplyGate{}, Usage: &recordingUsageSink{},
			Speech:  NewSpeechOutput(SpeechOutputDependencies{TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}), Audio: &recordingAudioSink{}, Runtime: runtime}),
			Runtime: runtime,
		}
	}
	tests := []struct {
		name  string
		build func() *AssistantHandler
	}{
		{name: "nil handler", build: func() *AssistantHandler { return nil }},
		{name: "llm", build: func() *AssistantHandler { deps := valid(); deps.LLM = nil; return NewAssistantHandler(deps) }},
		{name: "replies", build: func() *AssistantHandler { deps := valid(); deps.Replies = nil; return NewAssistantHandler(deps) }},
		{name: "gate", build: func() *AssistantHandler { deps := valid(); deps.Gate = nil; return NewAssistantHandler(deps) }},
		{name: "usage", build: func() *AssistantHandler { deps := valid(); deps.Usage = nil; return NewAssistantHandler(deps) }},
		{name: "speech", build: func() *AssistantHandler { deps := valid(); deps.Speech = nil; return NewAssistantHandler(deps) }},
		{name: "runtime", build: func() *AssistantHandler { deps := valid(); deps.Runtime = nil; return NewAssistantHandler(deps) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.build().HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "hello", SourceLanguage: "en-US"}); !errors.Is(err, ErrPipelineDependencyRequired) {
				t.Fatalf("HandleASRFinal() error = %v, want ErrPipelineDependencyRequired", err)
			}
		})
	}
}

func TestAssistantHandlerRejectsReplyWithoutSourceOrReplyLanguage(t *testing.T) {
	handler := newAssistantMutationHandler(assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{Text: "answer", Provider: "mock", Model: "v1"}}), &recordingUsageSink{}, &recordingRuntimeReporter{})

	err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "question"})
	if !errors.Is(err, ErrAssistantReplyInvalid) {
		t.Fatalf("HandleASRFinal() error = %v, want ErrAssistantReplyInvalid", err)
	}
}

func TestAssistantHandlerClassifiesSupersededProcessingRuntime(t *testing.T) {
	runtime := stateFailingRuntimeReporter{failState: session.RuntimeAssistantProcessing, err: session.ErrRuntimeIdentityConflict}
	handler := newAssistantMutationHandler(successfulAssistantLLM(), &recordingUsageSink{}, runtime)

	err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "question", SourceLanguage: "en-US"})
	if !errors.Is(err, ErrTurnSuperseded) {
		t.Fatalf("HandleASRFinal() error = %v, want ErrTurnSuperseded", err)
	}
}

func TestAssistantHandlerPreservesAcceptedErrorsAtEachPostCommitBoundary(t *testing.T) {
	wantErr := errors.New("usage unavailable")
	tests := []struct {
		name      string
		usageType string
		runtime   session.RuntimeStateReporter
		gate      AssistantReplyCommitGate
		wantUsage int
	}{
		{name: "accepted LLM usage", usageType: "assistant_llm", runtime: &recordingRuntimeReporter{}, gate: acceptingAssistantReplyGate{}, wantUsage: 0},
		{name: "accepted TTS usage", usageType: "tts", runtime: &recordingRuntimeReporter{}, gate: acceptingAssistantReplyGate{}, wantUsage: 1},
		{name: "superseded usage", usageType: "assistant_llm", runtime: &recordingRuntimeReporter{}, gate: staleAssistantReplyGate{}, wantUsage: 0},
		{name: "accepted restore", usageType: "", runtime: stateFailingRuntimeReporter{failState: session.RuntimeListening, err: wantErr}, gate: acceptingAssistantReplyGate{}, wantUsage: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage := &recordingUsageSink{failService: test.usageType, err: wantErr}
			handler := newAssistantMutationHandlerWithGate(successfulAssistantLLM(), usage, test.runtime, test.gate)
			err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "question", SourceLanguage: "en-US"})
			if test.name == "superseded usage" {
				if !errors.Is(err, wantErr) {
					t.Fatalf("HandleASRFinal() error = %v, want superseded usage error", err)
				}
			} else if !errors.Is(err, ErrAssistantReplyAccepted) {
				t.Fatalf("HandleASRFinal() error = %v, want ErrAssistantReplyAccepted", err)
			}
			if len(usage.facts) != test.wantUsage {
				t.Fatalf("usage facts = %#v, want %d successful facts", usage.facts, test.wantUsage)
			}
		})
	}
}

func newAssistantMutationHandler(llm assistant.Provider, usage UsageFactSink, runtime session.RuntimeStateReporter) *AssistantHandler {
	return newAssistantMutationHandlerWithGate(llm, usage, runtime, acceptingAssistantReplyGate{})
}

func newAssistantMutationHandlerWithGate(llm assistant.Provider, usage UsageFactSink, runtime session.RuntimeStateReporter, gate AssistantReplyCommitGate) *AssistantHandler {
	return NewAssistantHandler(AssistantHandlerDependencies{
		LLM: llm, Provider: "mock-assistant", Replies: &recordingAssistantReplySink{}, Gate: gate, Usage: usage,
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS:   tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
			Audio: &recordingAudioSink{}, Runtime: runtime,
		}),
		Runtime: runtime,
	})
}
