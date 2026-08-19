package command

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad"
)

var (
	ErrDependenciesRequired = errors.New("command gate dependencies are required")
	ErrInvalidOptions       = errors.New("invalid command gate options")
	ErrInvalidOpenRequest   = errors.New("invalid command gate open request")
	ErrDuplicateOpen        = errors.New("duplicate command gate open request")
	ErrGateClosed           = errors.New("command gate is closed")
)

// State is the command-only input lifecycle. Dormant is the only state in which a frame may
// continue to the ordinary VAD and business handler.
type State string

const (
	StateDormant     State = "dormant"
	StateArmed       State = "armed"
	StateCapturing   State = "capturing"
	StateRecognizing State = "recognizing"
)

const commandIDRetentionLimit = 256

// Failure identifies a recoverable command path outcome. These values are observations for
// metrics or feedback; none represents a terminal media-runtime error.
type Failure string

const (
	FailureNone           Failure = ""
	FailureWindowExpired  Failure = "window_expired"
	FailureNoSpeech       Failure = "no_speech"
	FailureASR            Failure = "asr_failed"
	FailureInterpretation Failure = "interpretation_failed"
	FailureNotAllowed     Failure = "command_not_allowed"
	FailureExecution      Failure = "execution_failed"
	FailureCanceled       Failure = "canceled"
	FailureInvalidAudio   Failure = "invalid_audio"
)

// Options bounds every command attempt. WindowTTL is a hard deadline for capture;
// NoSpeechTimeout bounds armed silence. MaxAudioDuration and EndSilence are passed to the shared
// VAD Segmenter, so ordinary and command audio use the same utterance-boundary implementation.
type Options struct {
	WindowTTL        time.Duration
	NoSpeechTimeout  time.Duration
	MaxAudioDuration time.Duration
	EndSilence       time.Duration
	PrefixPadding    time.Duration
}

// OpenRequest carries identity owned by the existing realtime runtime. CommandID becomes the
// isolated ASR turn ID and should therefore be unique within the session.
type OpenRequest struct {
	SessionID      string
	CommandID      string
	SourceLanguage string
	OpenedAt       time.Time
	// CaptureFrom allows the server-owned ingress buffer to replay audio that arrived before the
	// cross-protocol wake signal. It must use the same server clock as audio.Frame.CapturedAt.
	CaptureFrom time.Time
}

// ExecuteRequest asks the runtime adapter to apply one already allowlisted mode intent.
type ExecuteRequest struct {
	SessionID string
	CommandID string
	Language  string
	Command   Command
}

// AppliedLanguageConfig records an explicit language direction accepted by the API control plane.
// A non-nil value is an authoritative execution fact even when the realtime mode was unchanged.
type AppliedLanguageConfig struct {
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
	Version        int    `json:"version"`
}

// ExecutionResult carries the authoritative state returned by the runtime coordinator. The Gate
// uses it only to publish feedback and never treats feedback delivery as part of command execution.
type ExecutionResult struct {
	Status         realtimev1.ModeSwitchStatus
	State          realtimev1.ModeStateSnapshot
	LanguageConfig *AppliedLanguageConfig
}

// Executor is deliberately narrower than runtime.Manager, keeping mode-generation and operation
// construction in the runtime adapter instead of leaking those details into command recognition.
type Executor interface {
	ExecuteCommand(context.Context, ExecuteRequest) (ExecutionResult, error)
}

// SuccessFeedbackRequest contains only validated intent and authoritative post-execution facts.
// A generator may change wording but must not execute actions or reinterpret command success.
type SuccessFeedbackRequest struct {
	Command          Command
	Execution        ExecutionResult
	ResponseLanguage string
}

// SuccessFeedbackResult contains the optional phrasing and billable provider metadata produced by
// a feedback model. Execution state is intentionally absent because the generator cannot change it.
type SuccessFeedbackResult struct {
	Message      string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	CostAmount   string
	Currency     string
}

// FeedbackRequest carries the already accepted command result and, for a successful mode
// transition, the authoritative facts used to optionally phrase an audible confirmation. The event
// remains the deterministic fallback; feedback generation must never delay or alter its publication.
type FeedbackRequest struct {
	Event   realtimev1.CommandResultEvent
	Success *SuccessFeedbackRequest
}

// SuccessFeedbackGenerator renders best-effort natural-language confirmation after execution.
// Failure must never roll back or repeat the already completed command.
type SuccessFeedbackGenerator interface {
	GenerateSuccessFeedback(context.Context, SuccessFeedbackRequest) (SuccessFeedbackResult, error)
}

// SuccessFeedbackFunc adapts a function to the post-execution feedback boundary.
type SuccessFeedbackFunc func(context.Context, SuccessFeedbackRequest) (SuccessFeedbackResult, error)

func (f SuccessFeedbackFunc) GenerateSuccessFeedback(ctx context.Context, request SuccessFeedbackRequest) (SuccessFeedbackResult, error) {
	return f(ctx, request)
}

// ResultSink accepts one best-effort acknowledgement for each terminal command attempt. Publish
// implementations must enqueue promptly; transport I/O belongs to the sink's own worker.
type ResultSink interface {
	Publish(context.Context, realtimev1.CommandResultEvent) error
}

// FeedbackSink plays best-effort command speech after the typed result has been accepted. Publish
// must return promptly; Interrupt cancels only active feedback, while Close also waits for release.
type FeedbackSink interface {
	Publish(FeedbackRequest)
	Interrupt()
	Close()
}

// Observer receives bounded process metrics. Implementations must not retain command or session
// identities; detailed diagnosis belongs in structured logs outside this interface.
type Observer interface {
	RecordCommandInterpretation(time.Duration, bool)
	RecordCommandOutcome(realtimev1.CommandResultStatus, Failure)
}

// Dependencies use a command-specific VAD instance and the existing ASR provider boundary.
// The classifier must not be shared with ordinary Turn segmentation because its internal rolling
// context, if any, belongs to a different input lifecycle.
type Dependencies struct {
	Classifier  vad.Classifier
	ASR         asr.Provider
	Interpreter Interpreter
	Validator   Validator
	Executor    Executor
	Results     ResultSink
	Feedback    FeedbackSink
	Observer    Observer
	Logger      *slog.Logger
	Now         func() time.Time
}

// Result tells the caller whether the frame is quarantined from ordinary handlers. Recognition
// and execution continue asynchronously after StateRecognizing so provider latency never blocks
// the media ingress loop. Failure and Executed are reserved for terminal capture-time outcomes.
type Result struct {
	Consumed bool
	State    State
	Failure  Failure
	Executed *Command
}

// Gate owns one command attempt at a time. Open and Consume are synchronized because wake-word
// signals and WebRTC audio may arrive on different callbacks.
type Gate struct {
	mu               sync.Mutex
	deps             Dependencies
	options          Options
	segmenter        *vad.Segmenter
	state            State
	request          OpenRequest
	stream           asr.Stream
	attemptCtx       context.Context
	attemptCancel    context.CancelFunc
	stopCallerCancel func() bool
	attempt          uint64
	windowTimer      commandTimer
	silenceTimer     commandTimer
	afterFunc        func(time.Duration, func()) commandTimer
	now              func() time.Time
	logger           *slog.Logger
	seenCommandIDs   map[string]struct{}
	commandIDOrder   []string
	recognitionTasks map[uint64]chan struct{}
	captureComplete  bool
	closed           bool
}

type commandTimer interface {
	Stop() bool
}

// NewGate returns a dormant, immediately usable gate with explicit hard bounds.
func NewGate(deps Dependencies, options Options) (*Gate, error) {
	if deps.Classifier == nil || deps.ASR == nil || deps.Interpreter == nil || deps.Validator == nil || deps.Executor == nil {
		return nil, ErrDependenciesRequired
	}
	if options.WindowTTL <= 0 || options.NoSpeechTimeout <= 0 || options.MaxAudioDuration <= 0 ||
		options.EndSilence <= 0 || options.NoSpeechTimeout > options.WindowTTL ||
		options.MaxAudioDuration > options.WindowTTL || options.EndSilence >= options.MaxAudioDuration ||
		options.PrefixPadding < 0 || options.PrefixPadding >= options.MaxAudioDuration {
		return nil, ErrInvalidOptions
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	segmenter, err := vad.NewSegmenter(deps.Classifier, vad.Options{
		SilenceAfter:  options.EndSilence,
		MaxDuration:   options.MaxAudioDuration,
		PrefixPadding: options.PrefixPadding,
	})
	if err != nil {
		return nil, ErrInvalidOptions
	}
	return &Gate{
		deps: deps, options: options, state: StateDormant,
		segmenter: segmenter, now: now, logger: logger,
		seenCommandIDs:   make(map[string]struct{}, commandIDRetentionLimit),
		commandIDOrder:   make([]string, 0, commandIDRetentionLimit),
		recognitionTasks: make(map[uint64]chan struct{}),
		afterFunc: func(delay time.Duration, callback func()) commandTimer {
			return time.AfterFunc(delay, callback)
		},
	}, nil
}

// State returns the current lifecycle state for diagnostics and tests.
func (g *Gate) State() State {
	if g == nil {
		return StateDormant
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// Cancel permanently closes this runtime-scoped Gate, cancels all attempts, and waits for any
// recognition worker to release provider resources. It is safe to retry during runtime shutdown.
func (g *Gate) Cancel() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.closed = true
	g.abandonLocked()
	tasks := make([]<-chan struct{}, 0, len(g.recognitionTasks))
	for _, done := range g.recognitionTasks {
		tasks = append(tasks, done)
	}
	g.mu.Unlock()
	if g.deps.Feedback != nil {
		g.deps.Feedback.Close()
	}
	for _, done := range tasks {
		<-done
	}
}

// Open arms a fresh bounded window. The same CommandID is an idempotent retry and returns
// ErrDuplicateOpen without disturbing audio; a different ID supersedes the incomplete attempt.
func (g *Gate) Open(request OpenRequest) error {
	if g == nil {
		return ErrDependenciesRequired
	}
	if request.SessionID == "" || request.CommandID == "" || request.OpenedAt.IsZero() ||
		(!request.CaptureFrom.IsZero() && request.CaptureFrom.After(request.OpenedAt)) {
		return ErrInvalidOpenRequest
	}
	if request.CaptureFrom.IsZero() {
		request.CaptureFrom = request.OpenedAt
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return ErrGateClosed
	}
	if _, duplicate := g.seenCommandIDs[request.CommandID]; duplicate {
		return ErrDuplicateOpen
	}
	g.rememberCommandIDLocked(request.CommandID)
	if g.deps.Feedback != nil {
		g.deps.Feedback.Interrupt()
	}
	if g.state != StateDormant {
		g.publishTerminalResultLocked(defaultFailureResult(g.request, FailureCanceled, g.now()), FailureCanceled)
	}
	g.abandonLocked()
	g.attempt++
	g.request = request
	g.state = StateArmed
	g.attemptCtx, g.attemptCancel = context.WithTimeout(context.Background(), g.options.WindowTTL)
	attempt := g.attempt
	g.windowTimer = g.afterFunc(g.options.WindowTTL, func() {
		g.expire(attempt, FailureWindowExpired)
	})
	g.silenceTimer = g.afterFunc(g.options.NoSpeechTimeout, func() {
		g.expire(attempt, FailureNoSpeech)
	})
	return nil
}

// Consume evaluates one normalized frame before the ordinary VAD. Once armed, every frame is
// consumed even when the command fails or reaches a bound; only a later frame observed while
// dormant may enter an ordinary business handler.
func (g *Gate) Consume(ctx context.Context, frame audio.Frame) Result {
	if g == nil {
		return Result{State: StateDormant}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.consumeLocked(ctx, frame, false)
}

// Replay consumes an utterance explicitly transferred from the ordinary Segmenter. Its frames may
// predate OpenedAt, but they still pass through the command Segmenter's identical VAD state machine.
func (g *Gate) Replay(ctx context.Context, frames []audio.Frame) Result {
	if g == nil {
		return Result{State: StateDormant}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	result := Result{Consumed: true, State: g.state}
	for _, frame := range frames {
		result = g.consumeLocked(ctx, frame, true)
		if result.State == StateDormant {
			return result
		}
	}
	return result
}

func (g *Gate) consumeLocked(ctx context.Context, frame audio.Frame, replayed bool) Result {
	if g.state == StateDormant {
		return Result{State: StateDormant}
	}
	// Recognition and semantic processing run asynchronously. Frames that arrive while they are
	// in flight still belong to the quarantined command boundary, but must never be appended to the
	// finalized ASR stream or start another recognition worker for the same command ID.
	if g.state == StateRecognizing {
		return Result{Consumed: true, State: StateRecognizing}
	}
	if err := ctx.Err(); err != nil {
		return g.failLocked(FailureCanceled)
	}
	if !validFrame(frame) {
		return g.failLocked(FailureInvalidAudio)
	}
	// Frames captured before the local wake decision belong to the ordinary
	// microphone timeline. Quarantine them without starting Command ASR so a
	// delayed media packet cannot become the spoken command.
	if !replayed && frame.CapturedAt.Before(g.request.CaptureFrom) {
		return Result{Consumed: true, State: StateArmed}
	}

	if !replayed && !frame.CapturedAt.Before(g.request.OpenedAt.Add(g.options.WindowTTL)) {
		return g.failLocked(FailureWindowExpired)
	}
	if !replayed && g.state == StateArmed && !frame.CapturedAt.Before(g.request.OpenedAt.Add(g.options.NoSpeechTimeout)) {
		return g.failLocked(FailureNoSpeech)
	}

	events, err := g.segmenter.Push(ctx, frame)
	if err != nil {
		return g.failLocked(g.classifyFailure(ctx, FailureInvalidAudio))
	}
	result := Result{Consumed: true, State: g.state}
	for _, event := range events {
		switch event.Type {
		case vad.EventOpened:
			if failure := g.startCaptureLocked(ctx); failure != FailureNone {
				return g.failLocked(failure)
			}
			for _, prefix := range event.Frames {
				if err := g.stream.PushAudio(g.attemptCtx, prefix.PCM); err != nil {
					return g.failLocked(g.classifyFailure(ctx, FailureASR))
				}
			}
			if g.silenceTimer != nil {
				g.silenceTimer.Stop()
				g.silenceTimer = nil
			}
			result.State = StateCapturing
		case vad.EventAudio:
			if event.Frame == nil || g.stream == nil {
				return g.failLocked(FailureInvalidAudio)
			}
			if err := g.stream.PushAudio(g.attemptCtx, event.Frame.PCM); err != nil {
				return g.failLocked(g.classifyFailure(ctx, FailureASR))
			}
			result.State = StateCapturing
		case vad.EventFinal:
			return g.startRecognitionLocked(ctx)
		}
	}
	return result
}

func (g *Gate) startCaptureLocked(ctx context.Context) Failure {
	g.stopCallerCancel = context.AfterFunc(ctx, g.attemptCancel)
	stream, err := g.deps.ASR.StartStream(g.attemptCtx, asr.StreamRequest{
		SessionID: g.request.SessionID, TurnID: g.request.CommandID, SourceLanguage: g.request.SourceLanguage,
	})
	if err != nil || stream == nil {
		return g.classifyFailure(ctx, FailureASR)
	}
	g.stream = stream
	g.state = StateCapturing
	return FailureNone
}

func (g *Gate) startRecognitionLocked(ctx context.Context) Result {
	g.state = StateRecognizing
	attempt := g.attempt
	request := g.request
	attemptCtx := g.attemptCtx
	stream := g.stream
	done := make(chan struct{})
	g.recognitionTasks[attempt] = done
	go g.recognize(attempt, request, attemptCtx, ctx, stream, done)
	return Result{Consumed: true, State: StateRecognizing}
}

type recognitionOutcome struct {
	event    realtimev1.CommandResultEvent
	feedback *FeedbackRequest
	failure  Failure
}

func (g *Gate) recognize(
	attempt uint64,
	request OpenRequest,
	attemptCtx context.Context,
	caller context.Context,
	stream asr.Stream,
	done chan struct{},
) {
	outcome := g.runRecognition(request, attemptCtx, caller, stream)

	g.mu.Lock()
	if g.attempt != attempt || g.state != StateRecognizing {
		g.mu.Unlock()
		g.completeRecognitionTask(attempt, done)
		return
	}
	g.abandonLocked()
	g.mu.Unlock()

	g.publishTerminalOutcome(outcome.event, outcome.failure, outcome.feedback)
	g.completeRecognitionTask(attempt, done)
}

func (g *Gate) completeRecognitionTask(attempt uint64, done chan struct{}) {
	g.mu.Lock()
	delete(g.recognitionTasks, attempt)
	g.mu.Unlock()
	close(done)
}

func (g *Gate) runRecognition(
	request OpenRequest,
	attemptCtx context.Context,
	caller context.Context,
	stream asr.Stream,
) recognitionOutcome {
	result, err := stream.Finish(attemptCtx)
	if err != nil {
		g.logRecognitionFailure(request, "asr_finish", err, 0)
		return failureOutcome(request, classifyAttemptFailure(caller, attemptCtx, FailureASR), realtimev1.CommandResultFailed, "命令语音识别失败，请重试", Command{}, g.now())
	}
	processingCtx, ok := g.beginProcessing(request.CommandID, caller)
	if !ok {
		return failureOutcome(request, FailureCanceled, realtimev1.CommandResultFailed, "上一条命令已被新的唤醒取消", Command{}, g.now())
	}
	commandText := stripWakeWordPrefix(result.Text)
	if strings.TrimSpace(commandText) == "" {
		g.logRecognitionFailure(request, "asr_empty", ErrInterpretRequestInvalid, len([]rune(result.Text)))
		return failureOutcome(request, FailureInterpretation, realtimev1.CommandResultFailed, "没有识别到唤醒词后的问题，请稍作停顿后重试", Command{}, g.now())
	}
	interpretationStarted := time.Now()
	candidate, err := g.deps.Interpreter.Interpret(processingCtx, InterpretRequest{
		SessionID: request.SessionID, CommandID: request.CommandID,
		Text: commandText, Language: result.SourceLanguage,
	})
	g.recordInterpretation(time.Since(interpretationStarted), err != nil)
	if err != nil {
		g.logRecognitionFailure(request, "interpretation", err, len([]rune(commandText)))
		return failureOutcome(request, classifyAttemptFailure(caller, processingCtx, FailureInterpretation), realtimev1.CommandResultFailed, "暂时无法理解这个指令，请重试", Command{}, g.now())
	}
	parsed, err := g.deps.Validator.Validate(candidate)
	if err != nil {
		g.logRecognitionFailure(request, "validation", err, len([]rune(commandText)))
		return failureOutcome(request, FailureNotAllowed, realtimev1.CommandResultUnsupported, "当前不支持这个能力", Command{}, g.now())
	}
	executeRequest := ExecuteRequest{
		SessionID: request.SessionID, CommandID: request.CommandID,
		Language: result.SourceLanguage, Command: parsed,
	}
	execution, err := g.deps.Executor.ExecuteCommand(processingCtx, executeRequest)
	if err != nil {
		status, message := executionFailureFeedback(parsed, err)
		return failureOutcome(request, classifyAttemptFailure(caller, processingCtx, FailureExecution), status, message, parsed, g.now())
	}
	event := commandResultEvent(request, parsed, execution, commandSuccessFallback(parsed, execution), g.now())
	return recognitionOutcome{
		event: event,
		feedback: &FeedbackRequest{
			Event: event,
			Success: &SuccessFeedbackRequest{
				Command: parsed, Execution: execution, ResponseLanguage: result.SourceLanguage,
			},
		},
	}
}

func (g *Gate) logRecognitionFailure(request OpenRequest, stage string, err error, textRunes int) {
	if g == nil || g.logger == nil {
		return
	}
	g.logger.Warn("realtime semantic command failed",
		"session_id", request.SessionID,
		"command_id", request.CommandID,
		"stage", stage,
		"text_runes", textRunes,
		"error", err,
	)
}

// beginProcessing ends the bounded capture phase after Command ASR has finalized. Semantic
// interpretation and the selected handler then use a fresh context tied to the realtime session,
// so capture expiry cannot cancel an assistant response. A new wake or runtime shutdown still
// cancels the replacement context through the Gate's existing attempt lifecycle.
func (g *Gate) beginProcessing(commandID string, runtimeCtx context.Context) (context.Context, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != StateRecognizing || g.request.CommandID != commandID || g.closed {
		return nil, false
	}
	if g.windowTimer != nil {
		g.windowTimer.Stop()
		g.windowTimer = nil
	}
	if g.stopCallerCancel != nil {
		g.stopCallerCancel()
		g.stopCallerCancel = nil
	}
	if g.attemptCancel != nil {
		g.attemptCancel()
	}
	processingCtx, cancel := context.WithCancel(runtimeCtx)
	g.attemptCtx = processingCtx
	g.attemptCancel = cancel
	g.captureComplete = true
	return processingCtx, true
}

func failureOutcome(
	request OpenRequest,
	failure Failure,
	status realtimev1.CommandResultStatus,
	message string,
	parsed Command,
	occurredAt time.Time,
) recognitionOutcome {
	return recognitionOutcome{failure: failure, event: realtimev1.CommandResultEvent{
		Type: realtimev1.CommandResultTopic, EventVersion: realtimev1.CommandResultEventVersion,
		CommandID: request.CommandID, SessionID: request.SessionID, Status: status,
		Action: string(parsed.Action), TargetMode: parsed.TargetMode, Message: message, OccurredAt: occurredAt.UTC(),
	}}
}

func (g *Gate) publishResultLocked(event realtimev1.CommandResultEvent) {
	g.publishResult(event)
}

func (g *Gate) publishTerminalResultLocked(event realtimev1.CommandResultEvent, failure Failure) {
	g.publishTerminalOutcome(event, failure, nil)
}

// publishTerminalOutcome keeps typed results, observations, and audible feedback aligned for every
// completed attempt. Cancellation remains silent because a replacement wake or Runtime shutdown
// owns the audio path; assistant queries produce their own answer audio and must not be duplicated.
func (g *Gate) publishTerminalOutcome(event realtimev1.CommandResultEvent, failure Failure, feedback *FeedbackRequest) {
	g.publishResult(event)
	g.recordOutcome(event.Status, failure)
	if g.deps.Feedback != nil && failure != FailureCanceled && event.Action != string(ActionAssistantQuery) {
		if feedback == nil {
			feedback = &FeedbackRequest{Event: event}
		}
		g.deps.Feedback.Publish(*feedback)
	}
}

func (g *Gate) publishResult(event realtimev1.CommandResultEvent) {
	if g.deps.Results == nil || event.Validate() != nil {
		return
	}
	_ = g.deps.Results.Publish(context.Background(), event)
}

func (g *Gate) recordInterpretation(duration time.Duration, failed bool) {
	if g.deps.Observer != nil {
		g.deps.Observer.RecordCommandInterpretation(duration, failed)
	}
}

func (g *Gate) recordOutcome(status realtimev1.CommandResultStatus, failure Failure) {
	if g.deps.Observer != nil {
		g.deps.Observer.RecordCommandOutcome(status, failure)
	}
}

func (g *Gate) rememberCommandIDLocked(commandID string) {
	if len(g.commandIDOrder) == commandIDRetentionLimit {
		delete(g.seenCommandIDs, g.commandIDOrder[0])
		copy(g.commandIDOrder, g.commandIDOrder[1:])
		g.commandIDOrder = g.commandIDOrder[:commandIDRetentionLimit-1]
	}
	g.seenCommandIDs[commandID] = struct{}{}
	g.commandIDOrder = append(g.commandIDOrder, commandID)
}

func commandResultEvent(request OpenRequest, parsed Command, execution ExecutionResult, message string, occurredAt time.Time) realtimev1.CommandResultEvent {
	status := realtimev1.CommandResultApplied
	if execution.Status == realtimev1.ModeSwitchUnchanged {
		status = realtimev1.CommandResultUnchanged
	}
	return realtimev1.CommandResultEvent{
		Type: realtimev1.CommandResultTopic, EventVersion: realtimev1.CommandResultEventVersion,
		CommandID: request.CommandID, SessionID: request.SessionID,
		RuntimeInstanceID: execution.State.RuntimeInstanceID, Generation: execution.State.Generation,
		Status: status, Action: string(parsed.Action), TargetMode: parsed.TargetMode,
		Message: message, OccurredAt: occurredAt.UTC(),
	}
}

func commandSuccessFallback(parsed Command, execution ExecutionResult) string {
	if parsed.Action == ActionAssistantQuery {
		return "助手已处理本轮提问"
	}
	if execution.LanguageConfig != nil && parsed.TargetMode == realtimev1.ModeInterpretation {
		return "已设置为" + spokenLanguageName(execution.LanguageConfig.SourceLanguage) + "和" +
			spokenLanguageName(execution.LanguageConfig.TargetLanguage) + "同声传译"
	}
	if execution.Status == realtimev1.ModeSwitchUnchanged {
		if parsed.TargetMode == realtimev1.ModeInterpretation {
			return "当前已是同声传译模式"
		}
		return "当前已是通用助手模式"
	}
	if parsed.TargetMode == realtimev1.ModeInterpretation {
		return "已进入同声传译模式"
	}
	return "已返回通用助手模式"
}

// ValidSuccessFeedback bounds generated text before it is sent to speech output.
func ValidSuccessFeedback(message string) bool {
	if message == "" || utf8.RuneCountInString(message) > 80 {
		return false
	}
	for _, r := range message {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func spokenLanguageName(code string) string {
	if name := map[string]string{
		"zh-CN": "中文", "en-US": "英语", "ja-JP": "日语", "ko-KR": "韩语",
		"fr-FR": "法语", "de-DE": "德语", "ru-RU": "俄语", "pt-BR": "葡萄牙语",
		"it-IT": "意大利语", "es-ES": "西班牙语",
	}[code]; name != "" {
		return name
	}
	return code
}

func executionFailureFeedback(parsed Command, err error) (realtimev1.CommandResultStatus, string) {
	if errors.Is(err, ErrClarificationRequired) {
		return realtimev1.CommandResultClarificationRequired, "请说明需要使用的语言方向"
	}
	if errors.Is(err, ErrUnsupported) {
		return realtimev1.CommandResultUnsupported, "当前不支持这个能力或参数"
	}
	if parsed.Action == ActionAssistantQuery {
		return realtimev1.CommandResultFailed, "助手暂时无法回答，请重试"
	}
	return realtimev1.CommandResultFailed, "命令未执行，原模式保持不变"
}

// stripWakeWordPrefix keeps only speech after the last fixed wake word. The Gate can reach this
// function only after trusted local KWS opened it; taking the suffix discards any earlier speech
// from the transferred utterance without admitting an unwoken utterance to semantic processing.
func stripWakeWordPrefix(text string) string {
	trimmed := strings.TrimSpace(text)
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, trimmed)
	lastEnd := -1
	for _, wake := range []string{"小灵小灵", "小林小林"} {
		if index := strings.LastIndex(compact, wake); index >= 0 && index+len(wake) > lastEnd {
			lastEnd = index + len(wake)
		}
	}
	if lastEnd >= 0 {
		return strings.TrimLeft(compact[lastEnd:], "，。！？,.!?：:、 ")
	}
	return trimmed
}

func (g *Gate) failLocked(failure Failure) Result {
	g.publishTerminalResultLocked(defaultFailureResult(g.request, failure, g.now()), failure)
	g.abandonLocked()
	return Result{Consumed: true, State: StateDormant, Failure: failure}
}

func defaultFailureResult(request OpenRequest, failure Failure, occurredAt time.Time) realtimev1.CommandResultEvent {
	message := map[Failure]string{
		FailureWindowExpired: "命令窗口已超时，请重新唤醒后再试",
		FailureNoSpeech:      "没有听到命令，请重新唤醒后再试",
		FailureASR:           "命令语音识别失败，请重试",
		FailureCanceled:      "上一条命令已被新的唤醒取消",
		FailureInvalidAudio:  "命令音频无效，请重试",
	}[failure]
	if message == "" {
		message = "命令未执行，原模式保持不变"
	}
	return realtimev1.CommandResultEvent{
		Type: realtimev1.CommandResultTopic, EventVersion: realtimev1.CommandResultEventVersion,
		CommandID: request.CommandID, SessionID: request.SessionID,
		Status: realtimev1.CommandResultFailed, Message: message, OccurredAt: occurredAt.UTC(),
	}
}

func (g *Gate) classifyFailure(caller context.Context, fallback Failure) Failure {
	return classifyAttemptFailure(caller, g.attemptCtx, fallback)
}

func classifyAttemptFailure(caller context.Context, attemptCtx context.Context, fallback Failure) Failure {
	if caller != nil && errors.Is(caller.Err(), context.Canceled) {
		return FailureCanceled
	}
	if attemptCtx != nil && errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		return FailureWindowExpired
	}
	return fallback
}

func (g *Gate) expire(attempt uint64, failure Failure) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == StateDormant || g.attempt != attempt {
		return
	}
	// A timer callback may already be waiting for the mutex when the first
	// speech frame stops the no-speech timer. Once capture has started, only the
	// overall window deadline may cancel the attempt.
	if failure == FailureNoSpeech && g.state != StateArmed {
		return
	}
	if failure == FailureWindowExpired && g.captureComplete {
		return
	}
	g.failLocked(failure)
}

func (g *Gate) abandonLocked() {
	if g.windowTimer != nil {
		g.windowTimer.Stop()
	}
	if g.silenceTimer != nil {
		g.silenceTimer.Stop()
	}
	if g.stream != nil {
		_ = g.stream.Close()
	}
	if g.attemptCancel != nil {
		g.attemptCancel()
	}
	if g.stopCallerCancel != nil {
		g.stopCallerCancel()
	}
	g.state = StateDormant
	g.request = OpenRequest{}
	if g.segmenter != nil {
		g.segmenter.Reset()
	}
	g.captureComplete = false
	g.stream = nil
	g.attemptCtx = nil
	g.attemptCancel = nil
	g.stopCallerCancel = nil
	g.windowTimer = nil
	g.silenceTimer = nil
}

func validFrame(frame audio.Frame) bool {
	return len(frame.PCM) > 0 && len(frame.PCM)%2 == 0 && frame.SampleRate == audio.SupportedSampleRate &&
		!frame.CapturedAt.IsZero()
}
