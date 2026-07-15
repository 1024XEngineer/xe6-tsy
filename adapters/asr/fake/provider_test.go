package fake

import (
	"testing"

	"github.com/1024XEngineer/xe6-tsy/apps/api/pkg/speechport"
)

func TestProvider_StartStreamReturnsScriptedEvents(t *testing.T) {
	provider := NewProvider([]speechport.ASREvent{{
		Type: speechport.ASREventPartial,
		Partial: &speechport.ASRPartial{
			SequenceNo: 1,
			Text:       "合成 partial",
		},
	}}, nil)

	stream, err := provider.StartStream(t.Context(), speechport.StartASRStreamRequest{RunID: "run-1"})
	if err != nil {
		t.Fatalf("StartStream() error = %v", err)
	}
	events := make([]speechport.ASREvent, 0)
	for event := range stream.Events() {
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Partial == nil || events[0].Partial.Text != "合成 partial" {
		t.Fatalf("events = %#v", events)
	}
	if provider.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", provider.Calls())
	}
}

func TestStream_CloseIsIdempotent(t *testing.T) {
	provider := NewProvider(nil, nil)
	stream, err := provider.StartStream(t.Context(), speechport.StartASRStreamRequest{RunID: "run-1"})
	if err != nil {
		t.Fatalf("StartStream() error = %v", err)
	}
	fakeStream := stream.(*Stream)

	if err := fakeStream.Close(t.Context()); err != nil {
		t.Fatalf("Close() first error = %v", err)
	}
	if err := fakeStream.Close(t.Context()); err != nil {
		t.Fatalf("Close() second error = %v", err)
	}
	if !fakeStream.Closed() {
		t.Fatal("stream is not marked closed")
	}
}
