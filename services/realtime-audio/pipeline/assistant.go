package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

var (
	// ErrAssistantReplyInvalid rejects an incomplete provider or event result.
	ErrAssistantReplyInvalid = errors.New("assistant reply is invalid")
	// ErrAssistantReplyAccepted prevents callers from rerunning an LLM after its reply was published.
	ErrAssistantReplyAccepted = errors.New("assistant reply accepted")
)

// AssistantReplySink publishes finalized replies without creating translation FinalTurns.
type AssistantReplySink interface {
	Publish(ctx context.Context, event realtimev1.AssistantReplyEvent) error
}

type AssistantReplyCommit func(ctx context.Context) error

// AssistantReplyCommitGate validates a Turn generation and publishes its reply atomically.
type AssistantReplyCommitGate interface {
	CommitAssistantReply(ctx context.Context, turn TurnContext, commit AssistantReplyCommit) (bool, error)
}

type AssistantHandlerDependencies struct {
	LLM     assistant.Provider
	Replies AssistantReplySink
	Gate    AssistantReplyCommitGate
	Usage   UsageFactSink
	Speech  *SpeechOutput
	Runtime session.RuntimeStateReporter
	Now     func() time.Time
	Latency LatencyLogger
}

// AssistantHandler handles only the business stages after the shared ASR final.
type AssistantHandler struct {
	llm     assistant.Provider
	replies AssistantReplySink
	gate    AssistantReplyCommitGate
	usage   UsageFactSink
	speech  *SpeechOutput
	runtime session.RuntimeStateReporter
	now     func() time.Time
	latency LatencyLogger
}

func NewAssistantHandler(deps AssistantHandlerDependencies) *AssistantHandler {
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &AssistantHandler{
		llm: deps.LLM, replies: deps.Replies, gate: deps.Gate, usage: deps.Usage,
		speech: deps.Speech, runtime: deps.Runtime, now: now, latency: deps.Latency,
	}
}

func (h *AssistantHandler) HandleASRFinal(ctx context.Context, turn TurnContext, result asr.FinalResult) (returnErr error) {
	if err := h.validate(); err != nil {
		return err
	}
	accepted := false
	defer func() {
		if err := h.runtime.SetProcessingState(ctx, session.ProcessingStateUpdate{
			SessionID: turn.SessionID, RuntimeState: session.RuntimeListening,
		}); err != nil {
			restoreErr := fmt.Errorf("restore listening runtime: %w", err)
			if accepted {
				restoreErr = assistantReplyAcceptedError("restore listening runtime", err)
			}
			returnErr = errors.Join(returnErr, restoreErr)
		}
	}()
	turnID := turn.ID
	if err := h.runtime.SetProcessingState(ctx, session.ProcessingStateUpdate{
		SessionID: turn.SessionID, RuntimeState: session.RuntimeAssistantProcessing, CurrentTurnID: &turnID,
	}); err != nil {
		return fmt.Errorf("report assistant processing runtime: %w", err)
	}

	startedAt := time.Now()
	reply, err := h.llm.Reply(ctx, assistant.Request{
		SessionID: turn.SessionID, TurnID: turn.ID, Text: result.Text,
		Language: asr.NormalizeLanguage(result.SourceLanguage),
	})
	if err != nil {
		usageErr := h.publishLLMUsageIfPresent(ctx, turn, reply)
		return errors.Join(fmt.Errorf("generate assistant reply: %w", err), usageErr)
	}
	reply.Text = strings.TrimSpace(reply.Text)
	reply.Language = asr.NormalizeLanguage(reply.Language)
	if reply.Language == "" {
		reply.Language = asr.NormalizeLanguage(result.SourceLanguage)
	}
	if reply.Text == "" || reply.Language == "" {
		return ErrAssistantReplyInvalid
	}
	h.latency.Checkpoint("assistant_reply_done", turn, startedAt,
		"language", reply.Language, "provider_latency_ms", reply.LatencyMS,
		"input_tokens", reply.InputTokens, "output_tokens", reply.OutputTokens,
	)
	usage, err := buildUsageFact(turn, "assistant_llm", reply.Provider, reply.Model, 0,
		reply.InputTokens, reply.OutputTokens, reply.CostAmount, reply.Currency, h.now())
	if err != nil {
		return fmt.Errorf("prepare assistant LLM usage: %w", err)
	}
	event := realtimev1.AssistantReplyEvent{
		EventVersion: realtimev1.AssistantReplyEventVersion,
		EventID:      "assistant_reply_" + turn.ID, TraceID: turn.TraceID,
		SessionID: turn.SessionID, TurnID: turn.ID,
		RuntimeInstanceID: turn.Mode.RuntimeInstanceID, Generation: turn.Mode.Generation,
		Text: reply.Text, Language: reply.Language, OccurredAt: h.now(),
	}
	if err := validateAssistantReplyEvent(event); err != nil {
		return err
	}
	committed, err := h.gate.CommitAssistantReply(ctx, turn, func(commitCtx context.Context) error {
		return h.replies.Publish(commitCtx, event)
	})
	if err != nil {
		return fmt.Errorf("commit AssistantReply: %w", err)
	}
	if !committed {
		return nil
	}
	accepted = true
	if err := h.usage.Publish(ctx, usage); err != nil {
		return assistantReplyAcceptedError("publish assistant LLM usage", err)
	}
	ttsResult, err := h.speech.Play(ctx, SpeechOutputRequest{
		Turn: turn, Language: reply.Language, Text: reply.Text,
		PlaybackID: "assistant_playback_" + turn.ID,
	})
	if err != nil {
		return assistantReplyAcceptedError("play assistant reply", err)
	}
	ttsUsage, err := buildUsageFact(turn, "tts", ttsResult.Provider, ttsResult.Model,
		ttsResult.AudioDuration.Milliseconds(), 0, 0, ttsResult.CostAmount, ttsResult.Currency, h.now())
	if err != nil {
		return assistantReplyAcceptedError("prepare assistant TTS usage", err)
	}
	if err := h.usage.Publish(ctx, ttsUsage); err != nil {
		return assistantReplyAcceptedError("publish assistant TTS usage", err)
	}
	return nil
}

func (h *AssistantHandler) validate() error {
	if h == nil || h.llm == nil || h.replies == nil || h.gate == nil || h.usage == nil ||
		h.speech == nil || h.runtime == nil {
		return ErrPipelineDependencyRequired
	}
	return nil
}

func (h *AssistantHandler) publishLLMUsageIfPresent(ctx context.Context, turn TurnContext, result assistant.Result) error {
	if strings.TrimSpace(result.Provider) == "" || strings.TrimSpace(result.Model) == "" ||
		(result.InputTokens == 0 && result.OutputTokens == 0) {
		return nil
	}
	fact, err := buildUsageFact(turn, "assistant_llm", result.Provider, result.Model, 0,
		result.InputTokens, result.OutputTokens, result.CostAmount, result.Currency, h.now())
	if err != nil {
		return err
	}
	return h.usage.Publish(ctx, fact)
}

func validateAssistantReplyEvent(event realtimev1.AssistantReplyEvent) error {
	if err := event.Validate(); err != nil {
		// Keep the pipeline-specific sentinel for callers that classify invalid
		// assistant output, while the contracts package remains the sole field
		// validation authority.
		return errors.Join(ErrAssistantReplyInvalid, err)
	}
	return nil
}

func assistantReplyAcceptedError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrAssistantReplyAccepted, operation, err)
}

var _ ASRFinalHandler = (*AssistantHandler)(nil)
