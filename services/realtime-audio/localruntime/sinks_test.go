package localruntime

import (
	"context"
	"encoding/json"
	"errors"
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
