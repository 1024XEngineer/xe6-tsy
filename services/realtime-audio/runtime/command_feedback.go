package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

const (
	commandFeedbackLLMTimeout  = time.Second
	commandUsagePublishTimeout = time.Second
)

type commandSpeechFeedbackDependencies struct {
	Speech                 *pipeline.SpeechOutput
	Usage                  pipeline.UsageFactSink
	Runtime                session.RuntimeStateReporter
	SuccessFeedback        command.SuccessFeedbackGenerator
	SuccessFeedbackTimeout time.Duration
	AccountID              string
	TraceID                string
	Logger                 *slog.Logger
	Now                    func() time.Time
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
	if deps.SuccessFeedbackTimeout <= 0 {
		deps.SuccessFeedbackTimeout = commandFeedbackLLMTimeout
	}
	return &commandSpeechFeedback{deps: deps}
}

func (f *commandSpeechFeedback) Publish(request command.FeedbackRequest) {
	if request.Event.Validate() != nil || request.Event.Message == "" {
		return
	}
	f.mu.Lock()
	if f.closed {
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
	go f.play(ctx, attempt, request)
}

func (f *commandSpeechFeedback) Interrupt() {
	f.mu.Lock()
	if f.cancel != nil {
		f.cancel()
	}
	f.attempt++
	f.mu.Unlock()
}

func (f *commandSpeechFeedback) Close() {
	f.mu.Lock()
	f.closed = true
	if f.cancel != nil {
		f.cancel()
	}
	f.mu.Unlock()
	f.wg.Wait()
}

func (f *commandSpeechFeedback) play(ctx context.Context, attempt uint64, request command.FeedbackRequest) {
	defer f.wg.Done()
	event := request.Event
	message := event.Message
	if request.Success != nil && f.deps.SuccessFeedback != nil {
		feedbackCtx, cancel := context.WithTimeout(ctx, f.deps.SuccessFeedbackTimeout)
		generated, err := f.deps.SuccessFeedback.GenerateSuccessFeedback(feedbackCtx, *request.Success)
		cancel()
		// Usage publication is deferred so a slow durable sink cannot delay speech. WithoutCancel in
		// publishFeedbackUsage preserves billable work even when a newer wake interrupts playback.
		defer func() {
			if usageErr := f.publishFeedbackUsage(ctx, event, generated); usageErr != nil {
				f.logFailure(event, "feedback_usage", usageErr)
			}
		}()
		if ctx.Err() != nil {
			return
		}
		generated.Message = strings.TrimSpace(generated.Message)
		if err != nil || !command.ValidSuccessFeedback(generated.Message) {
			if err == nil {
				err = fmt.Errorf("generated command feedback is invalid")
			}
			f.logFailure(event, "feedback_generation", err)
		} else {
			message = generated.Message
		}
	}
	if err := ctx.Err(); err != nil {
		return
	}
	turn := pipeline.TurnContext{
		ID: "command_" + event.CommandID, SessionID: event.SessionID,
		AccountID: f.deps.AccountID, TraceID: f.deps.TraceID, StartedAt: event.OccurredAt,
	}
	playbackID := "command_playback_" + event.CommandID
	result, err := f.deps.Speech.Play(ctx, pipeline.SpeechOutputRequest{
		Turn: turn, Language: feedbackLanguage(request), Text: message,
		PlaybackID: playbackID,
	})
	if err != nil {
		f.logFailure(event, "tts", err)
		f.restoreListeningIfCurrent(attempt, event, turn.ID, playbackID)
		return
	}
	fact, factErr := pipeline.BuildUsageFact(turn, "tts", result.Provider, result.Model,
		result.AudioDuration.Milliseconds(), 0, 0, result.CostAmount, result.Currency, f.deps.Now())
	if factErr != nil {
		f.logFailure(event, "usage_build", factErr)
	} else {
		// A completed synthesis remains billable after a newer wake cancels playback feedback,
		// but a stalled sink must not keep Runtime shutdown waiting indefinitely.
		usageCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commandUsagePublishTimeout)
		publishErr := f.deps.Usage.Publish(usageCtx, fact)
		cancel()
		if publishErr != nil {
			f.logFailure(event, "usage_publish", publishErr)
		}
	}
	f.restoreListeningIfCurrent(attempt, event, turn.ID, playbackID)
}

func (f *commandSpeechFeedback) publishFeedbackUsage(
	ctx context.Context,
	event realtimev1.CommandResultEvent,
	result command.SuccessFeedbackResult,
) error {
	if result.Provider == "" || result.Model == "" || (result.InputTokens == 0 && result.OutputTokens == 0) {
		return nil
	}
	turn := pipeline.TurnContext{
		ID: "command_feedback_" + event.CommandID, SessionID: event.SessionID,
		AccountID: f.deps.AccountID, TraceID: f.deps.TraceID, StartedAt: event.OccurredAt,
	}
	fact, err := pipeline.BuildUsageFact(turn, "assistant_llm", result.Provider, result.Model, 0,
		result.InputTokens, result.OutputTokens, result.CostAmount, result.Currency, f.deps.Now())
	if err != nil {
		return err
	}
	usageCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commandUsagePublishTimeout)
	defer cancel()
	return f.deps.Usage.Publish(usageCtx, fact)
}

func feedbackLanguage(request command.FeedbackRequest) string {
	if request.Success != nil && strings.TrimSpace(request.Success.ResponseLanguage) != "" {
		return request.Success.ResponseLanguage
	}
	return "zh-CN"
}

func (f *commandSpeechFeedback) restoreListeningIfCurrent(
	attempt uint64,
	event realtimev1.CommandResultEvent,
	turnID string,
	playbackID string,
) {
	f.mu.Lock()
	current := !f.closed && f.attempt == attempt
	f.mu.Unlock()
	if !current {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.deps.Runtime.SetProcessingState(ctx, session.ProcessingStateUpdate{
		SessionID: event.SessionID, RuntimeState: session.RuntimeListening,
		ExpectedTurnID: &turnID, ExpectedPlaybackID: &playbackID,
	}); err != nil {
		f.logFailure(event, "restore_listening", err)
	}
}

func (f *commandSpeechFeedback) logFailure(event realtimev1.CommandResultEvent, stage string, err error) {
	f.deps.Logger.Warn("command feedback failed",
		"session_id", event.SessionID, "command_id", event.CommandID,
		"stage", stage, "command_status", event.Status, "error", fmt.Errorf("command feedback: %w", err))
}

var _ command.FeedbackSink = (*commandSpeechFeedback)(nil)
