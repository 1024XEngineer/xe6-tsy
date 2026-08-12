package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/speech"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
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
	modes := &fakeTurnModeReader{snapshot: validTurnModeSnapshot("session-1")}
	opener := NewTurnOpener(NewMemoryTurnAllocator(), reader, modes)

	turn, err := opener.OpenTurn(context.Background(), TurnOpenRequest{
		SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1",
		StartedAt: time.Unix(1700000000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("OpenTurn() error = %v", err)
	}
	if turn.ID == "" || turn.SequenceNo != 1 || reader.calls != 1 || modes.calls != 1 {
		t.Fatalf("turn = %#v, reader calls = %d", turn, reader.calls)
	}
	if turn.LanguageConfig.Version != 7 || turn.LanguageConfig.LanguagePairs[0].Target != "en-US" {
		t.Fatalf("language snapshot = %#v", turn.LanguageConfig)
	}
	if turn.Mode.RuntimeInstanceID != "runtime-1" || turn.Mode.Mode != realtimev1.ModeInterpretation || turn.Mode.Generation != 1 {
		t.Fatalf("mode snapshot = %#v", turn.Mode)
	}

	reader.snapshot.LanguagePairs[0].Target = "fr-FR"
	modes.snapshot.Mode = realtimev1.ModeAssistant
	modes.snapshot.Generation = 2
	if turn.LanguageConfig.LanguagePairs[0].Target != "en-US" {
		t.Fatalf("Turn language snapshot changed after reader mutation: %#v", turn.LanguageConfig)
	}
	if turn.Mode.Mode != realtimev1.ModeInterpretation || turn.Mode.Generation != 1 {
		t.Fatalf("Turn mode snapshot changed after reader mutation: %#v", turn.Mode)
	}
}

func TestTurnOpenerRejectsLanguageConfigWithoutMatchingSession(t *testing.T) {
	tests := []struct {
		name            string
		configSessionID string
	}{
		{name: "empty", configSessionID: ""},
		{name: "different", configSessionID: "session-2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
				SessionID: test.configSessionID,
				Version:   7,
				Status:    "active",
				LanguagePairs: []session.LanguagePair{
					{Source: "zh-CN", Target: "en-US"},
				},
			}}
			opener := NewTurnOpener(NewMemoryTurnAllocator(), reader, &fakeTurnModeReader{snapshot: validTurnModeSnapshot("session-1")})

			_, err := opener.OpenTurn(context.Background(), TurnOpenRequest{SessionID: "session-1"})
			if !errors.Is(err, ErrLanguageConfigSessionMismatch) {
				t.Fatalf("OpenTurn() error = %v, want ErrLanguageConfigSessionMismatch", err)
			}
		})
	}
}

func TestTurnOpenerRejectsInvalidLanguageConfig(t *testing.T) {
	tests := []struct {
		name    string
		version int64
		pairs   []session.LanguagePair
	}{
		{name: "zero version", pairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}},
		{name: "no pairs", version: 1},
		{name: "empty source", version: 1, pairs: []session.LanguagePair{{Target: "en-US"}}},
		{name: "empty target", version: 1, pairs: []session.LanguagePair{{Source: "zh-CN"}}},
		{name: "source has surrounding whitespace", version: 1, pairs: []session.LanguagePair{{Source: " zh-CN ", Target: "en-US"}}},
		{name: "target has surrounding whitespace", version: 1, pairs: []session.LanguagePair{{Source: "zh-CN", Target: " en-US "}}},
		{name: "same source and target", version: 1, pairs: []session.LanguagePair{{Source: "zh-CN", Target: "zh-CN"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opener := NewTurnOpener(NewMemoryTurnAllocator(), &fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
				SessionID: "session-1", Version: test.version, Status: "active", LanguagePairs: test.pairs,
			}}, &fakeTurnModeReader{snapshot: validTurnModeSnapshot("session-1")})

			_, err := opener.OpenTurn(context.Background(), TurnOpenRequest{SessionID: "session-1"})
			if !errors.Is(err, ErrLanguageConfigUnavailable) {
				t.Fatalf("OpenTurn() error = %v, want ErrLanguageConfigUnavailable", err)
			}
		})
	}
}

func TestTurnOpenerRejectsInvalidModeSnapshot(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*TurnModeSnapshot)
		wantErr error
	}{
		{name: "different session", mutate: func(snapshot *TurnModeSnapshot) { snapshot.SessionID = "session-2" }, wantErr: ErrTurnModeSessionMismatch},
		{name: "missing runtime", mutate: func(snapshot *TurnModeSnapshot) { snapshot.RuntimeInstanceID = "" }, wantErr: ErrTurnModeUnavailable},
		{name: "invalid mode", mutate: func(snapshot *TurnModeSnapshot) { snapshot.Mode = realtimev1.Mode("unknown") }, wantErr: ErrTurnModeUnavailable},
		{name: "zero generation", mutate: func(snapshot *TurnModeSnapshot) { snapshot.Generation = 0 }, wantErr: ErrTurnModeUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := validTurnModeSnapshot("session-1")
			test.mutate(&snapshot)
			opener := NewTurnOpener(NewMemoryTurnAllocator(), &fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
				SessionID: "session-1", Version: 1, Status: "active",
				LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
			}}, &fakeTurnModeReader{snapshot: snapshot})

			_, err := opener.OpenTurn(t.Context(), TurnOpenRequest{SessionID: "session-1"})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("OpenTurn() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestTurnOpenerPropagatesModeSnapshotReadFailure(t *testing.T) {
	wantErr := errors.New("runtime mode unavailable")
	opener := NewTurnOpener(NewMemoryTurnAllocator(), &fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
		SessionID: "session-1", Version: 1, Status: "active",
		LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
	}}, &fakeTurnModeReader{err: wantErr})

	_, err := opener.OpenTurn(t.Context(), TurnOpenRequest{SessionID: "session-1"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("OpenTurn() error = %v, want %v", err, wantErr)
	}
}

func TestTurnOpenerAcquiresExactSpeechBinding(t *testing.T) {
	binding := speech.TurnSpeechBinding{
		SessionID: "session-1", LanguageConfigVersion: 7,
		Route:      speech.SpeechRoute{LanguageA: "en-US", LanguageB: "zh-CN", ASRProfileID: "asr-7", TTSProfileID: "tts-7"},
		ASRProfile: speech.Profile{ID: "asr-7"}, TTSProfile: speech.Profile{ID: "tts-7"},
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}), TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}),
	}
	acquirer := &fakeTurnSpeechBindingAcquirer{binding: binding}
	opener := NewTurnOpenerWithBinding(
		NewMemoryTurnAllocator(),
		&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 7, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}, {Source: "en-US", Target: "zh-CN"}},
		}},
		&fakeTurnModeReader{snapshot: validTurnModeSnapshot("session-1")},
		acquirer,
	)

	turn, err := opener.OpenTurn(t.Context(), TurnOpenRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("OpenTurn() error = %v", err)
	}
	if turn.SpeechBinding.Route.ASRProfileID != "asr-7" || turn.SpeechBinding.Route.TTSProfileID != "tts-7" || acquirer.calls != 1 {
		t.Fatalf("speech binding = %#v, acquire calls = %d", turn.SpeechBinding, acquirer.calls)
	}
	turn.ReleaseSpeechBinding()
	turn.ReleaseSpeechBinding()
	if acquirer.releases != 1 {
		t.Fatalf("binding releases = %d, want 1", acquirer.releases)
	}
}

func TestTurnOpenerRejectsUnavailableSpeechBinding(t *testing.T) {
	wantErr := errors.New("binding still preparing")
	opener := NewTurnOpenerWithBinding(
		NewMemoryTurnAllocator(),
		&fakeLanguageConfigReader{snapshot: session.LanguageConfigSnapshot{
			SessionID: "session-1", Version: 7, Status: "active", LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		}},
		&fakeTurnModeReader{snapshot: validTurnModeSnapshot("session-1")},
		&fakeTurnSpeechBindingAcquirer{err: wantErr},
	)

	_, err := opener.OpenTurn(t.Context(), TurnOpenRequest{SessionID: "session-1"})
	if !errors.Is(err, ErrTurnSpeechBindingUnavailable) || !errors.Is(err, wantErr) {
		t.Fatalf("OpenTurn() error = %v, want wrapped binding error", err)
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

type fakeTurnModeReader struct {
	snapshot TurnModeSnapshot
	err      error
	calls    int
}

type fakeTurnSpeechBindingAcquirer struct {
	binding  speech.TurnSpeechBinding
	err      error
	calls    int
	releases int
}

func (f *fakeTurnSpeechBindingAcquirer) AcquireForTurn(_ context.Context, _ string, _ int64) (speech.TurnSpeechBinding, speech.Release, error) {
	f.calls++
	if f.err != nil {
		return speech.TurnSpeechBinding{}, nil, f.err
	}
	return f.binding, func() { f.releases++ }, nil
}

func (f *fakeTurnModeReader) GetTurnMode(_ context.Context, _ string) (TurnModeSnapshot, error) {
	f.calls++
	return f.snapshot, f.err
}

func validTurnModeSnapshot(sessionID string) TurnModeSnapshot {
	return TurnModeSnapshot{
		SessionID: sessionID, RuntimeInstanceID: "runtime-1",
		Mode: realtimev1.ModeInterpretation, Generation: 1,
	}
}

func newTestTurnOpener(languages session.LanguageConfigReader) *TurnOpener {
	return NewTurnOpener(NewMemoryTurnAllocator(), languages, &fakeTurnModeReader{snapshot: validTurnModeSnapshot("session-1")})
}

var _ TurnModeReader = (*fakeTurnModeReader)(nil)
var _ TurnSpeechBindingAcquirer = (*fakeTurnSpeechBindingAcquirer)(nil)
