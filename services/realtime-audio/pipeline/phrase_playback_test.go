package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestPhrasePlaybackSchedulerPreservesOrderAndUsesIndependentIDs(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	usage := &phraseUsageSink{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio, Usage: usage,
	})
	turn := TurnContext{ID: "turn-1", SessionID: "session-1", AccountID: "account-1", TraceID: "trace-1"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	for sequence := int64(1); sequence <= 3; sequence++ {
		if !scheduler.Enqueue(PhrasePlaybackRequest{
			Turn: turn, UtteranceID: turn.ID, PhraseSequence: sequence,
			Language: "en-US", Text: "phrase", PlaybackID: "phrase_turn-1_" + string(rune('0'+sequence)),
		}) {
			t.Fatalf("Enqueue(%d) rejected", sequence)
		}
	}
	if !audio.waitFor(3, time.Second) {
		t.Fatalf("timed out waiting for audio chunks: %#v", audio.ids())
	}
	got := provider.requests()
	if len(got) != 3 {
		t.Fatalf("TTS requests = %d, want 3", len(got))
	}
	for index, request := range got {
		want := "phrase_turn-1_" + string(rune('1'+index))
		if request.PlaybackID != want {
			t.Fatalf("request %d playback ID = %q, want %q", index, request.PlaybackID, want)
		}
	}
	deadline := time.Now().Add(time.Second)
	for len(usage.facts()) < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	facts := usage.facts()
	if len(facts) != 3 {
		t.Fatalf("usage facts = %d, want 3", len(facts))
	}
	if facts[0].ID == facts[1].ID || facts[1].ID == facts[2].ID || facts[0].IdempotencyKey == facts[1].IdempotencyKey {
		t.Fatalf("phrase usage identities are not unique: %#v", facts)
	}
}

func TestPhrasePlaybackSchedulerTargetsPlaybackOwner(t *testing.T) {
	audio := &recordingPhraseAudio{currentPlaybackID: "old-playback"}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{Audio: audio})
	if got := scheduler.CurrentPlaybackID(context.Background(), "session-1"); got != "old-playback" {
		t.Fatalf("CurrentPlaybackID() = %q, want old-playback", got)
	}
	if err := scheduler.InterruptPlayback(context.Background(), "session-1", "old-playback", 1, "mode_switch"); err != nil {
		t.Fatalf("InterruptPlayback() error = %v", err)
	}
	audio.mu.Lock()
	interruptedID := audio.interruptedID
	audio.mu.Unlock()
	if interruptedID != "old-playback" {
		t.Fatalf("audio interrupted ID = %q, want old-playback", interruptedID)
	}
}

func TestPhrasePlaybackSchedulerModeCleanupKeepsNewGenerationTasks(t *testing.T) {
	audio := &recordingPhraseAudio{currentPlaybackID: "old-playback"}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{Audio: audio})
	scheduler.mu.Lock()
	state := scheduler.sessionLocked("session-1")
	oldUtterance := &phrasePlaybackUtterance{unfinished: 1}
	newUtterance := &phrasePlaybackUtterance{unfinished: 1}
	state.queue = []*phrasePlaybackTask{
		{request: PhrasePlaybackRequest{Turn: TurnContext{SessionID: "session-1", Mode: TurnModeSnapshot{Generation: 1}}, UtteranceID: "old-turn", PlaybackID: "old-playback"}, generation: state.generation, utterance: oldUtterance},
		{request: PhrasePlaybackRequest{Turn: TurnContext{SessionID: "session-1", Mode: TurnModeSnapshot{Generation: 2}}, UtteranceID: "new-turn", PlaybackID: "new-playback"}, generation: state.generation, utterance: newUtterance},
	}
	state.utterances["old-turn"] = oldUtterance
	state.utterances["new-turn"] = newUtterance
	scheduler.mu.Unlock()

	if err := scheduler.InterruptPlayback(context.Background(), "session-1", "old-playback", 1, "mode_switch"); err != nil {
		t.Fatalf("InterruptPlayback() error = %v", err)
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if len(state.queue) != 1 || state.queue[0].request.UtteranceID != "new-turn" {
		t.Fatalf("queue after mode cleanup = %#v, want only new-turn", state.queue)
	}
	if _, superseded := state.superseded["old-turn"]; !superseded {
		t.Fatal("old-turn was not marked superseded")
	}
}

func TestPhrasePlaybackSchedulerRetriesUsageAndReportsPermanentFailure(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	usage := &phraseUsageSink{failures: 1}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio, Usage: usage,
	})
	turn := TurnContext{ID: "turn-retry", SessionID: "session-retry", AccountID: "account-1", TraceID: "trace-retry"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("phrase rejected")
	}
	deadline := time.Now().Add(time.Second)
	for usage.attemptsCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := usage.attemptsCount(); got != 2 {
		t.Fatalf("usage attempts = %d, want 2", got)
	}
	if len(usage.facts()) != 1 {
		t.Fatalf("published usage facts = %d, want 1", len(usage.facts()))
	}

	permanent := errors.New("outbox unavailable")
	reported := make(chan error, 1)
	usage = &phraseUsageSink{err: permanent}
	scheduler = NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio, Usage: usage,
		UsageError: func(_ PhrasePlaybackRequest, _ UsageFact, err error) { reported <- err },
	})
	turn = TurnContext{ID: "turn-error", SessionID: "session-error", AccountID: "account-1", TraceID: "trace-error"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("phrase with failing usage sink rejected")
	}
	select {
	case err := <-reported:
		if !errors.Is(err, permanent) {
			t.Fatalf("reported usage error = %v, want %v", err, permanent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for usage error callback")
	}
}

func TestPhrasePlaybackSchedulerCleansCompletedUtteranceAndAcceptsFinalResidual(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-cleanup", SessionID: "session-cleanup", AccountID: "account-1", TraceID: "trace-cleanup"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("phrase rejected")
	}
	if !audio.waitFor(1, time.Second) {
		t.Fatal("phrase did not play")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		scheduler.mu.Lock()
		_, exists := scheduler.sessions[turn.SessionID].utterances[turn.ID]
		scheduler.mu.Unlock()
		if !exists {
			break
		}
		time.Sleep(time.Millisecond)
	}
	scheduler.mu.Lock()
	_, exists := scheduler.sessions[turn.SessionID].utterances[turn.ID]
	scheduler.mu.Unlock()
	if exists {
		t.Fatal("completed utterance remains in session state")
	}
	final := phraseRequest(turn, 2)
	final.Final = true
	final.PlaybackID = "phrase_" + turn.ID + "_final"
	if !scheduler.Enqueue(final) {
		t.Fatal("final residual rejected after phrase state cleanup")
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatal("final residual did not play")
	}
}

func TestPhrasePlaybackSchedulerAcceptsLatePhraseAfterEarlierAudioDrains(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-late", SessionID: "session-late"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if result := scheduler.EnqueueWithReason(phraseRequest(turn, 1)); !result.Accepted {
		t.Fatalf("first phrase rejected: %#v", result)
	}
	if !audio.waitFor(1, time.Second) {
		t.Fatal("first phrase did not play")
	}
	waitForRetiredPhraseUtterance(t, scheduler, turn.SessionID, turn.ID)

	second := phraseRequest(turn, 2)
	second.Text = "late translated phrase"
	if result := scheduler.EnqueueWithReason(second); !result.Accepted {
		t.Fatalf("late phrase rejected after earlier audio drained: %#v", result)
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatal("late phrase did not play")
	}
	requests := provider.requests()
	if len(requests) != 2 || requests[1].Text != second.Text {
		t.Fatalf("TTS requests = %#v, want late phrase exactly once", requests)
	}
}

func TestPhrasePlaybackSchedulerDeduplicatesAcceptedPlaybackID(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-deduplicate", SessionID: "session-deduplicate"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	request := phraseRequest(turn, 1)
	if result := scheduler.EnqueueWithReason(request); !result.Accepted {
		t.Fatalf("first enqueue rejected: %#v", result)
	}
	if !audio.waitFor(1, time.Second) {
		t.Fatal("first phrase did not play")
	}
	waitForRetiredPhraseUtterance(t, scheduler, turn.SessionID, turn.ID)
	if result := scheduler.EnqueueWithReason(request); !result.Accepted {
		t.Fatalf("idempotent retry rejected: %#v", result)
	}
	time.Sleep(20 * time.Millisecond)
	if requests := provider.requests(); len(requests) != 1 {
		t.Fatalf("duplicate PlaybackID generated %d TTS requests, want 1", len(requests))
	}
}

func TestPhrasePlaybackSchedulerMergesStreamingBacklogWithoutDroppingActiveUtterance(t *testing.T) {
	provider := newPhraseBlockingTTSProvider()
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-1", SessionID: "session-1"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("first phrase rejected")
	}
	<-provider.started
	for sequence := int64(2); sequence <= 5; sequence++ {
		accepted := scheduler.Enqueue(phraseRequest(turn, sequence))
		if !accepted {
			t.Fatalf("phrase %d was rejected", sequence)
		}
	}
	if !scheduler.Enqueue(phraseRequest(turn, 6)) {
		t.Fatal("sixth phrase should be merged into the pending backlog")
	}
	provider.release()
	if !audio.waitFor(1, time.Second) {
		t.Fatal("active phrase did not finish")
	}

	newTurn := TurnContext{ID: "turn-2", SessionID: turn.SessionID}
	scheduler.ResetUtterance(newTurn.SessionID, newTurn.ID)
	provider.allowImmediate = true
	if !scheduler.Enqueue(phraseRequest(newTurn, 1)) {
		t.Fatal("next utterance did not recover playback eligibility")
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatal("next utterance did not play")
	}
}

func TestPhrasePlaybackSchedulerInterruptDropsLateQueue(t *testing.T) {
	provider := newPhraseBlockingTTSProvider()
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-1", SessionID: "session-1"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	scheduler.Enqueue(phraseRequest(turn, 1))
	<-provider.started
	scheduler.Enqueue(phraseRequest(turn, 2))
	if err := scheduler.InterruptCurrent(context.Background(), turn.SessionID, "wake_word_detected"); err != nil {
		t.Fatalf("InterruptCurrent() error = %v", err)
	}
	provider.release()
	time.Sleep(20 * time.Millisecond)
	if got := len(provider.requests()); got != 1 {
		t.Fatalf("late queued TTS requests = %d, want 1", got)
	}
}

func TestPhrasePlaybackSchedulerMergesQueuedStreamSegmentsAndKeepsActiveTask(t *testing.T) {
	provider := newPhraseBlockingTTSProvider()
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-stream-merge", SessionID: "session-stream-merge"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	first := phraseRequest(turn, 1001)
	first.PhraseGroup = 1
	first.Text = "first"
	if result := scheduler.EnqueueWithReason(first); !result.Accepted {
		t.Fatalf("first stream segment rejected: %#v", result)
	}
	<-provider.started
	for _, segment := range []struct {
		sequence int64
		text     string
	}{{1002, "second"}, {1003, "third"}, {1004, "fourth"}, {1005, "fifth"}, {1006, "sixth"}} {
		request := phraseRequest(turn, segment.sequence)
		request.PhraseGroup = 1
		request.Text = segment.text
		if result := scheduler.EnqueueWithReason(request); !result.Accepted {
			t.Fatalf("stream segment %d rejected: %#v", segment.sequence, result)
		}
	}
	provider.release()
	if !audio.waitFor(2, time.Second) {
		t.Fatal("merged stream task did not play")
	}
	requests := provider.requests()
	if len(requests) != 2 || requests[0].Text != "first" || requests[1].Text != "second third fourth fifth sixth" {
		t.Fatalf("TTS requests = %#v, want active plus one merged queued task", requests)
	}
}

func TestPhrasePlaybackSchedulerDoesNotMergeDifferentStreamPhraseGroups(t *testing.T) {
	turn := TurnContext{ID: "turn-stream-groups", SessionID: "session-stream-groups"}
	left := phraseRequest(turn, 1001)
	left.PhraseGroup = 1
	right := phraseRequest(turn, 2001)
	right.PhraseGroup = 2
	if shouldMergePhrasePlayback(left, right, false) {
		t.Fatal("stream chunks from different phrase groups must remain separate")
	}

	right.PhraseGroup = 1
	if !shouldMergePhrasePlayback(left, right, false) {
		t.Fatal("stream chunks from the same phrase group should remain mergeable")
	}
}

func TestPhrasePlaybackSchedulerDoesNotMergeStreamSegmentsPastTTSWindow(t *testing.T) {
	left := phraseRequest(TurnContext{ID: "turn-window", SessionID: "session-window"}, 1001)
	left.Text = strings.Repeat("a", 39)
	right := left
	right.PhraseSequence = 1002
	right.PlaybackID = "window-right"
	right.Text = "b"
	if shouldMergePhrasePlayback(left, right, false) {
		t.Fatal("stream segments over the 40-rune TTS window were merged")
	}
}

func TestJoinPhrasePlaybackTextPreservesJapaneseChunkBoundary(t *testing.T) {
	if got := joinPhrasePlaybackText("これはかな", "だけです"); got != "これはかなだけです" {
		t.Fatalf("joinPhrasePlaybackText() = %q", got)
	}
}

func TestPhrasePlaybackSchedulerReportsEnqueueReasons(t *testing.T) {
	provider := newPhraseBlockingTTSProvider()
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-reasons", SessionID: "session-reasons"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if result := scheduler.EnqueueWithReason(PhrasePlaybackRequest{}); result.Reason != PhrasePlaybackRejectInvalid {
		t.Fatalf("invalid request reason = %#v", result)
	}
	if result := scheduler.EnqueueWithReason(phraseRequest(turn, 1)); !result.Accepted {
		t.Fatalf("first phrase rejected: %#v", result)
	}
	<-provider.started
	for sequence := int64(2); sequence <= 5; sequence++ {
		if result := scheduler.EnqueueWithReason(phraseRequest(turn, sequence)); !result.Accepted {
			t.Fatalf("phrase %d rejected: %#v", sequence, result)
		}
	}
	if result := scheduler.EnqueueWithReason(phraseRequest(turn, 6)); !result.Accepted {
		t.Fatalf("backlog phrase should be merged: %#v", result)
	}
	if err := scheduler.Stop(context.Background(), turn.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if result := scheduler.EnqueueWithReason(phraseRequest(turn, 7)); result.Reason != PhrasePlaybackRejectPlaybackClosed {
		t.Fatalf("closed reason = %#v", result)
	}
}

func TestPhrasePlaybackSchedulerRetriesFailedTaskBeforeAdvancingQueue(t *testing.T) {
	provider := &flakyPhraseTTSProvider{failures: 1}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-retry-playback", SessionID: "session-retry-playback"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	first := phraseRequest(turn, 1)
	second := phraseRequest(turn, 2)
	if !scheduler.Enqueue(first) || !scheduler.Enqueue(second) {
		t.Fatal("failed to enqueue retry test tasks")
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatalf("audio chunks = %d, want retry then next task", len(audio.ids()))
	}
	requests := provider.requestsSnapshot()
	if len(requests) != 3 || requests[0].PlaybackID != first.PlaybackID ||
		requests[1].PlaybackID != first.PlaybackID+"_retry_2" || requests[2].PlaybackID != second.PlaybackID {
		t.Fatalf("TTS request order = %#v, want first, first retry, second", requests)
	}
}

func TestPhrasePlaybackSchedulerRetriesWithFreshPlaybackIDAfterFirstChunk(t *testing.T) {
	provider := &finishFailingAfterChunkTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-retry-after-chunk", SessionID: "session-retry-after-chunk"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	request := phraseRequest(turn, 1)
	if !scheduler.Enqueue(request) {
		t.Fatal("failed to enqueue retry test task")
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatalf("audio chunks = %#v, want failed attempt plus fresh retry", audio.ids())
	}
	ids := audio.ids()
	if ids[0] != request.PlaybackID || ids[1] != request.PlaybackID+"_retry_2" {
		t.Fatalf("audio playback IDs = %#v, want original then fresh retry ID", ids)
	}
	requests := provider.requestsSnapshot()
	if len(requests) != 2 || requests[0].PlaybackID != ids[0] || requests[1].PlaybackID != ids[1] {
		t.Fatalf("TTS requests = %#v, want lifecycle IDs %#v", requests, ids)
	}
}

func TestPhrasePlaybackSchedulerStopWithoutWorkerRejectsLateFinal(t *testing.T) {
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: &recordingTTSProvider{}, Audio: &recordingPhraseAudio{}, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: &recordingPhraseAudio{},
	})
	turn := TurnContext{ID: "turn-late-final", SessionID: "session-stopped-before-reset"}
	if err := scheduler.Stop(context.Background(), turn.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := scheduler.InterruptCurrent(context.Background(), turn.SessionID, "late_interrupt"); err != nil {
		t.Fatalf("InterruptCurrent() error = %v", err)
	}
	request := phraseRequest(turn, finalPhrasePlaybackSequence)
	request.Final = true
	if result := scheduler.EnqueueWithReason(request); result.Accepted || result.Reason != PhrasePlaybackRejectPlaybackClosed {
		t.Fatalf("late final result = %#v, want playback_closed", result)
	}
	scheduler.mu.Lock()
	_, workerCreated := scheduler.sessions[turn.SessionID]
	scheduler.mu.Unlock()
	if workerCreated {
		t.Fatal("late final created a worker for a stopped session")
	}
}

func TestPhrasePlaybackSchedulerInterruptRejectsLateFinalUntilNextReset(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-interrupted", SessionID: "session-interrupted"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if err := scheduler.InterruptCurrent(context.Background(), turn.SessionID, "wake_word_detected"); err != nil {
		t.Fatalf("InterruptCurrent() error = %v", err)
	}
	lateFinal := phraseRequest(turn, finalPhrasePlaybackSequence)
	lateFinal.Final = true
	if result := scheduler.EnqueueWithReason(lateFinal); result.Accepted || result.Reason != PhrasePlaybackRejectGenerationSuperseded {
		t.Fatalf("late final result = %#v, want generation_superseded", result)
	}

	nextTurn := TurnContext{ID: "turn-after-interrupt", SessionID: turn.SessionID}
	scheduler.ResetUtterance(nextTurn.SessionID, nextTurn.ID)
	request := phraseRequest(nextTurn, 1)
	if result := scheduler.EnqueueWithReason(request); !result.Accepted {
		t.Fatalf("next generation request rejected: %#v", result)
	}
	if !audio.waitFor(1, time.Second) {
		t.Fatal("next generation audio did not play")
	}
	if result := scheduler.EnqueueWithReason(lateFinal); result.Accepted || result.Reason != PhrasePlaybackRejectGenerationSuperseded {
		t.Fatalf("old final after reset result = %#v, want generation_superseded", result)
	}
	if err := scheduler.Stop(context.Background(), turn.SessionID); err != nil {
		t.Fatalf("cleanup Stop() error = %v", err)
	}
}

func TestPhrasePlaybackSchedulerBoundsStoppedSessionBarriers(t *testing.T) {
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{})
	for index := 0; index < maxPhrasePlaybackSessionBarriers+25; index++ {
		if err := scheduler.Stop(context.Background(), fmt.Sprintf("stopped-session-%d", index)); err != nil {
			t.Fatalf("Stop(%d) error = %v", index, err)
		}
	}
	scheduler.mu.Lock()
	barriers, order := len(scheduler.barriers), len(scheduler.barrierOrder)
	scheduler.mu.Unlock()
	if barriers > maxPhrasePlaybackSessionBarriers || order > maxPhrasePlaybackSessionBarriers {
		t.Fatalf("stopped session barriers = %d/%d, want both <= %d", barriers, order, maxPhrasePlaybackSessionBarriers)
	}
}

func TestPhrasePlaybackSchedulerStopWaitsForWorkerExit(t *testing.T) {
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{})
	turn := TurnContext{ID: "turn-worker-exit", SessionID: "session-worker-exit"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	scheduler.mu.Lock()
	done := scheduler.sessions[turn.SessionID].done
	scheduler.mu.Unlock()
	if err := scheduler.Stop(context.Background(), turn.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-done:
	default:
		t.Fatal("Stop() returned before the session worker exited")
	}
	scheduler.mu.Lock()
	_, exists := scheduler.sessions[turn.SessionID]
	scheduler.mu.Unlock()
	if exists {
		t.Fatal("stopped session worker remains registered")
	}
}

func TestPhrasePlaybackSchedulerResetDetachesSlowCanceledWorker(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: stubbornFinishTTSProvider{release: release}, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	oldTurn := TurnContext{ID: "turn-slow-stop", SessionID: "session-slow-stop"}
	scheduler.ResetUtterance(oldTurn.SessionID, oldTurn.ID)
	scheduler.mu.Lock()
	oldState := scheduler.sessions[oldTurn.SessionID]
	scheduler.mu.Unlock()
	if !scheduler.Enqueue(phraseRequest(oldTurn, 1)) || !audio.waitFor(1, time.Second) {
		t.Fatal("slow playback did not start")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := scheduler.Stop(stopCtx, oldTurn.SessionID)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want deadline exceeded", err)
	}
	nextTurn := TurnContext{ID: "turn-after-slow-stop", SessionID: oldTurn.SessionID}
	scheduler.ResetUtterance(nextTurn.SessionID, nextTurn.ID)
	scheduler.mu.Lock()
	newState := scheduler.sessions[nextTurn.SessionID]
	scheduler.mu.Unlock()
	if newState == nil || newState == oldState || newState.closed {
		t.Fatalf("ResetUtterance() did not create an independent worker: old=%p new=%p", oldState, newState)
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case <-oldState.done:
	case <-time.After(time.Second):
		t.Fatal("slow canceled worker did not exit after provider released")
	}
	scheduler.mu.Lock()
	registered := scheduler.sessions[nextTurn.SessionID]
	scheduler.mu.Unlock()
	if registered != newState {
		t.Fatal("old worker cleanup removed the replacement session")
	}
	if err := scheduler.Stop(context.Background(), nextTurn.SessionID); err != nil {
		t.Fatalf("cleanup Stop() error = %v", err)
	}
}

func TestPhrasePlaybackSchedulerBackpressuresGlobalQueueWithoutDropping(t *testing.T) {
	provider := newPhraseBlockingTTSProvider()
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-backpressure", SessionID: "session-backpressure"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("active phrase rejected")
	}
	<-provider.started
	for sequence := int64(2); sequence <= maxPhrasePlaybackQueue+1; sequence++ {
		request := phraseRequest(turn, sequence)
		request.PlaybackID = "backpressure_" + time.Unix(sequence, 0).Format("150405")
		if result := scheduler.EnqueueWithReason(request); !result.Accepted {
			t.Fatalf("queued phrase %d rejected: %#v", sequence, result)
		}
	}

	tailTurn := TurnContext{ID: "turn-backpressure-tail", SessionID: turn.SessionID}
	scheduler.ResetUtterance(tailTurn.SessionID, tailTurn.ID)
	resultCh := make(chan PhrasePlaybackEnqueueResult, 1)
	go func() {
		resultCh <- scheduler.EnqueueWithReason(phraseRequest(tailTurn, 1))
	}()
	select {
	case result := <-resultCh:
		t.Fatalf("enqueue returned before queue capacity was available: %#v", result)
	case <-time.After(30 * time.Millisecond):
	}
	provider.release()
	select {
	case result := <-resultCh:
		if !result.Accepted {
			t.Fatalf("backpressured phrase was dropped: %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("backpressured enqueue did not resume")
	}
}

func TestPhrasePlaybackSchedulerStopBroadcastsToBlockedEnqueues(t *testing.T) {
	provider := newPhraseBlockingTTSProvider()
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-stop-waiters", SessionID: "session-stop-waiters"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("active phrase rejected")
	}
	<-provider.started
	for sequence := int64(2); sequence <= maxPhrasePlaybackQueue+1; sequence++ {
		request := phraseRequest(turn, sequence)
		request.PlaybackID = "stop_waiter_" + time.Unix(sequence, 0).Format("150405")
		if !scheduler.Enqueue(request) {
			t.Fatalf("queued phrase %d rejected", sequence)
		}
	}

	results := make(chan PhrasePlaybackEnqueueResult, 2)
	for index := 0; index < 2; index++ {
		waiterTurn := TurnContext{ID: "turn-stop-waiter-" + string(rune('a'+index)), SessionID: turn.SessionID}
		scheduler.ResetUtterance(waiterTurn.SessionID, waiterTurn.ID)
		go func(waiterTurn TurnContext) {
			results <- scheduler.EnqueueWithReason(phraseRequest(waiterTurn, 1))
		}(waiterTurn)
	}
	time.Sleep(30 * time.Millisecond)
	if err := scheduler.Stop(context.Background(), turn.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	for index := 0; index < 2; index++ {
		select {
		case result := <-results:
			if result.Accepted || result.Reason != PhrasePlaybackRejectPlaybackClosed {
				t.Fatalf("blocked enqueue result = %#v, want playback_closed", result)
			}
		case <-time.After(time.Second):
			t.Fatal("Stop() did not wake every blocked enqueue")
		}
	}
}

func TestPhrasePlaybackSchedulerReopensSessionAfterStop(t *testing.T) {
	provider := &recordingTTSProvider{}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-reconnect", SessionID: "session-reconnect"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("first phrase rejected")
	}
	if !audio.waitFor(1, time.Second) {
		t.Fatal("first phrase did not play")
	}
	if err := scheduler.Stop(context.Background(), turn.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 2)) {
		t.Fatal("phrase after session restart rejected")
	}
	if !audio.waitFor(2, time.Second) {
		t.Fatal("phrase after session restart did not play")
	}
}

func TestPhrasePlaybackSchedulerPublishesFirstChunkBeforeTTSFinish(t *testing.T) {
	provider := &firstChunkTTSProvider{releaseFinish: make(chan struct{})}
	audio := &recordingPhraseAudio{}
	scheduler := NewPhrasePlaybackScheduler(PhrasePlaybackSchedulerDependencies{
		Speech: NewSpeechOutput(SpeechOutputDependencies{
			TTS: provider, Audio: audio, Runtime: phraseRuntimeReporter{}, Provider: "fake",
		}),
		Audio: audio,
	})
	turn := TurnContext{ID: "turn-first", SessionID: "session-first"}
	scheduler.ResetUtterance(turn.SessionID, turn.ID)
	if !scheduler.Enqueue(phraseRequest(turn, 1)) {
		t.Fatal("first phrase rejected")
	}
	if !audio.waitFor(1, time.Second) {
		t.Fatal("first audio chunk was not published before TTS finish")
	}
	close(provider.releaseFinish)
}

func phraseRequest(turn TurnContext, sequence int64) PhrasePlaybackRequest {
	return PhrasePlaybackRequest{
		Turn: turn, UtteranceID: turn.ID, PhraseSequence: sequence,
		Language: "en-US", Text: "translated", PlaybackID: "phrase_" + turn.ID + "_" + string(rune('0'+sequence)),
	}
}

func waitForRetiredPhraseUtterance(t *testing.T, scheduler *PhrasePlaybackSchedulerService, sessionID, utteranceID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		scheduler.mu.Lock()
		state := scheduler.sessions[sessionID]
		retired := state != nil && state.retired[utteranceID] != nil
		scheduler.mu.Unlock()
		if retired {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("utterance did not retire after playback drained")
}

type phraseRuntimeReporter struct{}

func (phraseRuntimeReporter) SetProcessingState(context.Context, session.ProcessingStateUpdate) error {
	return nil
}

type recordingPhraseAudio struct {
	mu                sync.Mutex
	idsV              []string
	currentPlaybackID string
	interruptedID     string
}

func (a *recordingPhraseAudio) Publish(_ context.Context, chunk AudioChunk) error {
	a.mu.Lock()
	a.idsV = append(a.idsV, chunk.PlaybackID)
	a.mu.Unlock()
	return nil
}
func (*recordingPhraseAudio) Complete(context.Context, string, string) error       { return nil }
func (*recordingPhraseAudio) Cancel(context.Context, string, string, string) error { return nil }
func (a *recordingPhraseAudio) CurrentPlaybackID(context.Context, string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentPlaybackID
}
func (a *recordingPhraseAudio) InterruptPlayback(_ context.Context, _, playbackID string, _ int64, _ string) error {
	a.mu.Lock()
	a.interruptedID = playbackID
	a.currentPlaybackID = ""
	a.mu.Unlock()
	return nil
}
func (a *recordingPhraseAudio) ids() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.idsV...)
}
func (a *recordingPhraseAudio) waitFor(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(a.ids()) >= count {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

type phraseUsageSink struct {
	mu       sync.Mutex
	factsV   []UsageFact
	failures int
	err      error
	attempts int
}

func (s *phraseUsageSink) Publish(_ context.Context, fact UsageFact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.failures > 0 {
		s.failures--
		return errors.New("temporary usage failure")
	}
	if s.err != nil {
		return s.err
	}
	s.factsV = append(s.factsV, fact)
	return nil
}

func (s *phraseUsageSink) facts() []UsageFact {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]UsageFact(nil), s.factsV...)
}

func (s *phraseUsageSink) attemptsCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

type recordingTTSProvider struct {
	mu   sync.Mutex
	seen []tts.Request
}

func (p *recordingTTSProvider) StartStream(ctx context.Context, request tts.Request) (tts.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.seen = append(p.seen, request)
	p.mu.Unlock()
	return &immediateTTSStream{}, nil
}
func (p *recordingTTSProvider) requests() []tts.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]tts.Request(nil), p.seen...)
}

type immediateTTSStream struct{}

func (*immediateTTSStream) Chunks() <-chan tts.AudioChunk {
	ch := make(chan tts.AudioChunk, 1)
	ch <- tts.AudioChunk{SequenceNo: 1, Data: []byte{1}}
	close(ch)
	return ch
}
func (*immediateTTSStream) Finish(context.Context) (tts.Result, error) {
	return tts.Result{Provider: "fake-provider", Model: "fake-model"}, nil
}
func (*immediateTTSStream) Close() error { return nil }

type phraseBlockingTTSProvider struct {
	mu             sync.Mutex
	seen           []tts.Request
	started        chan struct{}
	releaseCh      chan struct{}
	allowImmediate bool
}

func newPhraseBlockingTTSProvider() *phraseBlockingTTSProvider {
	return &phraseBlockingTTSProvider{started: make(chan struct{}, 1), releaseCh: make(chan struct{})}
}
func (p *phraseBlockingTTSProvider) StartStream(ctx context.Context, request tts.Request) (tts.Stream, error) {
	p.mu.Lock()
	p.seen = append(p.seen, request)
	immediate := p.allowImmediate
	p.mu.Unlock()
	if !immediate {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	return &phraseBlockingTTSStream{ctx: ctx, release: p.releaseCh, immediate: immediate}, nil
}
func (p *phraseBlockingTTSProvider) requests() []tts.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]tts.Request(nil), p.seen...)
}
func (p *phraseBlockingTTSProvider) release() {
	select {
	case <-p.releaseCh:
	default:
		close(p.releaseCh)
	}
}

type phraseBlockingTTSStream struct {
	ctx       context.Context
	release   <-chan struct{}
	immediate bool
}

func (s *phraseBlockingTTSStream) Chunks() <-chan tts.AudioChunk {
	ch := make(chan tts.AudioChunk, 1)
	go func() {
		defer close(ch)
		if !s.immediate {
			select {
			case <-s.release:
			case <-s.ctx.Done():
				return
			}
		}
		ch <- tts.AudioChunk{SequenceNo: 1, Data: []byte{1}}
	}()
	return ch
}
func (s *phraseBlockingTTSStream) Finish(ctx context.Context) (tts.Result, error) {
	return tts.Result{}, ctx.Err()
}
func (*phraseBlockingTTSStream) Close() error { return nil }

type firstChunkTTSProvider struct {
	releaseFinish chan struct{}
}

func (p *firstChunkTTSProvider) StartStream(context.Context, tts.Request) (tts.Stream, error) {
	return &firstChunkTTSStream{releaseFinish: p.releaseFinish}, nil
}

type firstChunkTTSStream struct {
	releaseFinish <-chan struct{}
}

func (*firstChunkTTSStream) Chunks() <-chan tts.AudioChunk {
	ch := make(chan tts.AudioChunk, 1)
	ch <- tts.AudioChunk{SequenceNo: 1, Data: []byte{1}}
	close(ch)
	return ch
}

func (s *firstChunkTTSStream) Finish(ctx context.Context) (tts.Result, error) {
	select {
	case <-s.releaseFinish:
		return tts.Result{}, nil
	case <-ctx.Done():
		return tts.Result{}, ctx.Err()
	}
}

func (*firstChunkTTSStream) Close() error { return nil }

type stubbornFinishTTSProvider struct {
	release <-chan struct{}
}

type flakyPhraseTTSProvider struct {
	mu       sync.Mutex
	failures int
	requests []tts.Request
}

type finishFailingAfterChunkTTSProvider struct {
	mu       sync.Mutex
	requests []tts.Request
}

func (p *finishFailingAfterChunkTTSProvider) StartStream(_ context.Context, request tts.Request) (tts.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	return &finishFailingAfterChunkTTSStream{failFinish: len(p.requests) == 1}, nil
}

func (p *finishFailingAfterChunkTTSProvider) requestsSnapshot() []tts.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]tts.Request(nil), p.requests...)
}

type finishFailingAfterChunkTTSStream struct {
	failFinish bool
}

func (*finishFailingAfterChunkTTSStream) Chunks() <-chan tts.AudioChunk {
	chunks := make(chan tts.AudioChunk, 1)
	chunks <- tts.AudioChunk{SequenceNo: 1, Data: []byte{1}}
	close(chunks)
	return chunks
}

func (s *finishFailingAfterChunkTTSStream) Finish(context.Context) (tts.Result, error) {
	if s.failFinish {
		return tts.Result{}, errors.New("temporary finish failure")
	}
	return tts.Result{Provider: "fake-provider", Model: "fake-model"}, nil
}

func (*finishFailingAfterChunkTTSStream) Close() error { return nil }

func (p *flakyPhraseTTSProvider) StartStream(ctx context.Context, request tts.Request) (tts.Stream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	fail := p.failures > 0
	if fail {
		p.failures--
	}
	p.mu.Unlock()
	if fail {
		return nil, errors.New("temporary TTS failure")
	}
	return (&recordingTTSProvider{}).StartStream(ctx, request)
}

func (p *flakyPhraseTTSProvider) requestsSnapshot() []tts.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]tts.Request(nil), p.requests...)
}

func (p stubbornFinishTTSProvider) StartStream(context.Context, tts.Request) (tts.Stream, error) {
	return &stubbornFinishTTSStream{release: p.release}, nil
}

type stubbornFinishTTSStream struct {
	release <-chan struct{}
}

func (*stubbornFinishTTSStream) Chunks() <-chan tts.AudioChunk {
	chunks := make(chan tts.AudioChunk, 1)
	chunks <- tts.AudioChunk{SequenceNo: 1, Data: []byte{1}}
	close(chunks)
	return chunks
}

func (s *stubbornFinishTTSStream) Finish(context.Context) (tts.Result, error) {
	<-s.release
	return tts.Result{}, nil
}

func (*stubbornFinishTTSStream) Close() error { return nil }
