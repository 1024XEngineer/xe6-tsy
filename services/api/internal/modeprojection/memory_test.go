package modeprojection

import (
	"errors"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestMemoryRepositoryProjectsModeChangesIdempotently(t *testing.T) {
	repository := NewMemoryRepository()
	first := modeEvent("event-1", "runtime-1", 1, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time.Unix(10, 0))
	if err := repository.Project(t.Context(), first); err != nil {
		t.Fatalf("first projection: %v", err)
	}
	if err := repository.Project(t.Context(), first); err != nil {
		t.Fatalf("replay projection: %v", err)
	}
	conflict := first
	conflict.TraceID = "different-trace"
	if err := repository.Project(t.Context(), conflict); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting replay error = %v, want conflict", err)
	}
	projection, err := repository.Latest(t.Context(), first.SessionID)
	if err != nil {
		t.Fatalf("Latest() = (%#v, %v)", projection, err)
	}
	if projection.LastEventID != first.EventID || projection.Generation != first.ResultingGeneration || projection.ActiveMode != first.ToMode {
		t.Fatalf("projection = %#v, want first event", projection)
	}
}

func TestMemoryRepositoryIgnoresOutOfOrderEventsWithinRuntime(t *testing.T) {
	repository := NewMemoryRepository()
	newer := modeEvent("event-2", "runtime-1", 2, realtimev1.ModeInterpretation, realtimev1.ModeAssistant, time.Unix(20, 0))
	older := modeEvent("event-1", "runtime-1", 1, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time.Unix(10, 0))
	if err := repository.Project(t.Context(), newer); err != nil {
		t.Fatalf("newer projection: %v", err)
	}
	if err := repository.Project(t.Context(), older); err != nil {
		t.Fatalf("older audit projection: %v", err)
	}
	projection, _ := repository.Latest(t.Context(), newer.SessionID)
	if projection.LastEventID != newer.EventID || projection.Generation != newer.ResultingGeneration {
		t.Fatalf("projection = %#v, want newer generation", projection)
	}
}

func TestMemoryRepositoryUsesOccurredAtAcrossRuntimes(t *testing.T) {
	repository := NewMemoryRepository()
	oldRuntime := modeEvent("event-old", "runtime-old", 4, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time.Unix(20, 0))
	delayedOldRuntime := modeEvent("event-delayed", "runtime-old", 3, realtimev1.ModeInterpretation, realtimev1.ModeAssistant, time.Unix(15, 0))
	newRuntime := modeEvent("event-new", "runtime-new", 1, realtimev1.ModeInterpretation, realtimev1.ModeAssistant, time.Unix(25, 0))
	for _, event := range []realtimev1.ModeChangedEvent{oldRuntime, newRuntime, delayedOldRuntime} {
		if err := repository.Project(t.Context(), event); err != nil {
			t.Fatalf("project %s: %v", event.EventID, err)
		}
	}
	projection, _ := repository.Latest(t.Context(), oldRuntime.SessionID)
	if projection.LastEventID != newRuntime.EventID || projection.RuntimeInstanceID != newRuntime.RuntimeInstanceID {
		t.Fatalf("projection = %#v, want newer runtime event", projection)
	}
}

func TestMemoryRepositoryDoesNotBreakCrossRuntimeTimestampTies(t *testing.T) {
	repository := NewMemoryRepository()
	first := modeEvent("event-z", "runtime-1", 2, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, time.Unix(20, 0))
	tied := modeEvent("event-a", "runtime-2", 1, realtimev1.ModeInterpretation, realtimev1.ModeAssistant, first.OccurredAt)
	for _, event := range []realtimev1.ModeChangedEvent{first, tied} {
		if err := repository.Project(t.Context(), event); err != nil {
			t.Fatalf("project %s: %v", event.EventID, err)
		}
	}
	projection, _ := repository.Latest(t.Context(), first.SessionID)
	if projection.LastEventID != first.EventID {
		t.Fatalf("projection = %#v, want first observed event on timestamp tie", projection)
	}
}

func TestMemoryRepositoryUsesEventIDToBreakCrossRuntimeTimestampTie(t *testing.T) {
	repository := NewMemoryRepository()
	occurredAt := time.Unix(30, 0).UTC()
	for _, event := range []realtimev1.ModeChangedEvent{
		modeEvent("event-b", "runtime-b", 1, realtimev1.ModeInterpretation, realtimev1.ModeAssistant, occurredAt),
		modeEvent("event-a", "runtime-a", 9, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, occurredAt),
		modeEvent("event-c", "runtime-c", 1, realtimev1.ModeAssistant, realtimev1.ModeInterpretation, occurredAt),
	} {
		if err := repository.Project(t.Context(), event); err != nil {
			t.Fatalf("Project(%s) error = %v", event.EventID, err)
		}
	}
	projection, err := repository.Latest(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if projection.LastEventID != "event-c" || projection.RuntimeInstanceID != "runtime-c" {
		t.Fatalf("projection = %#v, want stable event-c winner", projection)
	}
}

func TestMemoryRepositoryLatestRejectsMissingProjection(t *testing.T) {
	repository := NewMemoryRepository()
	if _, err := repository.Latest(t.Context(), ""); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("Latest(empty) error = %v, want invalid argument", err)
	}
	if _, err := repository.Latest(t.Context(), "session-missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Latest(missing) error = %v, want not found", err)
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
