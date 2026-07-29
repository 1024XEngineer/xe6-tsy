package realtimev1

import (
	"encoding/json"
	"strings"
	"testing"
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
