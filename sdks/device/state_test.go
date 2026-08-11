package device

import (
	"testing"
	"time"
)

func TestStateStoreRejectsStaleObservationsAndAcceptsNewRuntime(t *testing.T) {
	store, err := NewStateStore("session-1")
	if err != nil {
		t.Fatal(err)
	}
	first := makeModeState("session-1", "runtime-1", 2, ModeAssistant, time.Unix(2, 0).UTC())
	if !store.ApplyMode(first) || store.ApplyMode(first) {
		t.Fatal("duplicate mode snapshot handling is incorrect")
	}
	if store.ApplyMode(makeModeState("session-1", "runtime-1", 1, ModeInterpretation, time.Unix(3, 0).UTC())) {
		t.Fatal("older generation was accepted")
	}
	second := makeModeState("session-1", "runtime-2", 1, ModeInterpretation, time.Unix(4, 0).UTC())
	if !store.RuntimeInstanceChanged(second) || !store.ApplyMode(second) {
		t.Fatal("new runtime instance was not accepted")
	}
	lateOld := first
	lateOld.UpdatedAt = time.Unix(9, 0).UTC()
	if store.ApplyMode(lateOld) {
		t.Fatal("late old-runtime snapshot was accepted")
	}
}

func TestStateStoreConnectionVersionIsMonotonic(t *testing.T) {
	store, err := NewStateStore("session-1")
	if err != nil {
		t.Fatal(err)
	}
	base := ConnectionSnapshot{SessionID: "session-1", ConnectionID: "connection-1", State: ConnectionConnected, Version: 2, UpdatedAt: time.Unix(2, 0).UTC()}
	if !store.ApplyConnection(base) || store.ApplyConnection(base) {
		t.Fatal("connection version handling is incorrect")
	}
	base.Version = 1
	if store.ApplyConnection(base) {
		t.Fatal("older connection version was accepted")
	}
}
