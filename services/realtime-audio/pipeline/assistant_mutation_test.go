package pipeline

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestAssistantHandlerNormalizesReplyLanguageBeforePublishing(t *testing.T) {
	replies := &recordingAssistantReplySink{}
	llm := assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{
		Text: "answer", Language: "en_gb", Provider: "mock", Model: "v1",
	}})
	handler := newTestAssistantHandler(llm, replies, &recordingUsageSink{},
		tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}), acceptingAssistantReplyGate{}, time.Now())

	if err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "question", SourceLanguage: "en-US"}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if len(replies.events) != 1 || replies.events[0].Language != "en-GB" {
		t.Fatalf("published reply = %#v, want normalized en-GB", replies.events)
	}
}

func TestAssistantHandlerObservesInvalidReplyFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		llm   assistant.Result
		input asr.FinalResult
	}{
		{name: "empty text", llm: assistant.Result{Text: " ", Language: "en-US", Provider: "mock", Model: "v1"}, input: asr.FinalResult{Text: "question", SourceLanguage: "en-US"}},
		{name: "empty language", llm: assistant.Result{Text: "answer", Provider: "mock", Model: "v1"}, input: asr.FinalResult{Text: "question"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			observer := &recordingProviderFailureObserver{}
			handler := newTestAssistantHandler(assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: test.llm}),
				&recordingAssistantReplySink{}, &recordingUsageSink{}, tts.NewFakeProvider(tts.FakeProviderConfig{}),
				acceptingAssistantReplyGate{}, time.Now())
			handler.latency = LatencyLogger{Observer: observer}
			if err := handler.HandleASRFinal(t.Context(), assistantTurn(), test.input); !errors.Is(err, ErrAssistantReplyInvalid) {
				t.Fatalf("HandleASRFinal() error = %v, want ErrAssistantReplyInvalid", err)
			}
			if observer.calls != 1 || observer.stage != "assistant_result" {
				t.Fatalf("provider failure observer = %#v, want assistant_result", observer)
			}
		})
	}
}

func TestAssistantHandlerReportsReplyCheckpoint(t *testing.T) {
	var output bytes.Buffer
	handler := newTestAssistantHandler(successfulAssistantLLM(), &recordingAssistantReplySink{}, &recordingUsageSink{},
		tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}), acceptingAssistantReplyGate{}, time.Now())
	handler.latency = LatencyLogger{Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	if err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "question", SourceLanguage: "en-US"}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"stage":"assistant_reply_done"`)) {
		t.Fatalf("checkpoint log = %s, want assistant_reply_done", output.String())
	}
}

func TestAssistantHandlerRejectsInvalidLLMUsageFact(t *testing.T) {
	turn := assistantTurn()
	turn.ID = ""
	handler := newTestAssistantHandler(successfulAssistantLLM(), &recordingAssistantReplySink{}, &recordingUsageSink{},
		tts.NewFakeProvider(tts.FakeProviderConfig{}), acceptingAssistantReplyGate{}, time.Now())
	err := handler.HandleASRFinal(t.Context(), turn, asr.FinalResult{Text: "question", SourceLanguage: "en-US"})
	if err == nil || !strings.Contains(err.Error(), "prepare assistant LLM usage") {
		t.Fatalf("HandleASRFinal() error = %v, want prepared LLM usage error", err)
	}
}

func TestAssistantHandlerRejectsInvalidTTSUsageFactAfterAccept(t *testing.T) {
	usage := &recordingUsageSink{}
	handler := newTestAssistantHandler(successfulAssistantLLM(), &recordingAssistantReplySink{}, usage,
		tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{AudioDuration: time.Second}}), acceptingAssistantReplyGate{}, time.Now())
	err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "question", SourceLanguage: "en-US"})
	if !errors.Is(err, ErrAssistantReplyAccepted) {
		t.Fatalf("HandleASRFinal() error = %v, want ErrAssistantReplyAccepted", err)
	}
	if len(usage.facts) != 1 || usage.facts[0].ServiceType != "assistant_llm" {
		t.Fatalf("usage facts = %#v, want only accepted LLM usage", usage.facts)
	}
}

func TestPublishLLMUsageIfPresentHonorsProviderModelAndTokenBoundaries(t *testing.T) {
	handler := newAssistantMutationHandler(successfulAssistantLLM(), &recordingUsageSink{}, &recordingRuntimeReporter{})
	tests := []struct {
		name   string
		result assistant.Result
		want   int
	}{
		{name: "missing provider", result: assistant.Result{Model: "v1", InputTokens: 1}, want: 0},
		{name: "missing model", result: assistant.Result{Provider: "mock", InputTokens: 1}, want: 0},
		{name: "no tokens", result: assistant.Result{Provider: "mock", Model: "v1"}, want: 0},
		{name: "input tokens", result: assistant.Result{Provider: "mock", Model: "v1", InputTokens: 1}, want: 1},
		{name: "output tokens", result: assistant.Result{Provider: "mock", Model: "v1", OutputTokens: 1}, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage := &recordingUsageSink{}
			handler.usage = usage
			if err := handler.publishLLMUsageIfPresent(t.Context(), assistantTurn(), test.result); err != nil {
				t.Fatalf("publishLLMUsageIfPresent() error = %v", err)
			}
			if len(usage.facts) != test.want {
				t.Fatalf("usage facts = %#v, want %d", usage.facts, test.want)
			}
		})
	}
}

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
