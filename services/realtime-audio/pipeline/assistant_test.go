package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestAssistantHandlerPublishesReplyUsageAndSpeech(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	llm := assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{
		Text: " 你好，我可以帮你。 ", Provider: "mock-llm", Model: "assistant-v1",
		InputTokens: 8, OutputTokens: 6,
	}})
	replies := &recordingAssistantReplySink{}
	usage := &recordingUsageSink{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1, 2}}},
		Result: tts.Result{Provider: "mock-tts", Model: "tts-v1", AudioDuration: time.Second},
	})
	handler := newTestAssistantHandler(llm, replies, usage, ttsProvider, acceptingAssistantReplyGate{}, base)

	if err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "你好", SourceLanguage: "zh-CN"}); err != nil {
		t.Fatalf("HandleASRFinal() error = %v", err)
	}
	requests := llm.Requests()
	if len(requests) != 1 || requests[0].Text != "你好" || requests[0].Language != "zh-CN" {
		t.Fatalf("LLM requests = %#v", requests)
	}
	if len(replies.events) != 1 {
		t.Fatalf("AssistantReply events = %#v", replies.events)
	}
	event := replies.events[0]
	if event.EventID != "assistant_reply_turn-1" || event.Text != "你好，我可以帮你。" || event.Language != "zh-CN" ||
		event.RuntimeInstanceID != "runtime-1" || event.Generation != 2 {
		t.Fatalf("AssistantReply = %#v", event)
	}
	if len(usage.facts) != 2 || usage.facts[0].ServiceType != "assistant_llm" || usage.facts[1].ServiceType != "tts" ||
		usage.facts[0].IdempotencyKey != "usage:turn-1:assistant_llm" {
		t.Fatalf("UsageFacts = %#v", usage.facts)
	}
	ttsRequests := ttsProvider.Requests()
	if len(ttsRequests) != 1 || ttsRequests[0].PlaybackID != "assistant_playback_turn-1" || ttsRequests[0].Text != event.Text {
		t.Fatalf("TTS requests = %#v", ttsRequests)
	}
}

func TestAssistantHandlerRejectsInvalidReply(t *testing.T) {
	llm := assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{
		Text: " ", Provider: "mock-llm", Model: "assistant-v1",
	}})
	replies := &recordingAssistantReplySink{}
	usage := &recordingUsageSink{}
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	handler := newTestAssistantHandler(llm, replies, usage, ttsProvider, acceptingAssistantReplyGate{}, time.Now())

	err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "hello", SourceLanguage: "en-US"})
	if !errors.Is(err, ErrAssistantReplyInvalid) {
		t.Fatalf("HandleASRFinal() error = %v, want ErrAssistantReplyInvalid", err)
	}
	if len(replies.events) != 0 || len(usage.facts) != 0 || len(ttsProvider.Requests()) != 0 {
		t.Fatalf("side effects = replies %d, usage %d, TTS %d", len(replies.events), len(usage.facts), len(ttsProvider.Requests()))
	}
}

func TestAssistantHandlerRecordsUsageFromFailedLLM(t *testing.T) {
	wantErr := errors.New("LLM unavailable")
	llm := assistant.NewFakeProvider(assistant.FakeProviderConfig{
		Result: assistant.Result{Provider: "mock-llm", Model: "assistant-v1", InputTokens: 4, OutputTokens: 1},
		Err:    wantErr,
	})
	usage := &recordingUsageSink{}
	handler := newTestAssistantHandler(llm, &recordingAssistantReplySink{}, usage,
		tts.NewFakeProvider(tts.FakeProviderConfig{}), acceptingAssistantReplyGate{}, time.Now())

	err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "hello", SourceLanguage: "en-US"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("HandleASRFinal() error = %v, want provider error", err)
	}
	if len(usage.facts) != 1 || usage.facts[0].ServiceType != "assistant_llm" {
		t.Fatalf("UsageFacts = %#v", usage.facts)
	}
}

func TestAssistantHandlerStopsWhenReplyCommitFailsOrIsStale(t *testing.T) {
	wantErr := errors.New("reply unavailable")
	for _, test := range []struct {
		name string
		gate AssistantReplyCommitGate
		sink *recordingAssistantReplySink
		want error
	}{
		{name: "publish failure", gate: acceptingAssistantReplyGate{}, sink: &recordingAssistantReplySink{err: wantErr}, want: wantErr},
		{name: "stale generation", gate: staleAssistantReplyGate{}, sink: &recordingAssistantReplySink{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			usage := &recordingUsageSink{}
			ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
			handler := newTestAssistantHandler(successfulAssistantLLM(), test.sink, usage, ttsProvider, test.gate, time.Now())
			err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "hello", SourceLanguage: "en-US"})
			if !errors.Is(err, test.want) {
				t.Fatalf("HandleASRFinal() error = %v, want %v", err, test.want)
			}
			if len(usage.facts) != 0 || len(ttsProvider.Requests()) != 0 {
				t.Fatalf("post-commit side effects = usage %d, TTS %d", len(usage.facts), len(ttsProvider.Requests()))
			}
		})
	}
}

func TestAssistantHandlerMarksTTSFailureAfterReplyAccepted(t *testing.T) {
	wantErr := errors.New("TTS unavailable")
	replies := &recordingAssistantReplySink{}
	usage := &recordingUsageSink{}
	handler := newTestAssistantHandler(successfulAssistantLLM(), replies, usage,
		tts.NewFakeProvider(tts.FakeProviderConfig{StartErr: wantErr}), acceptingAssistantReplyGate{}, time.Now())

	err := handler.HandleASRFinal(t.Context(), assistantTurn(), asr.FinalResult{Text: "hello", SourceLanguage: "en-US"})
	if !errors.Is(err, wantErr) || !errors.Is(err, ErrAssistantReplyAccepted) {
		t.Fatalf("HandleASRFinal() error = %v, want TTS error and accepted marker", err)
	}
	if len(replies.events) != 1 || len(usage.facts) != 1 || usage.facts[0].ServiceType != "assistant_llm" {
		t.Fatalf("accepted side effects = replies %#v, usage %#v", replies.events, usage.facts)
	}
}

func newTestAssistantHandler(llm assistant.Provider, replies AssistantReplySink, usage UsageFactSink,
	ttsProvider tts.Provider, gate AssistantReplyCommitGate, now time.Time,
) *AssistantHandler {
	runtime := &recordingRuntimeReporter{}
	speech := NewSpeechOutput(SpeechOutputDependencies{
		TTS: ttsProvider, Audio: &recordingAudioSink{}, Runtime: runtime,
	})
	return NewAssistantHandler(AssistantHandlerDependencies{
		LLM: llm, Replies: replies, Gate: gate, Usage: usage, Speech: speech, Runtime: runtime,
		Now: func() time.Time { return now },
	})
}

func successfulAssistantLLM() assistant.Provider {
	return assistant.NewFakeProvider(assistant.FakeProviderConfig{Result: assistant.Result{
		Text: "Hello, how can I help?", Language: "en-US", Provider: "mock-llm", Model: "assistant-v1",
	}})
}

func assistantTurn() TurnContext {
	return TurnContext{
		ID: "turn-1", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1",
		Mode: TurnModeSnapshot{
			SessionID: "session-1", RuntimeInstanceID: "runtime-1",
			Mode: realtimev1.ModeAssistant, Generation: 2,
		},
	}
}

type recordingAssistantReplySink struct {
	events []realtimev1.AssistantReplyEvent
	err    error
}

func (s *recordingAssistantReplySink) Publish(_ context.Context, event realtimev1.AssistantReplyEvent) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, event)
	return nil
}

type acceptingAssistantReplyGate struct{}

func (acceptingAssistantReplyGate) CommitAssistantReply(ctx context.Context, _ TurnContext, commit AssistantReplyCommit) (bool, error) {
	if err := commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

type staleAssistantReplyGate struct{}

func (staleAssistantReplyGate) CommitAssistantReply(context.Context, TurnContext, AssistantReplyCommit) (bool, error) {
	return false, nil
}
