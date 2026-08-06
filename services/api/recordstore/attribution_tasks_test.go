package recordstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func TestRetryBackoffGrowsAndCaps(t *testing.T) {
	tests := []struct {
		name     string
		attempts int
		want     time.Duration
	}{
		{name: "first attempt", attempts: 1, want: attributionTaskBackoff},
		{name: "second attempt", attempts: 2, want: 2 * attributionTaskBackoff},
		{name: "third attempt", attempts: 3, want: 4 * attributionTaskBackoff},
		{name: "capped", attempts: 100, want: attributionTaskMaxBackoff},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := retryBackoff(test.attempts); got != test.want {
				t.Fatalf("retryBackoff(%d) = %v, want %v", test.attempts, got, test.want)
			}
		})
	}
}

func TestAttributionTaskIDPrefixesTurnID(t *testing.T) {
	if got := attributionTaskID("turn_01"); got != "attr_turn_01" {
		t.Fatalf("attributionTaskID() = %q, want attr_turn_01", got)
	}
}

func TestAttributionTaskStoreRequiresPool(t *testing.T) {
	if err := NewAttributionTaskStore(nil).Enqueue(context.Background(), nil, "turn_01", "session_01"); !errors.Is(err, ErrAttributionTaskRequired) {
		t.Fatalf("Enqueue() error = %v, want ErrAttributionTaskRequired", err)
	}
	if _, err := NewAttributionTaskStore(nil).Receive(context.Background()); !errors.Is(err, ErrAttributionTaskRequired) {
		t.Fatalf("Receive() error = %v, want ErrAttributionTaskRequired", err)
	}
}

func TestAttributionTaskDeliveryTaskReturnsOriginal(t *testing.T) {
	want := turns.AttributionTask{
		TaskID: "attr_turn_01", TurnID: "turn_01", SessionID: "session_01",
		AccountID: "acct_01", TaskType: "turn_attribution", Attempts: 2,
	}
	delivery := &attributionTaskDelivery{task: want}
	if got := delivery.Task(); got != want {
		t.Fatalf("Task() = %#v, want %#v", got, want)
	}
}
