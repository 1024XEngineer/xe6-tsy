package ingress

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

type recordingTranslation struct{ calls []pipeline.TurnProcessRequest }

func (r *recordingTranslation) ProcessAudio(_ context.Context, request pipeline.TurnProcessRequest) (pipeline.TurnContext, error) {
	r.calls = append(r.calls, request)
	return pipeline.TurnContext{}, nil
}

type recordingCommandSink struct{ captures []CommandCapture }

func (s *recordingCommandSink) PublishCommand(_ context.Context, capture CommandCapture) error {
	s.captures = append(s.captures, capture)
	return nil
}

func TestDispatcherRoutesCommandAudioWithoutTranslation(t *testing.T) {
	translation := &recordingTranslation{}
	commands := &recordingCommandSink{}
	dispatcher, err := NewDispatcher(DispatcherDependencies{
		Translation: translation,
		Commands:    commands,
		Now:         func() time.Time { return time.Unix(100, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	if _, err := dispatcher.ProcessAudio(context.Background(), pipeline.TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", AudioChunks: [][]byte{{1}},
	}); err != nil {
		t.Fatalf("normal ProcessAudio() error = %v", err)
	}
	window, err := dispatcher.ArmCommandCapture("capture-1", 10*time.Second)
	if err != nil {
		t.Fatalf("ArmCommandCapture() error = %v", err)
	}
	if window.CaptureID != "capture-1" || window.Mode != ModeCommandCapture {
		t.Fatalf("window = %#v", window)
	}
	if _, err := dispatcher.ProcessAudio(context.Background(), pipeline.TurnProcessRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1", SourceLanguage: "zh-CN",
		StartedAt: time.Unix(101, 0).UTC(), AudioChunks: [][]byte{{2}, {3}}, Generation: window.Generation,
	}); err != nil {
		t.Fatalf("command ProcessAudio() error = %v", err)
	}
	if len(translation.calls) != 1 {
		t.Fatalf("translation calls = %d, want 1 normal call", len(translation.calls))
	}
	if err := dispatcher.CloseCommandCapture(context.Background(), "capture-1"); err != nil {
		t.Fatalf("CloseCommandCapture() error = %v", err)
	}
	if len(commands.captures) != 1 {
		t.Fatalf("command captures = %d, want 1", len(commands.captures))
	}
	capture := commands.captures[0]
	if capture.CaptureID != "capture-1" || capture.SessionID != "session-1" || capture.SourceLanguage != "zh-CN" {
		t.Fatalf("capture metadata = %#v", capture)
	}
	if len(capture.AudioChunks) != 2 || capture.AudioChunks[0][0] != 2 || capture.AudioChunks[1][0] != 3 {
		t.Fatalf("capture audio = %#v", capture.AudioChunks)
	}
}

func TestDispatcherExpiresCommandWindowAndResumesTranslation(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	translation := &recordingTranslation{}
	commands := &recordingCommandSink{}
	dispatcher, err := NewDispatcher(DispatcherDependencies{
		Translation: translation,
		Commands:    commands,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	window, err := dispatcher.ArmCommandCapture("capture-2", 10*time.Second)
	if err != nil {
		t.Fatalf("ArmCommandCapture() error = %v", err)
	}
	if _, err := dispatcher.ProcessAudio(context.Background(), pipeline.TurnProcessRequest{
		SessionID: "session-2", AccountID: "account-2", AudioChunks: [][]byte{{9}}, Generation: window.Generation,
	}); err != nil {
		t.Fatalf("command ProcessAudio() error = %v", err)
	}
	if err := dispatcher.BeforeFrame(context.Background(), now.Add(10*time.Second)); err != nil {
		t.Fatalf("BeforeFrame() error = %v", err)
	}
	if len(commands.captures) != 1 || len(commands.captures[0].AudioChunks) != 1 {
		t.Fatalf("expired captures = %#v", commands.captures)
	}
	if _, err := dispatcher.ProcessAudio(context.Background(), pipeline.TurnProcessRequest{
		SessionID: "session-2", AccountID: "account-2", AudioChunks: [][]byte{{4}},
	}); err != nil {
		t.Fatalf("resumed translation ProcessAudio() error = %v", err)
	}
	if len(translation.calls) != 1 {
		t.Fatalf("translation calls = %d, want 1 after expiry", len(translation.calls))
	}
}

func TestDispatcherDropsAudioFromPreviousModeGeneration(t *testing.T) {
	translation := &recordingTranslation{}
	dispatcher, err := NewDispatcher(DispatcherDependencies{Translation: translation})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	window, err := dispatcher.ArmCommandCapture("capture-3", 10*time.Second)
	if err != nil {
		t.Fatalf("ArmCommandCapture() error = %v", err)
	}
	if err := dispatcher.CancelCommandCapture("capture-3"); err != nil {
		t.Fatalf("CancelCommandCapture() error = %v", err)
	}
	if _, err := dispatcher.ProcessAudio(context.Background(), pipeline.TurnProcessRequest{Generation: window.Generation, AudioChunks: [][]byte{{1}}}); err != nil {
		t.Fatalf("stale ProcessAudio() error = %v", err)
	}
	if len(translation.calls) != 0 {
		t.Fatal("stale command audio must not enter translation")
	}
}

func TestDispatcherRejectsInvalidCommandWindow(t *testing.T) {
	dispatcher, err := NewDispatcher(DispatcherDependencies{Translation: &recordingTranslation{}})
	if err != nil {
		t.Fatalf("NewDispatcher() error = %v", err)
	}
	if _, err := dispatcher.ArmCommandCapture("", 10*time.Second); !errors.Is(err, ErrInvalidCommandCapture) {
		t.Fatalf("empty capture id error = %v", err)
	}
	if _, err := dispatcher.ArmCommandCapture("capture-4", 11*time.Second); !errors.Is(err, ErrInvalidCommandWindow) {
		t.Fatalf("long window error = %v", err)
	}
}
