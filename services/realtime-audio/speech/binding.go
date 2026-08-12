package speech

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

var (
	// ErrBindingCoordinatorRequired reports calls on a nil coordinator.
	ErrBindingCoordinatorRequired = errors.New("speech binding coordinator is required")
	// ErrSessionIDRequired prevents bindings that cannot be scoped to one runtime session.
	ErrSessionIDRequired = errors.New("speech binding session id is required")
	// ErrLanguageConfigVersionRequired prevents binding a Turn to an unversioned language configuration.
	ErrLanguageConfigVersionRequired = errors.New("speech language configuration version is required")
	// ErrBindingNotPrepared indicates that no usable binding exists for the requested version.
	ErrBindingNotPrepared = errors.New("speech binding is not prepared")
	// ErrBindingPending indicates that exact-version preparation is still in progress.
	ErrBindingPending = errors.New("speech binding preparation is pending")
	// ErrBindingVersionConflict rejects stale preparation and exact-version acquisition mismatches.
	ErrBindingVersionConflict = errors.New("speech binding version conflict")
	// ErrBindingPreparationConflict rejects different language pairs under the same config version.
	ErrBindingPreparationConflict = errors.New("speech binding preparation conflict")
	// ErrBindingSuperseded reports a preparation displaced by a newer config version.
	ErrBindingSuperseded = errors.New("speech binding preparation was superseded")
	// ErrBindingSessionClosed reports a preparation interrupted by session teardown.
	ErrBindingSessionClosed = errors.New("speech binding session is closed")
)

// Release returns one lease to a BindingCoordinator. It is safe to call more
// than once, which keeps deferred cleanup safe across competing error paths.
type Release func()

// TurnSpeechBinding is the immutable ASR/TTS selection captured for one Turn.
// The adapters are references to existing vendor-neutral provider boundaries;
// callers must release their lease only after all provider work for the Turn ends.
type TurnSpeechBinding struct {
	SessionID             string
	LanguageConfigVersion int64
	Route                 SpeechRoute
	ASRProfile            Profile
	TTSProfile            Profile
	ASR                   asr.Provider
	TTS                   tts.Provider
}

// BindingCoordinator resolves language routes once per configuration version and
// leases immutable bindings to Turns. A pending identity and monotonic version
// fence prevent a late resolver result from replacing a newer active selection.
type BindingCoordinator struct {
	mu       sync.Mutex
	registry *ProviderRegistry
	resolver RouteResolver
	sessions map[string]*sessionBindingState
}

type sessionBindingState struct {
	latestVersion     int64
	latestLanguageKey string
	active            *preparedBinding
	pending           *pendingBinding
	lastFailure       *pendingBinding
	leases            int
	closed            bool
}

type preparedBinding struct {
	binding     TurnSpeechBinding
	languageKey string
}

type pendingBinding struct {
	version     int64
	languageKey string
	done        chan struct{}
	cancel      context.CancelFunc
	err         error
	finished    bool
}

// NewBindingCoordinator constructs an isolated coordinator. The registry and
// resolver are fixed after construction so a prepared binding cannot observe a
// different provider map halfway through a Turn.
func NewBindingCoordinator(registry *ProviderRegistry, resolver RouteResolver) (*BindingCoordinator, error) {
	if registry == nil {
		return nil, ErrProviderRegistryRequired
	}
	if resolver == nil {
		return nil, ErrRouteResolverRequired
	}
	return &BindingCoordinator{
		registry: registry,
		resolver: resolver,
		sessions: make(map[string]*sessionBindingState),
	}, nil
}

// Prepare resolves and installs the speech binding for one language-config
// version. Calls for the same version and language pair share one preparation;
// a larger version supersedes any older pending resolution before it can commit.
func (c *BindingCoordinator) Prepare(
	ctx context.Context,
	sessionID string,
	languageConfigVersion int64,
	languageA string,
	languageB string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return ErrBindingCoordinatorRequired
	}
	if sessionID == "" {
		return ErrSessionIDRequired
	}
	if languageConfigVersion < 1 {
		return ErrLanguageConfigVersionRequired
	}
	languageKey, err := routeKey(languageA, languageB)
	if err != nil {
		return err
	}

	c.mu.Lock()
	state := c.sessions[sessionID]
	if state == nil {
		state = &sessionBindingState{}
		c.sessions[sessionID] = state
	}
	if state.closed {
		c.mu.Unlock()
		return ErrBindingSessionClosed
	}
	if languageConfigVersion < state.latestVersion {
		c.mu.Unlock()
		return fmt.Errorf("%w: requested %d, latest %d", ErrBindingVersionConflict, languageConfigVersion, state.latestVersion)
	}
	if languageConfigVersion == state.latestVersion {
		if state.latestLanguageKey != "" && state.latestLanguageKey != languageKey {
			c.mu.Unlock()
			return ErrBindingPreparationConflict
		}
		if state.active != nil && state.active.binding.LanguageConfigVersion == languageConfigVersion {
			c.mu.Unlock()
			return nil
		}
		if state.lastFailure != nil && state.lastFailure.version == languageConfigVersion {
			result := pendingResultLocked(state.lastFailure)
			c.mu.Unlock()
			return result
		}
		if state.pending != nil {
			pending := state.pending
			c.mu.Unlock()
			return waitForPending(ctx, c, pending)
		}
	}

	if state.pending != nil {
		c.finishPendingLocked(state.pending, ErrBindingSuperseded)
		state.pending.cancel()
	}
	state.lastFailure = nil
	prepareCtx, cancel := context.WithCancel(ctx)
	pending := &pendingBinding{
		version:     languageConfigVersion,
		languageKey: languageKey,
		done:        make(chan struct{}),
		cancel:      cancel,
	}
	state.latestVersion = languageConfigVersion
	state.latestLanguageKey = languageKey
	state.pending = pending
	c.mu.Unlock()

	route, resolveErr := c.resolver.ResolveBinding(prepareCtx, languageA, languageB)
	cancel()
	if resolveErr == nil {
		if err := ctx.Err(); err != nil {
			resolveErr = err
		}
	}
	if resolveErr == nil {
		resolveErr = c.installableBinding(sessionID, languageConfigVersion, languageKey, route, languageA, languageB, pending)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if result := pendingResultLocked(pending); result != nil {
		return result
	}
	if c.sessions[sessionID] != state || state.closed {
		c.finishPendingLocked(pending, ErrBindingSessionClosed)
		return ErrBindingSessionClosed
	}
	if state.pending != pending || state.latestVersion != languageConfigVersion {
		c.finishPendingLocked(pending, ErrBindingSuperseded)
		return ErrBindingSuperseded
	}
	if resolveErr != nil {
		state.pending = nil
		state.lastFailure = pending
		c.finishPendingLocked(pending, resolveErr)
		return resolveErr
	}

	// The pending pointer is the CAS token: only the still-current operation may
	// replace active. Existing Turn leases retain their own immutable binding.
	binding, err := c.buildBinding(sessionID, languageConfigVersion, route)
	if err != nil {
		state.pending = nil
		c.finishPendingLocked(pending, err)
		return err
	}
	state.active = &preparedBinding{binding: binding, languageKey: languageKey}
	state.pending = nil
	c.finishPendingLocked(pending, nil)
	return nil
}

// AcquireForTurn returns a lease only when the active binding version exactly
// matches the language configuration captured by that Turn. It never falls back
// to an older active profile after a configuration change.
func (c *BindingCoordinator) AcquireForTurn(
	ctx context.Context,
	sessionID string,
	languageConfigVersion int64,
) (TurnSpeechBinding, Release, error) {
	if err := ctx.Err(); err != nil {
		return TurnSpeechBinding{}, nil, err
	}
	if c == nil {
		return TurnSpeechBinding{}, nil, ErrBindingCoordinatorRequired
	}
	if sessionID == "" {
		return TurnSpeechBinding{}, nil, ErrSessionIDRequired
	}
	if languageConfigVersion < 1 {
		return TurnSpeechBinding{}, nil, ErrLanguageConfigVersionRequired
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.sessions[sessionID]
	if state == nil {
		return TurnSpeechBinding{}, nil, ErrBindingNotPrepared
	}
	if state.closed {
		return TurnSpeechBinding{}, nil, ErrBindingSessionClosed
	}
	if state.pending != nil && state.pending.version == languageConfigVersion {
		return TurnSpeechBinding{}, nil, ErrBindingPending
	}
	if state.active == nil {
		return TurnSpeechBinding{}, nil, ErrBindingNotPrepared
	}
	if state.active.binding.LanguageConfigVersion != languageConfigVersion {
		return TurnSpeechBinding{}, nil, fmt.Errorf(
			"%w: requested %d, active %d",
			ErrBindingVersionConflict,
			languageConfigVersion,
			state.active.binding.LanguageConfigVersion,
		)
	}

	state.leases++
	binding := cloneBinding(state.active.binding)
	var once sync.Once
	release := Release(func() {
		once.Do(func() { c.release(sessionID, state) })
	})
	return binding, release, nil
}

// CloseSession removes one session's binding state and cancels its pending
// resolver. Existing lease values remain usable because they own immutable
// provider references; a later session with the same id starts with fresh state.
func (c *BindingCoordinator) CloseSession(sessionID string) {
	if c == nil || sessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.sessions[sessionID]
	if state == nil {
		return
	}
	state.closed = true
	if state.pending != nil {
		c.finishPendingLocked(state.pending, ErrBindingSessionClosed)
		state.pending.cancel()
		state.pending = nil
	}
	delete(c.sessions, sessionID)
}

func (c *BindingCoordinator) installableBinding(
	sessionID string,
	version int64,
	languageKey string,
	route SpeechRoute,
	languageA string,
	languageB string,
	pending *pendingBinding,
) error {
	normalizedRoute, err := normalizeRoute(route)
	if err != nil {
		return err
	}
	routeLanguageKey, err := routeKey(normalizedRoute.LanguageA, normalizedRoute.LanguageB)
	if err != nil {
		return err
	}
	if routeLanguageKey != languageKey {
		return fmt.Errorf("%w: requested %s and %s", ErrSpeechRouteMismatch, languageA, languageB)
	}
	if _, err := c.registry.ASR(normalizedRoute.ASRProfileID); err != nil {
		return fmt.Errorf("resolve ASR profile for version %d: %w", version, err)
	}
	if _, err := c.registry.TTS(normalizedRoute.TTSProfileID); err != nil {
		return fmt.Errorf("resolve TTS profile for version %d: %w", version, err)
	}
	if pending == nil {
		return ErrBindingNotPrepared
	}
	return nil
}

func (c *BindingCoordinator) buildBinding(sessionID string, version int64, route SpeechRoute) (TurnSpeechBinding, error) {
	route, err := normalizeRoute(route)
	if err != nil {
		return TurnSpeechBinding{}, err
	}
	asrAdapter, err := c.registry.ASR(route.ASRProfileID)
	if err != nil {
		return TurnSpeechBinding{}, err
	}
	ttsAdapter, err := c.registry.TTS(route.TTSProfileID)
	if err != nil {
		return TurnSpeechBinding{}, err
	}
	asrProfile, err := c.registry.ASRProfile(route.ASRProfileID)
	if err != nil {
		return TurnSpeechBinding{}, err
	}
	ttsProfile, err := c.registry.TTSProfile(route.TTSProfileID)
	if err != nil {
		return TurnSpeechBinding{}, err
	}
	return TurnSpeechBinding{
		SessionID: sessionID, LanguageConfigVersion: version, Route: route,
		ASRProfile: asrProfile, TTSProfile: ttsProfile, ASR: asrAdapter, TTS: ttsAdapter,
	}, nil
}

func waitForPending(ctx context.Context, coordinator *BindingCoordinator, pending *pendingBinding) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-pending.done:
		coordinator.mu.Lock()
		defer coordinator.mu.Unlock()
		return pendingResultLocked(pending)
	}
}

func pendingResultLocked(pending *pendingBinding) error {
	if pending == nil || !pending.finished {
		return nil
	}
	return pending.err
}

func (c *BindingCoordinator) finishPendingLocked(pending *pendingBinding, err error) {
	if pending == nil || pending.finished {
		return
	}
	pending.err = err
	pending.finished = true
	close(pending.done)
}

func (c *BindingCoordinator) release(sessionID string, state *sessionBindingState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state.leases > 0 {
		state.leases--
	}
}

func cloneBinding(binding TurnSpeechBinding) TurnSpeechBinding {
	clone := binding
	clone.ASRProfile = cloneProfile(binding.ASRProfile)
	clone.TTSProfile = cloneProfile(binding.TTSProfile)
	return clone
}
