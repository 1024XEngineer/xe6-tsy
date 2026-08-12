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

func TestWakeCommandControllerStopClosesWindowAndRejectsLateWake(t *testing.T) {
	window := &fakeCommandWindow{active: true}
	engine := &fakeWakeEngine{}
	controller := NewWakeCommandController(engine, window)
	if err := controller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	lateHandler := engine.handler
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if window.closeCalls != 1 || window.Active() {
		t.Fatalf("window close calls=%d active=%v", window.closeCalls, window.Active())
	}

	lateHandler(WakeWordEvent{Phrase: "小灵"})
	if window.openCalls != 0 {
		t.Fatalf("late wake reopened window: open calls=%d", window.openCalls)
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
	openCalls  int
	closeCalls int
	duration   time.Duration
	active     bool
}

func (f *fakeCommandWindow) Open(_ context.Context, duration time.Duration) error {
	f.openCalls++
	f.duration = duration
	f.active = true
	return nil
}
func (f *fakeCommandWindow) Close(context.Context) error {
	f.closeCalls++
	f.active = false
	return nil
}
func (f *fakeCommandWindow) Active() bool { return f.active }
