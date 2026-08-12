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
	replacement := ConnectionSnapshot{SessionID: "session-1", ConnectionID: "connection-2", State: ConnectionConnected, Version: 1, UpdatedAt: time.Unix(3, 0).UTC()}
	if !store.ApplyConnection(replacement) {
		t.Fatal("replacement connection was rejected")
	}
	base.Version = 9
	base.UpdatedAt = time.Unix(9, 0).UTC()
	if store.ApplyConnection(base) {
		t.Fatal("retired connection was restored")
	}
}

func TestStateStoreRejectsRetiredRuntimeOperation(t *testing.T) {
	store, err := NewStateStore("session-1")
	if err != nil {
		t.Fatal(err)
	}
	first := RuntimeSnapshot{SessionID: "session-1", StartOperationID: "start-1", RuntimeState: RuntimeListening, UpdatedAt: time.Unix(1, 0).UTC()}
	second := RuntimeSnapshot{SessionID: "session-1", StartOperationID: "start-2", RuntimeState: RuntimeListening, UpdatedAt: time.Unix(2, 0).UTC()}
	if !store.ApplyRuntime(first) || !store.ApplyRuntime(second) {
		t.Fatal("runtime replacement was rejected")
	}
	first.UpdatedAt = time.Unix(9, 0).UTC()
	if store.ApplyRuntime(first) {
		t.Fatal("retired runtime operation was restored")
	}
}

func TestStateStoreRejectsRuntimeWithoutStartOperation(t *testing.T) {
	store, err := NewStateStore("session-1")
	if err != nil {
		t.Fatal(err)
	}
	empty := RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening, UpdatedAt: time.Unix(1, 0).UTC()}
	if store.ApplyRuntime(empty) {
		t.Fatal("runtime without start operation was accepted")
	}
	valid := RuntimeSnapshot{SessionID: "session-1", StartOperationID: "start-1", RuntimeState: RuntimeListening, UpdatedAt: time.Unix(2, 0).UTC()}
	if !store.ApplyRuntime(valid) {
		t.Fatal("valid runtime was rejected after empty snapshot")
	}
	empty.UpdatedAt = time.Unix(3, 0).UTC()
	if store.ApplyRuntime(empty) {
		t.Fatal("empty operation snapshot replaced valid runtime")
	}
}
