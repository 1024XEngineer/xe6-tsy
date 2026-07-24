package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

func TestMemoryTurnAllocatorSequentialPerSession(t *testing.T) {
	allocator := NewMemoryTurnAllocator()

	firstID, firstSequence, err := allocator.Next(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	secondID, secondSequence, err := allocator.Next(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	otherID, otherSequence, err := allocator.Next(context.Background(), "session-2")
	if err != nil {
		t.Fatalf("other Next() error = %v", err)
	}

	if firstSequence != 1 || secondSequence != 2 || otherSequence != 1 {
		t.Fatalf("sequences = %d, %d, %d; want 1, 2, 1", firstSequence, secondSequence, otherSequence)
	}
	if firstID == "" || secondID == "" || otherID == "" || firstID == secondID || firstID == otherID {
		t.Fatalf("turn IDs = %q, %q, %q; want unique non-empty IDs", firstID, secondID, otherID)
	}
}

func TestMemoryTurnAllocatorValidatesInput(t *testing.T) {
	allocator := NewMemoryTurnAllocator()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := allocator.Next(context.Background(), ""); !errors.Is(err, ErrSessionIDRequired) {
		t.Fatalf("empty session error = %v, want ErrSessionIDRequired", err)
	}
	if _, _, err := allocator.Next(canceled, "session-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error = %v, want context.Canceled", err)
	}
}

func TestMemoryTurnAllocatorConcurrent(t *testing.T) {
	allocator := NewMemoryTurnAllocator()
	const workers = 32
	sequences := make(chan int64, workers)
	var waitGroup sync.WaitGroup
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, sequence, err := allocator.Next(context.Background(), "session-1")
			if err != nil {
				t.Errorf("Next() error = %v", err)
				return
			}
			sequences <- sequence
		}()
	}
	waitGroup.Wait()
	close(sequences)

	seen := make(map[int64]bool, workers)
	for sequence := range sequences {
		if seen[sequence] {
			t.Fatalf("duplicate sequence %d", sequence)
		}
		seen[sequence] = true
	}
	for sequence := int64(1); sequence <= workers; sequence++ {
		if !seen[sequence] {
			t.Fatalf("missing sequence %d", sequence)
		}
	}
}

func TestTurnOpenerSnapshotsLanguageConfig(t *testing.T) {
	reader := &fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
		SessionID: "session-1",
		Version:   7,
		Status:    "active",
		LanguagePairs: []session.LanguagePair{
			{Source: "zh-CN", Target: "en-US"},
			{Source: "en-US", Target: "zh-CN"},
		},
	}}
	opener := NewTurnOpener(NewMemoryTurnAllocator(), reader)

	turn, err := opener.OpenTurn(context.Background(), TurnOpenRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1",
		StartedAt: time.Unix(1700000000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("OpenTurn() error = %v", err)
	}
	if turn.ID == "" || turn.SequenceNo != 1 || reader.calls != 1 {
		t.Fatalf("turn = %#v, reader calls = %d", turn, reader.calls)
	}
	if turn.LanguageConfig.Version != 7 || turn.LanguageConfig.LanguagePairs[0].Target != "en-US" {
		t.Fatalf("language snapshot = %#v", turn.LanguageConfig)
	}

	reader.snapshot.LanguagePairs[0].Target = "fr-FR"
	if turn.LanguageConfig.LanguagePairs[0].Target != "en-US" {
		t.Fatalf("Turn language snapshot changed after reader mutation: %#v", turn.LanguageConfig)
	}
}

type fakeLanguageConfigReader struct {
	snapshot session.LanguageConfigSnapshot
	calls    int
}

func (f *fakeLanguageConfigReader) GetCurrentConfig(_ context.Context, _ string) (session.LanguageConfigSnapshot, error) {
	f.calls++
	return f.snapshot, nil
}

var _ session.LanguageConfigReader = (*fakeLanguageConfigReader)(nil)
