package asr

import (
	"context"
	"errors"
	"testing"
)

func TestFakeProviderStreamsPartialAndFinalEvents(t *testing.T) {
	provider := NewFakeProvider(FakeProviderConfig{
		Partial: Event{Type: EventPartial, Text: "hello"},
		Final:   FinalResult{Text: "hello world", SourceLanguage: "en-US", Confidence: 0.98},
	})
	stream, err := provider.StartStream(context.Background(), StreamRequest{SessionID: "session-1", TurnID: "turn-1"})
	if err != nil {
		t.Fatalf("StartStream() error = %v", err)
	}
	if err := stream.PushAudio(context.Background(), []byte{1, 2}); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	events := stream.Events()
	partial := <-events
	final := <-events
	if partial.Type != EventPartial || partial.Text != "hello" {
		t.Fatalf("partial event = %#v", partial)
	}
	if final.Type != EventFinal || final.Final == nil || final.Final.Text != "hello world" {
		t.Fatalf("final event = %#v", final)
	}
	result, err := stream.Finish(context.Background())
	if err != nil || result.Text != "hello world" {
		t.Fatalf("Finish() = %#v, %v", result, err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestFakeProviderHonorsCancellationAndInjectedError(t *testing.T) {
	wantErr := errors.New("provider unavailable")
	provider := NewFakeProvider(FakeProviderConfig{StartErr: wantErr})
	if _, err := provider.StartStream(context.Background(), StreamRequest{SessionID: "session-1"}); !errors.Is(err, wantErr) {
		t.Fatalf("StartStream() error = %v, want %v", err, wantErr)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	provider = NewFakeProvider(FakeProviderConfig{})
	if _, err := provider.StartStream(canceled, StreamRequest{SessionID: "session-1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled StartStream() error = %v", err)
	}
}

var _ Provider = (*FakeProvider)(nil)
