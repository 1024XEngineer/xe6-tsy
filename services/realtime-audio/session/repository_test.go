package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestMemoryRuntimeRepositoryMissing(t *testing.T) {
	repository := NewMemoryRuntimeRepository()

	_, err := repository.Get(context.Background(), "session-1")
	if !errors.Is(err, ErrRuntimeNotFound) {
		t.Fatalf("Get() error = %v, want ErrRuntimeNotFound", err)
	}
}

func TestMemoryRuntimeRepositorySaveAndGet(t *testing.T) {
	repository := NewMemoryRuntimeRepository()
	want := RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening}

	if err := repository.Save(context.Background(), want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := repository.Get(context.Background(), want.SessionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.SessionID != want.SessionID || got.RuntimeState != want.RuntimeState {
		t.Fatalf("Get() = %#v, want %#v", got, want)
	}
}

func TestMemoryRuntimeRepositoryIsolatesPointers(t *testing.T) {
	repository := NewMemoryRuntimeRepository()
	turnID := "turn-1"
	playbackID := "playback-1"
	errorCode := "error-1"
	snapshot := RuntimeSnapshot{
		SessionID:         "session-1",
		RuntimeState:      RuntimePlaying,
		CurrentTurnID:     &turnID,
		CurrentPlaybackID: &playbackID,
		LastErrorCode:     &errorCode,
	}

	if err := repository.Save(context.Background(), snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	turnID = "mutated-input"
	got, err := repository.Get(context.Background(), snapshot.SessionID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	*got.CurrentTurnID = "mutated-output"

	again, err := repository.Get(context.Background(), snapshot.SessionID)
	if err != nil {
		t.Fatalf("second Get() error = %v", err)
	}
	if *again.CurrentTurnID != "turn-1" {
		t.Fatalf("CurrentTurnID = %q, want turn-1", *again.CurrentTurnID)
	}
}

func TestMemoryRuntimeRepositoryValidatesInput(t *testing.T) {
	repository := NewMemoryRuntimeRepository()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		run  func() error
		want error
	}{
		{name: "get empty session", run: func() error { _, err := repository.Get(context.Background(), ""); return err }, want: ErrSessionIDRequired},
		{name: "save empty session", run: func() error { return repository.Save(context.Background(), RuntimeSnapshot{}) }, want: ErrSessionIDRequired},
		{name: "get canceled", run: func() error { _, err := repository.Get(canceled, "session-1"); return err }, want: context.Canceled},
		{name: "save canceled", run: func() error { return repository.Save(canceled, RuntimeSnapshot{SessionID: "session-1"}) }, want: context.Canceled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMemoryRuntimeRepositoryConcurrentAccess(t *testing.T) {
	repository := NewMemoryRuntimeRepository()
	if err := repository.Save(context.Background(), RuntimeSnapshot{SessionID: "session-1"}); err != nil {
		t.Fatalf("initial Save() error = %v", err)
	}

	var workers sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for iteration := 0; iteration < 50; iteration++ {
				snapshot := RuntimeSnapshot{
					SessionID:    "session-1",
					RuntimeState: RuntimeState(fmt.Sprintf("worker-%d", worker)),
				}
				if err := repository.Save(context.Background(), snapshot); err != nil {
					t.Errorf("Save() error = %v", err)
					return
				}
				if _, err := repository.Get(context.Background(), snapshot.SessionID); err != nil {
					t.Errorf("Get() error = %v", err)
					return
				}
			}
		}(worker)
	}
	waitForWorkers(t, &workers)
}

func waitForWorkers(t *testing.T, workers *sync.WaitGroup) {
	t.Helper()
	workers.Wait()
}
