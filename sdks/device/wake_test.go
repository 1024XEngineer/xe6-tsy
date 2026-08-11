package device

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWakeCommandControllerFailsOpenWhenEngineUnavailable(t *testing.T) {
	controller := NewWakeCommandController(failingWakeEngine{}, &fakeCommandWindow{})
	if err := controller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if controller.Enabled() || controller.LastError() == nil {
		t.Fatal("wake-word failure did not disable optional feature")
	}
}

func TestWakeCommandControllerOpensBoundedWindow(t *testing.T) {
	window := &fakeCommandWindow{}
	engine := &fakeWakeEngine{}
	controller := NewWakeCommandController(engine, window)
	if err := controller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine.emit(WakeWordEvent{Phrase: "小灵"})
	if window.openCalls != 1 || window.duration != 5*time.Second {
		t.Fatalf("window calls=%d duration=%s", window.openCalls, window.duration)
	}
}

type failingWakeEngine struct{}

func (failingWakeEngine) Start(context.Context, func(WakeWordEvent)) error {
	return errors.New("kws unavailable")
}
func (failingWakeEngine) Stop() error { return nil }

type fakeWakeEngine struct{ handler func(WakeWordEvent) }

func (f *fakeWakeEngine) Start(_ context.Context, handler func(WakeWordEvent)) error {
	f.handler = handler
	return nil
}
func (f *fakeWakeEngine) Stop() error              { return nil }
func (f *fakeWakeEngine) emit(event WakeWordEvent) { f.handler(event) }

type fakeCommandWindow struct {
	openCalls int
	duration  time.Duration
}

func (f *fakeCommandWindow) Open(_ context.Context, duration time.Duration) error {
	f.openCalls++
	f.duration = duration
	return nil
}
func (f *fakeCommandWindow) Close(context.Context) error { return nil }
func (f *fakeCommandWindow) Active() bool                { return false }
