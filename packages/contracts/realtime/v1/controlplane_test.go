package realtimev1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStartRequestCarriesDurableOperationID(t *testing.T) {
	encoded, err := json.Marshal(StartRequest{
		OperationID: "operation-1",
		TraceID:     "trace-1",
		StartedBy:   "account-1",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, field := range []string{
		`"operation_id":"operation-1"`,
		`"trace_id":"trace-1"`,
		`"started_by":"account-1"`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("StartRequest JSON = %s, missing %s", encoded, field)
		}
	}
}

func TestStopRequestCarriesLifecycleMetadata(t *testing.T) {
	endedAt := time.Date(2026, time.July, 29, 19, 0, 0, 0, time.UTC)
	encoded, err := json.Marshal(StopRequest{
		TraceID: "trace-1", Reason: "user_requested", EndedAt: endedAt,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, field := range []string{
		`"trace_id":"trace-1"`,
		`"reason":"user_requested"`,
		`"ended_at":"2026-07-29T19:00:00Z"`,
	} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("StopRequest JSON = %s, missing %s", encoded, field)
		}
	}
}

func TestLifecycleErrorCodesAreDistinct(t *testing.T) {
	if ErrorRuntimeNotFound == ErrorRuntimeOperationConflict ||
		ErrorRuntimeNotFound == ErrorConnectionNotFound ||
		ErrorRuntimeOperationConflict == ErrorConnectionNotFound {
		t.Fatal("lifecycle error codes must remain distinct")
	}
}
