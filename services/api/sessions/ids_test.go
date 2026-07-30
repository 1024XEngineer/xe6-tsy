package sessions

import (
	"strings"
	"testing"
	"time"
)

func TestSystemClockReturnsUTC(t *testing.T) {
	now := SystemClock{}.Now()
	if now.Location() != time.UTC {
		t.Fatalf("Now() location = %v, want UTC", now.Location())
	}
	if now.IsZero() {
		t.Fatal("Now() returned zero time")
	}
}

func TestULIDGeneratorPrefixes(t *testing.T) {
	ids := NewULIDGenerator()
	sessionID := ids.NewVoiceSessionID()
	operationID := ids.NewStartOperationID()
	if !strings.HasPrefix(sessionID, "vs_") || len(sessionID) <= len("vs_") {
		t.Fatalf("NewVoiceSessionID() = %q", sessionID)
	}
	if !strings.HasPrefix(operationID, "op_") || len(operationID) <= len("op_") {
		t.Fatalf("NewStartOperationID() = %q", operationID)
	}
	if sessionID == operationID {
		t.Fatalf("IDs collided: %q", sessionID)
	}
}
