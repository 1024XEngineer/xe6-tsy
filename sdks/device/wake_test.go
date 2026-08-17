package device

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWakeCommandControllerFailsOpenWhenEngineUnavailable(t *testing.T) {
	controller := NewWakeCommandController(failingWakeEngine{}, &fakeWakeSender{})
	if err := controller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if controller.Enabled() || controller.LastError() == nil {
		t.Fatal("wake-word failure did not disable optional feature")
	}
}

func TestWakeCommandControllerSendsSharedSignal(t *testing.T) {
	sender := &fakeWakeSender{}
	engine := &fakeWakeEngine{}
	controller := NewWakeCommandController(engine, sender)
	controller.now = func() time.Time { return time.Unix(20, 0).UTC() }
	controller.newSignalID = func() (string, error) { return "wake-1", nil }
	if err := controller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine.emit(WakeWordEvent{})
	if len(sender.signals) != 1 {
		t.Fatalf("signal count = %d", len(sender.signals))
	}
	want := WakeWordDetectedSignal{
		Type: WakeWordDetectedType, EventVersion: WakeWordDetectedEventVersion,
		SignalID: "wake-1", DetectedAt: time.Unix(20, 0).UTC(),
	}
	if sender.signals[0] != want {
		t.Fatalf("signal = %#v, want %#v", sender.signals[0], want)
	}
}

func TestWakeCommandControllerKeepsKWSAvailableAfterSendFailure(t *testing.T) {
	sender := &fakeWakeSender{err: errors.New("data channel unavailable")}
	engine := &fakeWakeEngine{}
	controller := NewWakeCommandController(engine, sender)
	controller.newSignalID = func() (string, error) { return "wake-1", nil }
	if err := controller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine.emit(WakeWordEvent{DetectedAt: time.Unix(10, 0).UTC()})
	if !controller.Enabled() || controller.LastError() == nil {
		t.Fatal("transient signal failure disabled local KWS or was not reported")
	}
}

func TestWakeCommandControllerStopRejectsLateWake(t *testing.T) {
	sender := &fakeWakeSender{}
	engine := &fakeWakeEngine{}
	controller := NewWakeCommandController(engine, sender)
	if err := controller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	lateHandler := engine.handler
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}

	lateHandler(WakeWordEvent{})
	if len(sender.signals) != 0 {
		t.Fatalf("late wake sent %d signals", len(sender.signals))
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

type fakeWakeSender struct {
	signals []WakeWordDetectedSignal
	err     error
}

func (s *fakeWakeSender) SendWakeWordDetected(_ context.Context, signal WakeWordDetectedSignal) error {
	s.signals = append(s.signals, signal)
	return s.err
}
