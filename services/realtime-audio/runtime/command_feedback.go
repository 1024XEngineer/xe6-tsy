package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

type commandSpeechFeedbackDependencies struct {
	Speech    *pipeline.SpeechOutput
	Usage     pipeline.UsageFactSink
	Runtime   session.RuntimeStateReporter
	AccountID string
	TraceID   string
	Logger    *slog.Logger
	Now       func() time.Time
}

// commandSpeechFeedback isolates confirmation TTS from command execution. The typed result is
// already final before this worker starts, so provider or delivery failures can never replay an
// interpreter, language update, or mode transition.
type commandSpeechFeedback struct {
	mu      sync.Mutex
	deps    commandSpeechFeedbackDependencies
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closed  bool
	attempt uint64
}

func newCommandSpeechFeedback(deps commandSpeechFeedbackDependencies) *commandSpeechFeedback {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	return &commandSpeechFeedback{deps: deps}
}

func (f *commandSpeechFeedback) Publish(event realtimev1.CommandResultEvent) {
	if f == nil || event.Validate() != nil || event.Message == "" {
		return
	}
	f.mu.Lock()
	if f.closed || f.deps.Speech == nil {
		f.mu.Unlock()
		return
	}
	if f.cancel != nil {
		f.cancel()
	}
	f.attempt++
	attempt := f.attempt
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.wg.Add(1)
	f.mu.Unlock()
	go f.play(ctx, attempt, event)
}

func (f *commandSpeechFeedback) Interrupt() {
	if f == nil {
		return
	}
	f.mu.Lock()
	if f.cancel != nil {
		f.cancel()
	}
	f.attempt++
	f.mu.Unlock()
}

func (f *commandSpeechFeedback) Close() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.closed = true
	if f.cancel != nil {
		f.cancel()
	}
	f.mu.Unlock()
	f.wg.Wait()
}

func (f *commandSpeechFeedback) play(ctx context.Context, attempt uint64, event realtimev1.CommandResultEvent) {
	defer f.wg.Done()
	turn := pipeline.TurnContext{
		ID: "command_" + event.CommandID, SessionID: event.SessionID,
		AccountID: f.deps.AccountID, TraceID: f.deps.TraceID, StartedAt: event.OccurredAt,
	}
	result, err := f.deps.Speech.Play(ctx, pipeline.SpeechOutputRequest{
		Turn: turn, Language: "zh-CN", Text: event.Message,
		PlaybackID: "command_playback_" + event.CommandID,
	})
	if err != nil {
		f.logFailure(event, "tts", err)
		f.restoreListeningIfCurrent(attempt, event.SessionID)
		return
	}
	if f.deps.Usage != nil {
		fact, factErr := pipeline.BuildUsageFact(turn, "tts", result.Provider, result.Model,
			result.AudioDuration.Milliseconds(), 0, 0, result.CostAmount, result.Currency, f.deps.Now())
		if factErr != nil {
			f.logFailure(event, "usage_build", factErr)
		} else if publishErr := f.deps.Usage.Publish(context.WithoutCancel(ctx), fact); publishErr != nil {
			f.logFailure(event, "usage_publish", publishErr)
		}
	}
	f.restoreListeningIfCurrent(attempt, event.SessionID)
}

func (f *commandSpeechFeedback) restoreListeningIfCurrent(attempt uint64, sessionID string) {
	f.mu.Lock()
	current := !f.closed && f.attempt == attempt
	f.mu.Unlock()
	if !current {
		return
	}
	if f.deps.Runtime == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.deps.Runtime.SetProcessingState(ctx, session.ProcessingStateUpdate{
		SessionID: sessionID, RuntimeState: session.RuntimeListening,
	}); err != nil {
		f.deps.Logger.Warn("command feedback failed", "stage", "restore_listening", "error", err)
	}
}

func (f *commandSpeechFeedback) logFailure(event realtimev1.CommandResultEvent, stage string, err error) {
	f.deps.Logger.Warn("command feedback failed",
		"stage", stage, "command_status", event.Status, "error", fmt.Errorf("command feedback: %w", err))
}

var _ command.FeedbackSink = (*commandSpeechFeedback)(nil)
