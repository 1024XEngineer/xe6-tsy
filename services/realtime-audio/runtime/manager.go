// Package runtime assembles member 3's media processing graph for each session.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/segment"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
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

// AudioInput is the typed handoff from a WebRTC media adapter to the audio loop.
// SourceLanguage is an optional ASR hint for the input track. Empty means the
// provider should auto-detect (required for bilingual sessions). Language-config
// pairs are still read once by pipeline.TurnOpener for every Turn.
type AudioInput struct {
	Source         segment.FrameSource
	SourceLanguage string
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

// RuntimeReporter combines the narrow processing and terminal-failure ports
// required by a complete manager lifecycle.
type RuntimeReporter interface {
	session.RuntimeStateReporter
	session.RuntimeFailureReporter
}

// Dependencies contains member-3-owned adapters and downstream sinks.
type Dependencies struct {
	FrameSources   FrameSourceFactory
	NewSegmenter   SegmenterFactory
	Languages      session.LanguageConfigReader
	Speakers       recordsv1.SpeakerAttributionReader
	FinalTurns     recordsv1.FinalTurnSink
	Usage          pipeline.UsageFactSink
	Audio          pipeline.AudioChunkSink
	Runtime        RuntimeReporter
	Allocator      pipeline.TurnAllocator
	SpeakerTimeout time.Duration
	VoiceID        string
	Now            func() time.Time
}

// Manager owns one processing context per started realtime session.
// Start prepares the graph; Activate is used by LifecycleService after it has
// persisted RuntimeListening, and Stop is safe to retry after a timeout.
type Manager struct {
	mu        sync.Mutex
	locks     keyedLocker
	processor *pipeline.TurnProcessor
	failure   session.RuntimeFailureReporter
	deps      Dependencies
	entries   map[string]*entry
}

type entry struct {
	cancel      context.CancelFunc
	source      *closeOnceSource
	service     *segment.Service
	request     segment.Request
	ctx         context.Context
	done        chan struct{}
	operationID string
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
	return newManager(providers, deps)
}

// NewManagerFromEnvironment loads provider selection without loading .env files.
func NewManagerFromEnvironment(offline config.Providers, deps Dependencies) (*Manager, error) {
	providers, err := config.BuildProvidersFromEnvironment(offline)
	if err != nil {
		return nil, err
	}
	return newManager(providers, deps)
}

func newManager(providers config.Providers, deps Dependencies) (*Manager, error) {
	if deps.FrameSources == nil || deps.NewSegmenter == nil || deps.Languages == nil ||
		deps.FinalTurns == nil || deps.Usage == nil || deps.Audio == nil || deps.Runtime == nil {
		return nil, ErrDependencyRequired
	}
	if providers.ASR == nil || providers.Translation == nil || providers.TTS == nil {
		return nil, fmt.Errorf("%w: provider set", ErrDependencyRequired)
	}
	if deps.Allocator == nil {
		deps.Allocator = pipeline.NewMemoryTurnAllocator()
	}
	opener := pipeline.NewTurnOpener(deps.Allocator, deps.Languages)
	service := pipeline.NewPipelineService(pipeline.PipelineDependencies{
		Translator:     providers.Translation,
		TTS:            providers.TTS,
		Speakers:       deps.Speakers,
		FinalTurns:     deps.FinalTurns,
		Usage:          deps.Usage,
		Audio:          deps.Audio,
		Runtime:        deps.Runtime,
		SpeakerTimeout: deps.SpeakerTimeout,
		VoiceID:        deps.VoiceID,
		Now:            deps.Now,
	})
	return &Manager{
		processor: pipeline.NewTurnProcessor(pipeline.TurnProcessorDependencies{
			ASR: providers.ASR, Opener: opener, Pipeline: service,
		}),
		failure: deps.Runtime,
		deps:    deps, entries: make(map[string]*entry), locks: newKeyedLocker(),
	}, nil
}

// Start opens resources and registers the session without consuming media yet.
// LifecycleService calls Activate only after RuntimeListening is persisted.
func (m *Manager) Start(ctx context.Context, snapshot session.SessionSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil || m.processor == nil || m.deps.FrameSources == nil || m.deps.NewSegmenter == nil {
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
		if m.entries[snapshot.SessionID] == item {
			delete(m.entries, snapshot.SessionID)
		}
		m.mu.Unlock()
	}
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
	service, err := segment.NewService(segment.Dependencies{
		Source: owned, Segmenter: segmenter, Processor: m.processor,
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
		request: segment.Request{
			SessionID: snapshot.SessionID, AccountID: snapshot.AccountID,
			TraceID: snapshot.TraceID, SourceLanguage: input.SourceLanguage,
		}, done: make(chan struct{}),
	}

	m.mu.Lock()
	m.entries[snapshot.SessionID] = item
	m.mu.Unlock()
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
		m.mu.Lock()
		item.terminal = true
		item.err = err
		m.mu.Unlock()
		reportErr = m.reportFailure(ctx, item.request.SessionID)
		if reportErr != nil && !errors.Is(reportErr, context.Canceled) {
			err = errors.Join(err, fmt.Errorf("report runtime failure: %w", reportErr))
		}
	}
	m.mu.Lock()
	if ctx.Err() != nil && item.source.closeError() == nil {
		err = nil
	}
	item.err = err
	item.finished = true
	if reportFailure && reportErr == nil && ctx.Err() == nil && item.source.closeError() == nil &&
		m.entries[item.request.SessionID] == item {
		delete(m.entries, item.request.SessionID)
	}
	close(item.done)
	m.mu.Unlock()
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

func (m *Manager) reportFailure(ctx context.Context, sessionID string) error {
	reportCtx, cancel := context.WithTimeout(ctx, failureReportTimeout)
	defer cancel()

	// Lifecycle Stop may hold its session lock while waiting for this worker.
	// Keep the report cancelable so Stop cannot deadlock behind failure storage.
	done := make(chan error, 1)
	go func() { done <- m.failure.SetRuntimeFailed(reportCtx, sessionID) }()
	select {
	case err := <-done:
		return err
	case <-reportCtx.Done():
		return reportCtx.Err()
	}
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
		err := item.err
		if closeAttempt.err != nil {
			err = closeAttempt.err
		} else if finished {
			err = nil
		}
		if closeAttempt.err == nil && m.entries[sessionID] == item {
			delete(m.entries, sessionID)
		}
		m.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
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
