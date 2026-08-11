package pipeline

import (
	"errors"
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
				Pipeline: service,
				Finals:   service,
			})

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
		})
	}
}

func TestProviderFailureMatrixLLMDoesNotPublishAssistantFacts(t *testing.T) {
	providerErr := errors.New("assistant LLM unavailable")
	replies := &recordingAssistantReplySink{}
	usage := &recordingUsageSink{}
	runtime := &recordingRuntimeReporter{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	speech := NewSpeechOutput(SpeechOutputDependencies{
		TTS: ttsProvider, Audio: &recordingAudioSink{}, Runtime: runtime,
	})
	handler := NewAssistantHandler(AssistantHandlerDependencies{
		LLM:     assistant.NewFakeProvider(assistant.FakeProviderConfig{Err: providerErr}),
		Replies: replies, Gate: acceptingAssistantReplyGate{}, Usage: usage,
		Speech: speech, Runtime: runtime, Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
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
}

func TestProviderFailureMatrixTranslationDoesNotPublishFinalTurn(t *testing.T) {
	providerErr := errors.New("translation provider unavailable")
	finals := &recordingFinalSink{}
	usage := &recordingUsageSink{}
	runtime := &recordingRuntimeReporter{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	service := newTestPipelineService(PipelineDependencies{
		Translator: &translate.FakeProvider{Err: providerErr}, TTS: ttsProvider,
		FinalTurns: finals, Usage: usage, Audio: &recordingAudioSink{}, Runtime: runtime,
	})

	err := service.HandleASRFinal(t.Context(), testTurn(), asr.FinalResult{
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
			finals := &recordingFinalSink{}
			usage := &recordingUsageSink{}
			runtime := &recordingRuntimeReporter{}
			service := newTestPipelineService(PipelineDependencies{
				Translator: &translate.FakeProvider{Result: translate.Result{
					Text: "hello", Provider: "mock-translate", Model: "v1",
				}},
				TTS: tts.NewFakeProvider(test.config), FinalTurns: finals,
				Usage: usage, Audio: &recordingAudioSink{}, Runtime: runtime,
			})

			err := service.HandleASRFinal(t.Context(), testTurn(), asr.FinalResult{
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
		})
	}
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
