// Package runtime assembles member 3's media processing graph for each session.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/segment"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad"
)

var (
	ErrDependencyRequired = errors.New("realtime runtime dependency is required")
	ErrSessionIDRequired  = errors.New("realtime runtime session id is required")
	ErrTraceIDRequired    = errors.New("realtime runtime trace id is required")
	ErrAccountIDRequired  = errors.New("realtime runtime account id is required")
	ErrAudioInputRequired = errors.New("realtime runtime audio input is required")
	ErrPipelineNotFound   = errors.New("realtime runtime pipeline not found")
	ErrPipelineStopping   = errors.New("realtime runtime pipeline is stopping")
	ErrPipelineEnded      = errors.New("realtime runtime pipeline ended")
)

const failureReportTimeout = 5 * time.Second

var defaultCommandOptions = command.Options{
	WindowTTL:        15 * time.Second,
	NoSpeechTimeout:  2 * time.Second,
	MaxAudioDuration: 12 * time.Second,
	EndSilence:       800 * time.Millisecond,
	PrefixPadding:    500 * time.Millisecond,
}

// AudioInput is the typed handoff from a WebRTC media adapter to the audio loop.
// SourceLanguage is an optional ASR hint for the input track. Empty means the
// provider should auto-detect (required for bilingual sessions). Language-config
// pairs are still read once by pipeline.TurnOpener for every Turn.
type AudioInput struct {
	Source         segment.FrameSource
	SourceLanguage string
	// WakeWords is optional so transports without local wake-word detection
	// retain the legacy audio-only path.
	WakeWords segment.WakeWordSource
}

// FrameSourceFactory opens one normalized input source for a session.
type FrameSourceFactory interface {
	Open(ctx context.Context, snapshot session.SessionSnapshot) (AudioInput, error)
}

// FrameSourceFactoryFunc adapts a function to FrameSourceFactory.
type FrameSourceFactoryFunc func(context.Context, session.SessionSnapshot) (AudioInput, error)

func (f FrameSourceFactoryFunc) Open(ctx context.Context, snapshot session.SessionSnapshot) (AudioInput, error) {
	return f(ctx, snapshot)
}

// SegmenterFactory creates isolated VAD state for one session.
type SegmenterFactory func() (*vad.Segmenter, error)

// CommandClassifierFactory creates isolated speech classification state for a
// command window. It must not share a rolling classifier with ordinary VAD.
type CommandClassifierFactory func() (vad.Classifier, error)

// CommandInterpreterFactory builds one process-wide semantic boundary from the exact capability
// descriptors backed by registered mode handlers.
type CommandInterpreterFactory func([]command.CapabilityDescriptor) (command.Interpreter, error)

// PlaybackInterrupter cancels only the active playback for one session. It
// deliberately does not close the shared WebRTC track or connection.
type PlaybackInterrupter interface {
	InterruptCurrent(context.Context, string, string) error
}

// RuntimeReporter combines the narrow processing and terminal-failure ports
// required by a complete manager lifecycle.
type RuntimeReporter interface {
	session.RuntimeStateReporter
	session.RuntimeFailureReporter
}

// Dependencies contains member-3-owned adapters and downstream sinks.
type Dependencies struct {
	FrameSources          FrameSourceFactory
	NewSegmenter          SegmenterFactory
	NewCommandClassifier  CommandClassifierFactory
	NewCommandInterpreter CommandInterpreterFactory
	CommandOptions        command.Options
	Languages             session.LanguageConfigReader
	LanguageConfigurator  command.LanguageConfigurator
	CommandResults        command.ResultSink
	CommandObserver       command.Observer
	FinalTurns            recordsv1.FinalTurnSink
	AssistantReplies      pipeline.AssistantReplySink
	ModeChanges           ModeChangedSink
	Usage                 pipeline.UsageFactSink
	Audio                 pipeline.AudioChunkSink
	PlaybackInterrupter   PlaybackInterrupter
	Runtime               RuntimeReporter
	Allocator             pipeline.TurnAllocator
	VoiceID               string
	Logger                *slog.Logger
	Latency               *slog.Logger
	ProviderFailures      pipeline.ProviderFailureObserver
	Lifecycle             LifecycleObserver
	ModeCommands          ModeCommandObserver
	Now                   func() time.Time
	NewRuntimeInstanceID  RuntimeInstanceIDFactory
}

// LifecycleObserver receives process-local lifecycle counters without session
// identifiers. Calls happen only after a start or stop is committed in memory.
type LifecycleObserver interface {
	RecordRuntimeStarted()
	RecordRuntimeStopped()
}

// Manager owns one processing context per started realtime session.
// Start prepares the graph; Activate is used by LifecycleService after it has
// persisted RuntimeListening, and Stop is safe to retry after a timeout.
type Manager struct {
	mu                 sync.Mutex
	locks              keyedLocker
	processor          *pipeline.TurnProcessor
	commandASR         asr.Provider
	commandInterpreter command.Interpreter
	commandValidator   command.Validator
	commandOpener      *pipeline.TurnOpener
	speech             *pipeline.SpeechOutput
	playback           *pipeline.PipelineService
	router             *modeRouter
	failure            session.RuntimeFailureReporter
	logger             *slog.Logger
	deps               Dependencies
	entries            map[string]*entry
}

type entry struct {
	cancel      context.CancelFunc
	source      *closeOnceSource
	service     *segment.Service
	request     segment.Request
	ctx         context.Context
	done        chan struct{}
	operationID string
	mode        *modeCoordinator
	command     *command.Gate
	err         error
	active      bool
	stopping    bool
	terminal    bool
	finished    bool
}

// NewManager builds configured providers and assembles the reusable pipeline.
func NewManager(providerConfig config.ProviderConfig, offline config.Providers, deps Dependencies) (*Manager, error) {
	providers, err := config.BuildProviders(providerConfig, offline)
	if err != nil {
		return nil, err
	}
	return newManagerWithLabels(providers, labelsFromConfig(providerConfig), deps)
}

// NewManagerFromEnvironment loads provider selection without loading .env files.
func NewManagerFromEnvironment(offline config.Providers, deps Dependencies) (*Manager, error) {
	providerConfig, err := config.LoadProviderConfigFromEnvironment()
	if err != nil {
		return nil, err
	}
	return NewManager(providerConfig, offline, deps)
}

func newManager(providers config.Providers, deps Dependencies) (*Manager, error) {
	return newManagerWithLabels(providers, providerLabels{}, deps)
}

type providerLabels struct {
	asr         string
	llm         string
	translation string
	tts         string
}

func labelsFromConfig(providerConfig config.ProviderConfig) providerLabels {
	return providerLabels{
		asr:         normalizedProviderLabel(providerConfig.ASR.Provider),
		llm:         normalizedProviderLabel(providerConfig.Translation.Provider),
		translation: normalizedProviderLabel(providerConfig.Translation.Provider),
		tts:         normalizedProviderLabel(providerConfig.TTS.Provider),
	}
}

func normalizedProviderLabel(provider config.ProviderName) string {
	label := strings.ToLower(strings.TrimSpace(string(provider)))
	if label == "" {
		return string(config.ProviderMock)
	}
	return label
}

func newManagerWithLabels(providers config.Providers, labels providerLabels, deps Dependencies) (*Manager, error) {
	if deps.FrameSources == nil || deps.NewSegmenter == nil || deps.Languages == nil ||
		deps.FinalTurns == nil || deps.ModeChanges == nil || deps.Usage == nil || deps.Audio == nil || deps.Runtime == nil {
		return nil, ErrDependencyRequired
	}
	if providers.ASR == nil || providers.Translation == nil || providers.TTS == nil {
		return nil, fmt.Errorf("%w: provider set", ErrDependencyRequired)
	}
	if deps.Allocator == nil {
		deps.Allocator = pipeline.NewMemoryTurnAllocator()
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.NewRuntimeInstanceID == nil {
		deps.NewRuntimeInstanceID = defaultRuntimeInstanceID
	}
	manager := &Manager{
		failure: deps.Runtime, logger: deps.Logger,
		deps: deps, entries: make(map[string]*entry), locks: newKeyedLocker(),
	}
	opener := pipeline.NewTurnOpener(deps.Allocator, deps.Languages, managerTurnModeReader{manager: manager})
	manager.commandOpener = opener
	latency := pipeline.LatencyLogger{Logger: deps.Latency, Observer: deps.ProviderFailures}
	speech := pipeline.NewSpeechOutput(pipeline.SpeechOutputDependencies{
		TTS: providers.TTS, Audio: deps.Audio, Runtime: deps.Runtime,
		VoiceID: deps.VoiceID, Provider: labels.tts, Latency: latency,
	})
	commitGate := managerTurnCommitGate{manager: manager}
	service := pipeline.NewPipelineService(pipeline.PipelineDependencies{
		Translator: providers.Translation, TranslationProvider: labels.translation,
		FinalTurns: deps.FinalTurns,
		FinalGate:  commitGate,
		Usage:      deps.Usage,
		Runtime:    deps.Runtime,
		Now:        deps.Now,
		Speech:     speech,
		Latency:    latency,
	})
	// The router registry is the capability source of truth. The coordinator and command
	// interpreter therefore expose only modes backed by an actual handler.
	handlers := map[realtimev1.Mode]pipeline.ASRFinalHandler{
		realtimev1.ModeInterpretation: service,
	}
	if providers.Assistant != nil {
		if deps.AssistantReplies == nil {
			return nil, fmt.Errorf("%w: assistant reply sink", ErrDependencyRequired)
		}
		handlers[realtimev1.ModeAssistant] = pipeline.NewAssistantHandler(pipeline.AssistantHandlerDependencies{
			LLM: providers.Assistant, Provider: labels.llm, Replies: deps.AssistantReplies, Gate: commitGate,
			Usage: deps.Usage, Speech: speech, Runtime: deps.Runtime, Now: deps.Now,
			Latency: latency,
		})
	}
	router, err := newModeRouter(handlers)
	if err != nil {
		return nil, fmt.Errorf("create mode router: %w", err)
	}
	manager.processor = pipeline.NewTurnProcessor(pipeline.TurnProcessorDependencies{
		ASR: providers.ASR, ASRProvider: labels.asr, Opener: opener, Pipeline: service, Finals: router,
	})
	manager.commandASR = providers.ASR
	registry, err := commandRegistry(router.availableModes())
	if err != nil {
		return nil, fmt.Errorf("create command capability registry: %w", err)
	}
	manager.commandValidator = registry
	if deps.NewCommandInterpreter == nil {
		manager.commandInterpreter = command.LegacyInterpreter{}
	} else {
		manager.commandInterpreter, err = deps.NewCommandInterpreter(registry.Descriptors())
		if err != nil {
			return nil, fmt.Errorf("create command interpreter: %w", err)
		}
		if manager.commandInterpreter == nil {
			return nil, fmt.Errorf("%w: command interpreter", ErrDependencyRequired)
		}
	}
	manager.playback = service
	manager.speech = speech
	manager.router = router
	return manager, nil
}

func commandRegistry(modes []realtimev1.Mode) (*command.Registry, error) {
	descriptors := make([]command.CapabilityDescriptor, 0, len(modes))
	for _, mode := range modes {
		switch mode {
		case realtimev1.ModeAssistant:
			descriptors = append(descriptors, command.CapabilityDescriptor{
				Mode: mode, Description: "通用 AI 助手", SchemaVersion: 1,
				Actions: []command.Action{command.ActionReturnToAssistant, command.ActionAssistantQuery},
			})
		case realtimev1.ModeInterpretation:
			descriptors = append(descriptors, command.CapabilityDescriptor{
				Mode: mode, Description: "双语同声传译", SchemaVersion: 1,
				Actions: []command.Action{command.ActionActivateMode},
			})
		}
	}
	return command.NewRegistry(descriptors...)
}

// PlayFallback sends an immutable translated-text snapshot through the active
// session's playback graph. The session run context cancels playback when Stop
// begins, while the request context still bounds the caller's wait.
func (m *Manager) PlayFallback(ctx context.Context, request realtimev1.FallbackPlaybackRequest) error {
	if err := ctx.Err(); err != nil {
		return pipeline.MarkFallbackPlaybackNotStarted(err)
	}
	if m == nil || m.playback == nil {
		return pipeline.MarkFallbackPlaybackNotStarted(ErrDependencyRequired)
	}
	if request.SessionID == "" {
		return pipeline.MarkFallbackPlaybackNotStarted(ErrSessionIDRequired)
	}

	unlock := m.locks.lock(request.SessionID)
	m.mu.Lock()
	item := m.entries[request.SessionID]
	if item == nil || !item.active || item.stopping || item.terminal || item.finished {
		m.mu.Unlock()
		unlock()
		return pipeline.MarkFallbackPlaybackNotStarted(session.ErrRuntimeNotFound)
	}
	runCtx := item.ctx
	accountID := item.request.AccountID
	m.mu.Unlock()
	unlock()

	playbackCtx, cancel := context.WithCancel(ctx)
	stopCancellation := context.AfterFunc(runCtx, cancel)
	defer func() {
		stopCancellation()
		cancel()
	}()
	return m.playback.PlayFallback(playbackCtx, pipeline.FallbackPlayback{
		SessionID: request.SessionID, TurnID: request.TurnID, AccountID: accountID,
		TraceID: request.TraceID, TargetLanguage: request.TargetLanguage,
		TranslatedText: request.TranslatedText, LanguageConfigVersion: int64(request.LanguageConfigVersion),
		PlaybackID: "fallback_" + request.OperationID,
	})
}

// Start opens resources and registers the session without consuming media yet.
// LifecycleService calls Activate only after RuntimeListening is persisted.
func (m *Manager) Start(ctx context.Context, snapshot session.SessionSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil || m.processor == nil || m.router == nil || m.deps.FrameSources == nil || m.deps.NewSegmenter == nil {
		return ErrDependencyRequired
	}
	if snapshot.SessionID == "" {
		return ErrSessionIDRequired
	}
	if snapshot.AccountID == "" {
		return ErrAccountIDRequired
	}
	if snapshot.StartOperationID == "" {
		return session.ErrStartOperationIDRequired
	}
	if snapshot.TraceID == "" {
		return ErrTraceIDRequired
	}
	unlock := m.locks.lock(snapshot.SessionID)
	defer unlock()

	m.mu.Lock()
	item := m.entries[snapshot.SessionID]
	if item == nil {
		m.mu.Unlock()
	} else if !item.terminal {
		if item.operationID != snapshot.StartOperationID {
			m.mu.Unlock()
			return session.ErrRuntimeOperationConflict
		}
		m.mu.Unlock()
		return nil
	} else {
		// Do not replace a terminal entry while its failure state is still
		// being published; otherwise the old report could overwrite the new run.
		done := item.done
		m.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := item.source.closeContext(ctx); err != nil {
			return fmt.Errorf("close previous audio input: %w", err)
		}
		m.mu.Lock()
		removed := m.removeEntryLocked(snapshot.SessionID, item)
		m.mu.Unlock()
		m.recordRuntimeStopped(removed)
	}
	runtimeInstanceID, err := m.deps.NewRuntimeInstanceID()
	if err != nil {
		return err
	}
	if runtimeInstanceID == "" {
		return ErrRuntimeInstanceIDRequired
	}
	initialMode := snapshot.InitialMode.OrLegacyDefault()
	mode, err := newModeCoordinator(
		snapshot.SessionID,
		runtimeInstanceID,
		initialMode,
		m.router.availableModes(),
		m.deps.ModeChanges,
		m.deps.Now,
	)
	if err != nil {
		return fmt.Errorf("create mode coordinator: %w", err)
	}
	mode.observer = m.deps.ModeCommands
	input, err := m.deps.FrameSources.Open(ctx, snapshot)
	if err != nil {
		return fmt.Errorf("open audio input: %w", err)
	}
	owned := newCloseOnceSource(input.Source)
	if input.Source == nil {
		closeErr := owned.closeContext(ctx)
		return errors.Join(ErrAudioInputRequired, closeErr)
	}
	segmenter, err := m.deps.NewSegmenter()
	if err != nil {
		closeErr := owned.closeContext(ctx)
		return errors.Join(fmt.Errorf("create VAD segmenter: %w", err), closeErr)
	}
	var commandGate *command.Gate
	if input.WakeWords != nil && m.deps.NewCommandClassifier != nil {
		classifier, classifierErr := m.deps.NewCommandClassifier()
		if classifierErr != nil {
			closeErr := owned.closeContext(ctx)
			return errors.Join(fmt.Errorf("create command classifier: %w", classifierErr), closeErr)
		}
		options := m.deps.CommandOptions
		if options == (command.Options{}) {
			options = defaultCommandOptions
		}
		var successFeedback command.SuccessFeedbackGenerator
		if candidate, ok := m.commandInterpreter.(command.SuccessFeedbackGenerator); ok {
			successFeedback = candidate
		}
		feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{
			Speech: m.speech, Usage: m.deps.Usage, Runtime: m.deps.Runtime,
			SuccessFeedback: successFeedback,
			AccountID:       snapshot.AccountID, TraceID: snapshot.TraceID, Logger: m.logger, Now: m.deps.Now,
		})
		gate, gateErr := command.NewGate(command.Dependencies{
			Classifier:  classifier,
			ASR:         m.commandASR,
			Interpreter: m.commandInterpreter,
			Validator:   m.commandValidator,
			Executor: commandExecutor{
				manager: m, languages: m.deps.Languages, configurator: m.deps.LanguageConfigurator,
			},
			Results:  m.deps.CommandResults,
			Feedback: feedback,
			Observer: m.deps.CommandObserver,
			Logger:   m.logger,
			Now:      m.deps.Now,
		}, options)
		if gateErr != nil {
			closeErr := owned.closeContext(ctx)
			return errors.Join(fmt.Errorf("create command gate: %w", gateErr), closeErr)
		}
		commandGate = gate
	}
	service, err := segment.NewService(segment.Dependencies{
		Source: owned, Segmenter: segmenter, Processor: m.processor,
		Command: newRuntimeCommandGate(commandGate, m.playbackInterrupter()), WakeWords: input.WakeWords,
		Latency: m.deps.Latency, Now: m.deps.Now,
	})
	if err != nil {
		closeErr := owned.closeContext(ctx)
		return errors.Join(fmt.Errorf("create audio segment service: %w", err), closeErr)
	}
	// The session outlives the start request. Use an independent context so
	// request-scoped credentials and large values are not retained until Stop.
	runCtx, cancel := context.WithCancel(context.Background())
	item = &entry{
		cancel: cancel, source: owned, service: service,
		ctx:         runCtx,
		operationID: snapshot.StartOperationID,
		mode:        mode, command: commandGate,
		request: segment.Request{
			SessionID: snapshot.SessionID, AccountID: snapshot.AccountID,
			TraceID: snapshot.TraceID, SourceLanguage: input.SourceLanguage,
		}, done: make(chan struct{}),
	}

	m.mu.Lock()
	m.entries[snapshot.SessionID] = item
	m.mu.Unlock()
	m.logger.Info("realtime mode observation",
		"event", "runtime_started",
		"session_id", snapshot.SessionID,
		"trace_id", snapshot.TraceID,
		"runtime_instance_id", runtimeInstanceID,
		"operation_id", snapshot.StartOperationID,
		"active_mode", initialMode,
	)
	if m.deps.Lifecycle != nil {
		m.deps.Lifecycle.RecordRuntimeStarted()
	}
	return nil
}

// Activate starts the media loop for a prepared session.
func (m *Manager) Activate(ctx context.Context, sessionID string, operationID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil {
		return ErrDependencyRequired
	}
	if sessionID == "" {
		return ErrSessionIDRequired
	}
	if operationID == "" {
		return session.ErrStartOperationIDRequired
	}
	unlock := m.locks.lock(sessionID)
	defer unlock()
	m.mu.Lock()
	item := m.entries[sessionID]
	if item == nil {
		m.mu.Unlock()
		return ErrPipelineNotFound
	}
	if item.operationID != operationID {
		m.mu.Unlock()
		return session.ErrRuntimeOperationConflict
	}
	if item.stopping {
		m.mu.Unlock()
		return ErrPipelineStopping
	}
	if item.active {
		m.mu.Unlock()
		return nil
	}
	item.active = true
	runCtx := item.ctx
	m.mu.Unlock()
	go m.run(item, runCtx)
	return nil
}

func (m *Manager) run(item *entry, ctx context.Context) {
	err := item.service.Run(ctx, item.request)
	reportFailure := false
	var reportErr error
	if err != nil && ctx.Err() == nil {
		reportFailure = true
	} else if err == nil && ctx.Err() == nil {
		// A source EOF ends the worker without an error, but it is still a
		// terminal media state and must not leave persisted Listening forever.
		err = ErrPipelineEnded
		reportFailure = true
	}
	if reportFailure {
		errorCode := runtimeFailureCode(err)
		m.logger.Error("realtime pipeline worker failed",
			"session_id", item.request.SessionID,
			"operation_id", item.operationID,
			"trace_id", item.request.TraceID,
			"error_code", errorCode,
			"error", err,
		)
		m.mu.Lock()
		item.terminal = true
		item.err = err
		m.mu.Unlock()
		reportErr = m.reportFailure(ctx, item.request.SessionID, errorCode)
		if reportErr != nil && !errors.Is(reportErr, context.Canceled) {
			m.logger.Error("realtime pipeline failure state report failed",
				"session_id", item.request.SessionID,
				"operation_id", item.operationID,
				"trace_id", item.request.TraceID,
				"error", reportErr,
			)
			err = errors.Join(err, fmt.Errorf("report runtime failure: %w", reportErr))
		}
	}
	m.mu.Lock()
	if ctx.Err() != nil && item.source.closeError() == nil {
		err = nil
	}
	item.err = err
	item.finished = true
	removed := false
	if reportFailure && reportErr == nil && ctx.Err() == nil && item.source.closeError() == nil {
		removed = m.removeEntryLocked(item.request.SessionID, item)
	}
	close(item.done)
	m.mu.Unlock()
	m.recordRuntimeStopped(removed)
}

// PipelineActive reports whether a worker is active or still settling its
// terminal state. Lifecycle recovery waits for settlement before replacing it.
func (m *Manager) PipelineActive(sessionID string) bool {
	if m == nil || sessionID == "" {
		return false
	}
	unlock := m.locks.lock(sessionID)
	defer unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.entries[sessionID]
	return item != nil && !item.finished
}

func (m *Manager) reportFailure(ctx context.Context, sessionID string, errorCode realtimev1.RuntimeErrorCode) error {
	reportCtx, cancel := context.WithTimeout(ctx, failureReportTimeout)
	defer cancel()

	// Lifecycle Stop may hold its session lock while waiting for this worker.
	// Keep the report cancelable so Stop cannot deadlock behind failure storage.
	done := make(chan error, 1)
	go func() { done <- m.failure.SetRuntimeFailed(reportCtx, sessionID, errorCode) }()
	select {
	case err := <-done:
		return err
	case <-reportCtx.Done():
		return reportCtx.Err()
	}
}

func runtimeFailureCode(err error) realtimev1.RuntimeErrorCode {
	if errors.Is(err, translate.ErrUnexpectedBehavior) {
		return session.ErrorCodeTranslationRejected
	}
	return session.ErrorCodePipelineFailed
}

// runtimeCommandGate adds runtime-owned wake side effects around the bounded
// command recognizer. Open itself has no context in the command contract, so
// playback interruption is deliberately best effort and never prevents the
// command window from quarantining subsequent audio.
type runtimeCommandGate struct {
	gate        *command.Gate
	interrupter PlaybackInterrupter
}

func newRuntimeCommandGate(gate *command.Gate, interrupter PlaybackInterrupter) segment.CommandGate {
	if gate == nil {
		return nil
	}
	return runtimeCommandGate{gate: gate, interrupter: interrupter}
}

func (m *Manager) playbackInterrupter() PlaybackInterrupter {
	if m == nil {
		return nil
	}
	if m.deps.PlaybackInterrupter != nil {
		return m.deps.PlaybackInterrupter
	}
	interrupter, _ := m.deps.Audio.(PlaybackInterrupter)
	return interrupter
}

func (g runtimeCommandGate) Open(request command.OpenRequest) error {
	if err := g.gate.Open(request); err != nil {
		return err
	}
	if g.interrupter != nil {
		interruptCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = g.interrupter.InterruptCurrent(interruptCtx, request.SessionID, "wake_word_detected")
		cancel()
	}
	return nil
}

func (g runtimeCommandGate) Consume(ctx context.Context, frame audio.Frame) command.Result {
	return g.gate.Consume(ctx, frame)
}

func (g runtimeCommandGate) Replay(ctx context.Context, frames []audio.Frame) command.Result {
	return g.gate.Replay(ctx, frames)
}

func (g runtimeCommandGate) Cancel() {
	g.gate.Cancel()
}

// Stop cancels processing, closes the input source, and waits for the loop.
func (m *Manager) Stop(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil {
		return nil
	}
	if sessionID == "" {
		return ErrSessionIDRequired
	}
	unlock := m.locks.lock(sessionID)
	defer unlock()
	m.mu.Lock()
	item := m.entries[sessionID]
	if item == nil {
		m.mu.Unlock()
		return nil
	}
	// A finished entry keeps the previous worker or cleanup attempt's error.
	// Stop must report only the close attempt performed by this call.
	finished := item.finished
	item.stopping = true
	active := item.active
	item.cancel()
	m.mu.Unlock()

	closeAttempt := item.source.beginClose()
	if !active || finished {
		select {
		case <-closeAttempt.done:
			m.mu.Lock()
			if !item.finished {
				item.err = closeAttempt.err
				item.finished = true
				close(item.done)
			}
			m.mu.Unlock()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case <-item.done:
		m.mu.Lock()
		stopped := false
		err := item.err
		if closeAttempt.err != nil {
			err = closeAttempt.err
		} else if finished {
			err = nil
		}
		if closeAttempt.err == nil {
			stopped = m.removeEntryLocked(sessionID, item)
		}
		m.mu.Unlock()
		m.recordRuntimeStopped(stopped)
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// removeEntryLocked is the single ownership transition from active/retained to
// absent. The caller holds m.mu and records the lifecycle counter after unlock.
func (m *Manager) removeEntryLocked(sessionID string, item *entry) bool {
	if m.entries[sessionID] != item {
		return false
	}
	delete(m.entries, sessionID)
	return true
}

func (m *Manager) recordRuntimeStopped(removed bool) {
	if removed && m.deps.Lifecycle != nil {
		m.deps.Lifecycle.RecordRuntimeStopped()
	}
}

// closeOnceSource coalesces concurrent attempts, remains closed after success,
// and permits a later cleanup call to retry a failed close.
type closeOnceSource struct {
	segment.FrameSource
	mu      sync.Mutex
	attempt *closeAttempt
	err     error
}

type closeAttempt struct {
	done chan struct{}
	err  error
}

func newCloseOnceSource(source segment.FrameSource) *closeOnceSource {
	return &closeOnceSource{FrameSource: source}
}

func (s *closeOnceSource) beginClose() *closeAttempt {
	s.mu.Lock()
	if s.attempt != nil {
		attempt := s.attempt
		s.mu.Unlock()
		return attempt
	}
	attempt := &closeAttempt{done: make(chan struct{})}
	s.attempt = attempt
	s.mu.Unlock()

	go func() {
		if s.FrameSource != nil {
			attempt.err = s.FrameSource.Close()
		}
		s.mu.Lock()
		s.err = attempt.err
		if attempt.err != nil && s.attempt == attempt {
			s.attempt = nil
		}
		close(attempt.done)
		s.mu.Unlock()
	}()
	return attempt
}

func (s *closeOnceSource) Close() error {
	attempt := s.beginClose()
	<-attempt.done
	return attempt.err
}

func (s *closeOnceSource) closeContext(ctx context.Context) error {
	attempt := s.beginClose()
	select {
	case <-attempt.done:
		return attempt.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *closeOnceSource) closeError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

type keyedLocker struct {
	mu    sync.Mutex
	locks map[string]*keyedLockEntry
}

type keyedLockEntry struct {
	mutex      sync.Mutex
	references int
}

func newKeyedLocker() keyedLocker {
	return keyedLocker{locks: make(map[string]*keyedLockEntry)}
}

func (l *keyedLocker) lock(key string) func() {
	l.mu.Lock()
	item := l.locks[key]
	if item == nil {
		item = &keyedLockEntry{}
		l.locks[key] = item
	}
	item.references++
	l.mu.Unlock()

	item.mutex.Lock()
	return func() {
		item.mutex.Unlock()
		l.mu.Lock()
		item.references--
		if item.references == 0 && l.locks[key] == item {
			delete(l.locks, key)
		}
		l.mu.Unlock()
	}
}

var _ session.PipelineManager = (*Manager)(nil)
var _ session.PipelineActivator = (*Manager)(nil)
var _ session.PipelineHealthReader = (*Manager)(nil)
