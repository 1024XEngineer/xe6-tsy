package pipeline

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestProviderFailureMatrixASRDoesNotPublishDownstreamFacts(t *testing.T) {
	providerErr := errors.New("ASR provider unavailable")
	tests := []struct {
		name   string
		config asr.FakeProviderConfig
	}{
		{name: "start", config: asr.FakeProviderConfig{StartErr: providerErr}},
		{name: "finish", config: asr.FakeProviderConfig{FinishErr: providerErr}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			latency, assertFailure := failureObservation(t, "asr_"+test.name, "mock-asr")
			translator := &translate.FakeProvider{}
			ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
			finals := &recordingFinalSink{}
			usage := &recordingUsageSink{}
			runtime := &recordingRuntimeReporter{}
			service := newTestPipelineService(PipelineDependencies{
				Translator: translator, TTS: ttsProvider, FinalTurns: finals,
				Usage: usage, Audio: &recordingAudioSink{}, Runtime: runtime,
			})
			processor := NewTurnProcessor(TurnProcessorDependencies{
				ASR: asr.NewFakeProvider(test.config),
				Opener: newTestTurnOpener(&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
					SessionID: "session-1", Version: 1, Status: "active",
					LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
				}}),
				ASRProvider: "mock-asr", Pipeline: service,
				Finals: service,
			})
			service.latency = latency

			_, err := processor.ProcessAudio(t.Context(), TurnProcessRequest{
				SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1",
				SourceLanguage: "zh-CN", AudioChunks: [][]byte{{1, 2}},
			})
			if !errors.Is(err, providerErr) {
				t.Fatalf("ProcessAudio() error = %v, want provider error", err)
			}
			assertProviderFailureRuntimeStates(t, runtime,
				session.RuntimeASRProcessing, session.RuntimeListening)
			if len(usage.facts) != 0 || len(finals.events) != 0 ||
				len(translator.Requests()) != 0 || len(ttsProvider.Requests()) != 0 {
				t.Fatalf("ASR failure side effects: usage=%#v FinalTurns=%#v translation=%#v TTS=%#v",
					usage.facts, finals.events, translator.Requests(), ttsProvider.Requests())
			}
			assertFailure()
		})
	}
}

func TestProviderFailureMatrixLLMDoesNotPublishAssistantFacts(t *testing.T) {
	latency, assertFailure := failureObservation(t, "assistant_llm", "mock-assistant")
	providerErr := errors.New("assistant LLM unavailable")
	replies := &recordingAssistantReplySink{}
	usage := &recordingUsageSink{}
	runtime := &recordingRuntimeReporter{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	speech := NewSpeechOutput(SpeechOutputDependencies{
		TTS: ttsProvider, Audio: &recordingAudioSink{}, Runtime: runtime,
	})
	handler := NewAssistantHandler(AssistantHandlerDependencies{
		LLM: assistant.NewFakeProvider(assistant.FakeProviderConfig{Err: providerErr}), Provider: "mock-assistant",
		Replies: replies, Gate: acceptingAssistantReplyGate{}, Usage: usage,
		Speech: speech, Runtime: runtime, Now: func() time.Time { return time.Unix(1700000000, 0).UTC() }, Latency: latency,
	})

	err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{
		Text: "hello", SourceLanguage: "en-US",
	})
	if !errors.Is(err, providerErr) {
		t.Fatalf("HandleASRFinal() error = %v, want provider error", err)
	}
	assertProviderFailureRuntimeStates(t, runtime,
		session.RuntimeThinking, session.RuntimeListening)
	if len(replies.events) != 0 || len(usage.facts) != 0 || len(ttsProvider.Requests()) != 0 {
		t.Fatalf("LLM failure side effects: replies=%#v usage=%#v TTS=%#v",
			replies.events, usage.facts, ttsProvider.Requests())
	}
	assertFailure()
}

func TestProviderFailureMatrixTranslationDoesNotPublishFinalTurn(t *testing.T) {
	latency, assertFailure := failureObservation(t, "translation", "mock-translation")
	providerErr := errors.New("translation provider unavailable")
	finals := &recordingFinalSink{}
	usage := &recordingUsageSink{}
	runtime := &recordingRuntimeReporter{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Err: providerErr}, TranslationProvider: "mock-translation", TTS: ttsProvider,
		FinalTurns: finals, Usage: usage, Audio: &recordingAudioSink{}, Runtime: runtime,
		Latency: latency,
	})

	err := service.HandleASRFinal(t.Context(), observedFailureTurn(), asr.FinalResult{
		Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
	})
	if !errors.Is(err, providerErr) {
		t.Fatalf("HandleASRFinal() error = %v, want provider error", err)
	}
	assertProviderFailureRuntimeStates(t, runtime,
		session.RuntimeTranslating, session.RuntimeListening)
	if len(finals.events) != 0 || len(usage.facts) != 0 || len(ttsProvider.Requests()) != 0 {
		t.Fatalf("translation failure side effects: FinalTurns=%#v usage=%#v TTS=%#v",
			finals.events, usage.facts, ttsProvider.Requests())
	}
	assertFailure()
}

func TestProviderFailureMatrixTTSKeepsAcceptedFinalTurn(t *testing.T) {
	providerErr := errors.New("TTS provider unavailable")
	tests := []struct {
		name   string
		config tts.FakeProviderConfig
	}{
		{name: "start", config: tts.FakeProviderConfig{StartErr: providerErr}},
		{name: "finish", config: tts.FakeProviderConfig{FinishErr: providerErr}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			latency, assertFailure := failureObservation(t, "tts_"+test.name, "mock-tts")
			finals := &recordingFinalSink{}
			usage := &recordingUsageSink{}
			runtime := &recordingRuntimeReporter{}
			service := newTestPipelineService(PipelineDependencies{
				Translator: &translate.FakeProvider{Result: translate.Result{
					Text: "hello", Provider: "mock-translate", Model: "v1",
				}},
				TTS: tts.NewFakeProvider(test.config), TTSProvider: "mock-tts", FinalTurns: finals,
				Usage: usage, Audio: &recordingAudioSink{}, Runtime: runtime,
				Latency: latency,
			})

			err := service.HandleASRFinal(t.Context(), observedFailureTurn(), asr.FinalResult{
				Text: "你好", SourceLanguage: "zh-CN", Provider: "mock-asr", Model: "v1",
			})
			if !errors.Is(err, providerErr) || !errors.Is(err, ErrFinalTurnAccepted) {
				t.Fatalf("HandleASRFinal() error = %v, want provider error and accepted marker", err)
			}
			assertProviderFailureRuntimeStates(t, runtime,
				session.RuntimeTranslating, session.RuntimeTTSProcessing, session.RuntimeListening)
			if len(finals.events) != 1 {
				t.Fatalf("accepted FinalTurns = %#v, want exactly one", finals.events)
			}
			if len(usage.facts) != 1 || usage.facts[0].ServiceType != "translation" {
				t.Fatalf("usage facts = %#v, want committed translation usage without fabricated TTS usage", usage.facts)
			}
			assertFailure()
		})
	}
}

func failureObservation(t *testing.T, wantStage, wantProvider string) (LatencyLogger, func()) {
	t.Helper()
	var output bytes.Buffer
	observer := &recordingProviderFailureObserver{}
	latency := LatencyLogger{
		Logger: slog.New(slog.NewJSONHandler(&output, nil)), Observer: observer,
	}
	return latency, func() {
		t.Helper()
		decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
		var fields map[string]any
		for decoder.More() {
			var candidate map[string]any
			if err := decoder.Decode(&candidate); err != nil {
				t.Fatalf("decode provider failure log: %v (%s)", err, output.Bytes())
			}
			if candidate["msg"] == "realtime provider failed" {
				fields = candidate
			}
		}
		if fields == nil {
			t.Fatalf("provider failure log not found: %s", output.Bytes())
		}
		for key, want := range map[string]any{
			"stage": wantStage, "provider": wantProvider, "session_id": "session-1",
			"runtime_instance_id": "runtime-1",
		} {
			if got := fields[key]; got != want {
				t.Fatalf("provider failure field %s = %#v, want %#v", key, got, want)
			}
		}
		if turnID, ok := fields["turn_id"].(string); !ok || turnID == "" {
			t.Fatalf("provider failure turn_id = %#v", fields["turn_id"])
		}
		if mode, ok := fields["mode"].(string); !ok || mode == "" || fields["generation"] == nil {
			t.Fatalf("provider failure mode identity = %#v/%#v", fields["mode"], fields["generation"])
		}
		if observer.calls != 1 || observer.stage != wantStage || observer.provider != wantProvider {
			t.Fatalf("provider failure observer = %#v", observer)
		}
	}
}

func observedFailureTurn() TurnContext {
	turn := testTurn()
	turn.Mode = TurnModeSnapshot{SessionID: turn.SessionID, RuntimeInstanceID: "runtime-1", Mode: "interpretation", Generation: 1}
	return turn
}

func assertProviderFailureRuntimeStates(t *testing.T, reporter *recordingRuntimeReporter, wants ...session.RuntimeState) {
	t.Helper()
	if len(reporter.updates) != len(wants) {
		t.Fatalf("runtime updates = %#v, want states %v", reporter.updates, wants)
	}
	for index, want := range wants {
		if reporter.updates[index].RuntimeState != want {
			t.Fatalf("runtime update %d = %#v, want state %q", index, reporter.updates[index], want)
		}
	}
	listening := reporter.updates[len(reporter.updates)-1]
	if listening.RuntimeState != session.RuntimeListening || listening.CurrentTurnID != nil || listening.CurrentPlaybackID != nil {
		t.Fatalf("final runtime update = %#v, want listening without active identifiers", listening)
	}
}
