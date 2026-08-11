package modeprojection

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestMemoryRepositoryProjectsModeChangesIdempotently(t *testing.T) {
	repository := NewMemoryRepository()
	first := modeEvent("event-1", "runtime-1", 1, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time.Unix(10, 0))
	payload := []byte(`{"event_id":"event-1"}`)
	hash := sha256.Sum256(payload)
	if err := repository.ProjectModeChanged(t.Context(), first, hash); err != nil {
		t.Fatalf("first projection: %v", err)
	}
	if err := repository.ProjectModeChanged(t.Context(), first, hash); err != nil {
		t.Fatalf("replay projection: %v", err)
	}
	if err := repository.ProjectModeChanged(t.Context(), first, sha256.Sum256([]byte("different"))); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting replay error = %v, want conflict", err)
	}
	projection, ok, err := repository.Projection(t.Context(), first.SessionID)
	if err != nil || !ok {
		t.Fatalf("Projection() = (%#v, %v, %v)", projection, ok, err)
	}
	if projection.LastEventID != first.EventID || projection.Generation != first.ResultingGeneration || projection.ActiveMode != first.ToMode {
		t.Fatalf("projection = %#v, want first event", projection)
	}
}

func TestMemoryRepositoryIgnoresOutOfOrderEventsWithinRuntime(t *testing.T) {
	repository := NewMemoryRepository()
	newer := modeEvent("event-2", "runtime-1", 2, realtimev1.ModeInterpretation, realtimev1.ModeAssistant, time.Unix(20, 0))
	older := modeEvent("event-1", "runtime-1", 1, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time.Unix(10, 0))
	if err := repository.ProjectModeChanged(t.Context(), newer, sha256.Sum256([]byte("newer"))); err != nil {
		t.Fatalf("newer projection: %v", err)
	}
	if err := repository.ProjectModeChanged(t.Context(), older, sha256.Sum256([]byte("older"))); err != nil {
		t.Fatalf("older audit projection: %v", err)
	}
	projection, _, _ := repository.Projection(t.Context(), newer.SessionID)
	if projection.LastEventID != newer.EventID || projection.Generation != newer.ResultingGeneration {
		t.Fatalf("projection = %#v, want newer generation", projection)
	}
}

func TestMemoryRepositoryUsesOccurredAtAcrossRuntimes(t *testing.T) {
	repository := NewMemoryRepository()
	oldRuntime := modeEvent("event-old", "runtime-old", 4, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time.Unix(20, 0))
	lateOldRuntime := modeEvent("event-late", "runtime-old", 3, realtimev1.ModeInterpretation, realtimev1.ModeAssistant, time.Unix(30, 0))
	newRuntime := modeEvent("event-new", "runtime-new", 1, realtimev1.ModeInterpretation, realtimev1.ModeAssistant, time.Unix(25, 0))
	for _, event := range []realtimev1.ModeChangedEvent{oldRuntime, newRuntime, lateOldRuntime} {
		if err := repository.ProjectModeChanged(t.Context(), event, sha256.Sum256([]byte(event.EventID))); err != nil {
			t.Fatalf("project %s: %v", event.EventID, err)
		}
	}
	projection, _, _ := repository.Projection(t.Context(), oldRuntime.SessionID)
	if projection.LastEventID != lateOldRuntime.EventID || projection.RuntimeInstanceID != oldRuntime.RuntimeInstanceID {
		t.Fatalf("projection = %#v, want later event from old runtime", projection)
	}
}

func TestParseModeChangedEventValidatesBeforeHashing(t *testing.T) {
	if _, _, err := ParseModeChangedEvent([]byte(`{"event_version":2}`)); !errors.Is(err, domain.ErrInvalidArgument) && !errors.Is(err, realtimev1.ErrInvalidModeChangedEvent) {
		t.Fatalf("ParseModeChangedEvent() error = %v, want contract validation error", err)
	}
}

func modeEvent(eventID, runtimeID string, generation int64, from, to realtimev1.Mode, occurredAt time.Time) realtimev1.ModeChangedEvent {
	return realtimev1.ModeChangedEvent{
		EventVersion: realtimev1.ModeChangedEventVersion,
		EventID:      eventID, TraceID: "trace-" + eventID, SessionID: "session-1",
		RuntimeInstanceID: runtimeID, OperationID: "operation-" + eventID,
		FromMode: from, ToMode: to, ResultingGeneration: generation, OccurredAt: occurredAt,
	}
}
