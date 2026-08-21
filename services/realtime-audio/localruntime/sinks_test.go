package localruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

type recordingDataChannelFailures struct{ calls int }

func (r *recordingDataChannelFailures) RecordDataChannelFailure() { r.calls++ }

type notifyingDataChannelFailures struct {
	once   sync.Once
	called chan struct{}
}

func (n *notifyingDataChannelFailures) RecordDataChannelFailure() {
	n.once.Do(func() { close(n.called) })
}

func TestFrontendTranslationFinalJSONShape(t *testing.T) {
	event := recordsv1.FinalTurnEvent{
		EventVersion:          recordsv1.FinalTurnEventVersion,
		EventID:               "evt_1",
		TraceID:               "trace_1",
		TurnID:                "turn_1",
		SessionID:             "vs_1",
		SequenceNo:            1,
		SourceLanguage:        "zh-CN",
		TargetLanguage:        "en-US",
		SourceText:            "你好",
		TranslatedText:        "Hello",
		SpeakerCode:           recordsv1.PendingSpeakerCode,
		LanguageConfigVersion: 1,
		AttributionStatus:     recordsv1.AttributionPending,
		StartedAt:             time.Unix(1, 0).UTC(),
		EndedAt:               time.Unix(2, 0).UTC(),
		OccurredAt:            time.Unix(2, 0).UTC(),
	}
	payload := FrontendTranslationFinal{
		Type:            "translation.final",
		Event:           "translation.final",
		TurnID:          event.TurnID,
		ID:              event.EventID,
		SessionID:       event.SessionID,
		SourceText:      event.SourceText,
		TranslatedText:  event.TranslatedText,
		SourceLanguage:  event.SourceLanguage,
		TargetLanguage:  event.TargetLanguage,
		SequenceNo:      event.SequenceNo,
		LanguageConfigV: event.LanguageConfigVersion,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != "translation.final" || decoded["translated_text"] != "Hello" {
		t.Fatalf("payload = %#v", decoded)
	}
}

func TestFrontendAssistantReplyJSONShape(t *testing.T) {
	payload := FrontendAssistantReply{
		Type: "assistant.reply", Event: "assistant.reply",
		EventVersion: realtimev1.AssistantReplyEventVersion, ID: "reply-1",
		TraceID: "trace-1", SessionID: "session-1", TurnID: "turn-1",
		RuntimeInstanceID: "runtime-1", Generation: 2,
		Text: "Hello", Language: "en-US", OccurredAt: time.Unix(2, 0).UTC(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != "assistant.reply" || decoded["text"] != "Hello" || decoded["runtime_instance_id"] != "runtime-1" {
		t.Fatalf("payload = %#v", decoded)
	}
}

func TestFrontendASRPartialJSONShape(t *testing.T) {
	payload := realtimev1.ASRPartialEvent{
		Type: realtimev1.ASRPartialTopic, EventVersion: realtimev1.ASRPartialEventVersion,
		SessionID: "session-1", TurnID: "turn-1", Text: "你好",
		OccurredAt: time.Unix(2, 0).UTC(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != realtimev1.ASRPartialTopic || decoded["event_version"] != float64(realtimev1.ASRPartialEventVersion) || decoded["text"] != "你好" {
		t.Fatalf("payload = %#v", decoded)
	}
	if _, present := decoded["source_language"]; present {
		t.Fatalf("payload unexpectedly contains unresolved source language: %#v", decoded)
	}
}

func TestDataChannelASRPartialObserverTreatsDeliveryAsBestEffort(t *testing.T) {
	event := realtimev1.ASRPartialEvent{
		Type: realtimev1.ASRPartialTopic, EventVersion: realtimev1.ASRPartialEventVersion,
		SessionID: "session-1", TurnID: "turn-1", Text: "你好", OccurredAt: time.Unix(2, 0).UTC(),
	}

	t.Run("invalid event is ignored", func(t *testing.T) {
		failures := &recordingDataChannelFailures{}
		DataChannelASRPartialObserver{Failures: failures}.ObserveASRPartial(context.Background(), realtimev1.ASRPartialEvent{})
		if failures.calls != 0 {
			t.Fatalf("invalid event recorded delivery failure: %d", failures.calls)
		}
	})

	t.Run("unavailable media records failure without returning", func(t *testing.T) {
		failures := &recordingDataChannelFailures{}
		DataChannelASRPartialObserver{Media: stubMediaLookup{}, Failures: failures}.ObserveASRPartial(context.Background(), event)
		if failures.calls != 1 {
			t.Fatalf("media lookup failure count = %d, want 1", failures.calls)
		}
	})

	t.Run("missing media and channel record failure", func(t *testing.T) {
		for _, lookup := range []MediaLookup{
			mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) { return nil, nil }),
			mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) { return &fakeMediaTransport{}, nil }),
		} {
			failures := &recordingDataChannelFailures{}
			DataChannelASRPartialObserver{Media: lookup, Failures: failures}.ObserveASRPartial(context.Background(), event)
			if failures.calls != 1 {
				t.Fatalf("unavailable media failure count = %d, want 1", failures.calls)
			}
		}
	})
}

func TestDataChannelPhraseSubtitleObserverTreatsDeliveryAsBestEffort(t *testing.T) {
	event := realtimev1.PhraseSubtitleEvent{
		Type: realtimev1.PhraseSubtitleTopic, EventVersion: realtimev1.PhraseSubtitleEventVersion,
		SessionID: "session-1", UtteranceID: "turn-1", PhraseSequence: 1,
		SourceText: "你好", Status: realtimev1.PhraseSubtitleSourceStable, OccurredAt: time.Unix(2, 0).UTC(),
	}

	t.Run("invalid event is ignored", func(t *testing.T) {
		failures := &recordingDataChannelFailures{}
		DataChannelPhraseSubtitleObserver{Failures: failures}.ObservePhraseSubtitle(context.Background(), realtimev1.PhraseSubtitleEvent{})
		if failures.calls != 0 {
			t.Fatalf("invalid event recorded delivery failure: %d", failures.calls)
		}
	})
	t.Run("unavailable media records failure", func(t *testing.T) {
		failures := &recordingDataChannelFailures{}
		DataChannelPhraseSubtitleObserver{Media: stubMediaLookup{}, Failures: failures}.ObservePhraseSubtitle(context.Background(), event)
		if failures.calls != 1 {
			t.Fatalf("unavailable media failure count = %d, want 1", failures.calls)
		}
	})
}

func TestStaticLanguageConfigReaderReturnsBilingualPairs(t *testing.T) {
	snapshot, err := (StaticLanguageConfigReader{}).GetCurrentConfig(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("GetCurrentConfig() error = %v", err)
	}
	if snapshot.Status != "active" || len(snapshot.LanguagePairs) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestMemoryUsageSinkRecordsFacts(t *testing.T) {
	t.Parallel()
	sink := &MemoryUsageSink{}
	fact := pipeline.UsageFact{ID: "usage-1", SessionID: "session-1", TurnID: "turn-1"}
	if err := sink.Publish(context.Background(), fact); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := sink.Facts()
	if len(got) != 1 || got[0].ID != "usage-1" {
		t.Fatalf("Facts() = %#v", got)
	}
	got[0].ID = "mutated"
	if sink.Facts()[0].ID != "usage-1" {
		t.Fatal("Facts() did not copy stored slice")
	}
}

func TestDataChannelFinalTurnSinkPublish(t *testing.T) {
	t.Parallel()

	event := recordsv1.FinalTurnEvent{
		EventID:               "evt_1",
		TurnID:                "turn_1",
		SessionID:             "vs_1",
		SequenceNo:            1,
		SourceLanguage:        "zh-CN",
		TargetLanguage:        "en-US",
		SourceText:            "你好",
		TranslatedText:        "Hello",
		LanguageConfigVersion: 1,
	}

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		err := (DataChannelFinalTurnSink{}).Publish(canceled, event)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("nil media is noop", func(t *testing.T) {
		t.Parallel()
		failures := &recordingDataChannelFailures{}
		if err := (DataChannelFinalTurnSink{Failures: failures}).Publish(context.Background(), event); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if failures.calls != 1 {
			t.Fatalf("failures = %d, want 1", failures.calls)
		}
	})

	t.Run("media lookup error is best-effort", func(t *testing.T) {
		t.Parallel()
		sink := DataChannelFinalTurnSink{Media: stubMediaLookup{}}
		if err := sink.Publish(context.Background(), event); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	})

	t.Run("nil translation events is best-effort", func(t *testing.T) {
		t.Parallel()
		sink := DataChannelFinalTurnSink{
			Media: mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) {
				return &fakeMediaTransport{}, nil
			}),
		}
		if err := sink.Publish(context.Background(), event); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	})

	t.Run("discard audio sink accepts chunks", func(t *testing.T) {
		t.Parallel()
		if err := (DiscardAudioSink{}).Publish(context.Background(), pipeline.AudioChunk{Data: []byte{1}}); err != nil {
			t.Fatalf("DiscardAudioSink.Publish: %v", err)
		}
	})
}

func TestDataChannelAssistantReplySinkReportsDeliveryFailures(t *testing.T) {
	t.Parallel()
	event := realtimev1.AssistantReplyEvent{SessionID: "vs_1", EventID: "reply-1"}

	t.Run("missing media", func(t *testing.T) {
		failures := &recordingDataChannelFailures{}
		err := (DataChannelAssistantReplySink{Failures: failures}).Publish(context.Background(), event)
		if !errors.Is(err, ErrAssistantReplyMediaUnavailable) {
			t.Fatalf("Publish error = %v, want media unavailable", err)
		}
		if failures.calls != 1 {
			t.Fatalf("failures = %d, want 1", failures.calls)
		}
	})
	t.Run("media lookup failure", func(t *testing.T) {
		err := (DataChannelAssistantReplySink{Media: stubMediaLookup{}}).Publish(context.Background(), event)
		if err == nil {
			t.Fatal("Publish error = nil, want lookup failure")
		}
	})
	t.Run("channel unavailable", func(t *testing.T) {
		sink := DataChannelAssistantReplySink{
			Media: mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) {
				return &fakeMediaTransport{events: &webrtc.PionEventSink{}}, nil
			}),
		}
		err := sink.Publish(context.Background(), event)
		if !errors.Is(err, ErrAssistantReplyChannelUnavailable) {
			t.Fatalf("Publish error = %v, want channel unavailable", err)
		}
	})
}

func TestDataChannelCommandResultSinkReportsUnavailableMedia(t *testing.T) {
	t.Parallel()
	failures := &notifyingDataChannelFailures{called: make(chan struct{})}
	sink := NewDataChannelCommandResultSink(nil, failures)
	event := realtimev1.CommandResultEvent{
		Type: realtimev1.CommandResultTopic, EventVersion: realtimev1.CommandResultEventVersion,
		CommandID: "wake-1", SessionID: "session-1", Status: realtimev1.CommandResultFailed,
		Message: "命令未执行，原模式保持不变", OccurredAt: time.Unix(2, 0).UTC(),
	}
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case <-failures.called:
	case <-time.After(time.Second):
		t.Fatal("missing media failure was not observed")
	}
}

func TestDataChannelCommandResultSinkQueuesWithoutWaitingForTransport(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	sink := NewDataChannelCommandResultSink(mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) {
		startedOnce.Do(func() { close(started) })
		<-release
		return nil, errors.New("closed")
	}), nil)
	event := realtimev1.CommandResultEvent{
		Type: realtimev1.CommandResultTopic, EventVersion: realtimev1.CommandResultEventVersion,
		CommandID: "wake-1", SessionID: "session-1", Status: realtimev1.CommandResultFailed,
		Message: "命令未执行，原模式保持不变", OccurredAt: time.Unix(2, 0).UTC(),
	}
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("delivery worker did not receive event")
	}
	if err := sink.Publish(context.Background(), func() realtimev1.CommandResultEvent {
		next := event
		next.CommandID = "wake-2"
		return next
	}()); err != nil {
		t.Fatalf("second Publish() blocked or failed: %v", err)
	}
	close(release)
}

func TestDataChannelCommandResultSinkIsolatesSlowSessions(t *testing.T) {
	t.Parallel()
	slowStarted := make(chan struct{})
	fastStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var slowOnce sync.Once
	var fastOnce sync.Once
	sink := NewDataChannelCommandResultSink(mediaLookupFunc(func(ctx context.Context, sessionID string) (webrtc.MediaTransport, error) {
		switch sessionID {
		case "session-slow":
			slowOnce.Do(func() { close(slowStarted) })
			select {
			case <-releaseSlow:
			case <-ctx.Done():
			}
		case "session-fast":
			fastOnce.Do(func() { close(fastStarted) })
		}
		return nil, errors.New("unavailable")
	}), nil)
	event := realtimev1.CommandResultEvent{
		Type: realtimev1.CommandResultTopic, EventVersion: realtimev1.CommandResultEventVersion,
		CommandID: "wake-slow", SessionID: "session-slow", Status: realtimev1.CommandResultFailed,
		Message: "命令未执行，原模式保持不变", OccurredAt: time.Unix(2, 0).UTC(),
	}
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish(slow) error = %v", err)
	}
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow session worker did not start")
	}

	event.CommandID = "wake-fast"
	event.SessionID = "session-fast"
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish(fast) error = %v", err)
	}
	select {
	case <-fastStarted:
	case <-time.After(time.Second):
		t.Fatal("fast session was blocked behind the slow session")
	}
	close(releaseSlow)
}

func TestEnergySpeechClassifierDetectsLoudFrame(t *testing.T) {
	quiet := make([]byte, 320)
	loud := make([]byte, 320)
	for i := 0; i < len(loud); i += 2 {
		loud[i] = 0x00
		loud[i+1] = 0x40
	}
	classifier := EnergySpeechClassifier{}
	quietFrame, err := audio.NewFrame(quiet, audio.SupportedSampleRate, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	loudFrame, err := audio.NewFrame(loud, audio.SupportedSampleRate, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if classifier.Speech(quietFrame) {
		t.Fatal("quiet frame classified as speech")
	}
	if !classifier.Speech(loudFrame) {
		t.Fatal("loud frame not classified as speech")
	}
}
