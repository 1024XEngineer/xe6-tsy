package command

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad"
)

var (
	ErrDependenciesRequired = errors.New("command gate dependencies are required")
	ErrInvalidOptions       = errors.New("invalid command gate options")
	ErrInvalidOpenRequest   = errors.New("invalid command gate open request")
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

// Failure identifies a recoverable command path outcome. These values are observations for
// metrics or feedback; none represents a terminal media-runtime error.
type Failure string

const (
	FailureNone          Failure = ""
	FailureWindowExpired Failure = "window_expired"
	FailureNoSpeech      Failure = "no_speech"
	FailureAudioTooLong  Failure = "audio_too_long"
	FailureASR           Failure = "asr_failed"
	FailureNotAllowed    Failure = "command_not_allowed"
	FailureExecution     Failure = "execution_failed"
	FailureCanceled      Failure = "canceled"
	FailureInvalidAudio  Failure = "invalid_audio"
)

// Options bounds every command attempt. WindowTTL is a hard deadline for the complete attempt;
// NoSpeechTimeout bounds armed silence, MaxAudioDuration bounds captured PCM, and EndSilence
// deterministically finalizes a spoken command.
type Options struct {
	WindowTTL        time.Duration
	NoSpeechTimeout  time.Duration
	MaxAudioDuration time.Duration
	EndSilence       time.Duration
}

// OpenRequest carries identity owned by the existing realtime runtime. CommandID becomes the
// isolated ASR turn ID and should therefore be unique within the session.
type OpenRequest struct {
	SessionID      string
	CommandID      string
	SourceLanguage string
	OpenedAt       time.Time
}

// ExecuteRequest asks the runtime adapter to apply one already allowlisted mode intent.
type ExecuteRequest struct {
	SessionID string
	CommandID string
	Command   Command
}

// Executor is deliberately narrower than runtime.Manager, keeping mode-generation and operation
// construction in the runtime adapter instead of leaking those details into command recognition.
type Executor interface {
	ExecuteCommand(context.Context, ExecuteRequest) error
}

// Dependencies use a command-specific VAD instance and the existing ASR provider boundary.
// The classifier must not be shared with ordinary Turn segmentation because its internal rolling
// context, if any, belongs to a different input lifecycle.
type Dependencies struct {
	Classifier vad.Classifier
	ASR        asr.Provider
	Executor   Executor
}

// Result tells the caller whether the frame is quarantined from ordinary handlers. Failure is
// populated for recoverable command outcomes; Executed is populated only after executor success.
type Result struct {
	Consumed bool
	State    State
	Failure  Failure
	Executed *Command
}

// Gate owns one command attempt at a time. Open and Consume are synchronized because wake-word
// signals and WebRTC audio may arrive on different callbacks.
type Gate struct {
	mu          sync.Mutex
	deps        Dependencies
	options     Options
	state       State
	request     OpenRequest
	lastFrame   time.Time
	lastSpeech  time.Time
	audioLength time.Duration
	stream      asr.Stream
}

// NewGate returns a dormant, immediately usable gate with explicit hard bounds.
func NewGate(deps Dependencies, options Options) (*Gate, error) {
	if deps.Classifier == nil || deps.ASR == nil || deps.Executor == nil {
		return nil, ErrDependenciesRequired
	}
	if options.WindowTTL <= 0 || options.NoSpeechTimeout <= 0 || options.MaxAudioDuration <= 0 ||
		options.EndSilence <= 0 || options.NoSpeechTimeout > options.WindowTTL ||
		options.MaxAudioDuration > options.WindowTTL || options.EndSilence >= options.MaxAudioDuration {
		return nil, ErrInvalidOptions
	}
	return &Gate{deps: deps, options: options, state: StateDormant}, nil
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

// Open arms a fresh bounded window. Reopening abandons any incomplete command stream, which makes
// repeated hardware wake signals deterministic and prevents audio from separate wake cycles from
// being combined into one executable command.
func (g *Gate) Open(request OpenRequest) error {
	if g == nil {
		return ErrDependenciesRequired
	}
	if request.SessionID == "" || request.CommandID == "" || request.OpenedAt.IsZero() {
		return ErrInvalidOpenRequest
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.abandonLocked()
	g.request = request
	g.state = StateArmed
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
	if g.state == StateDormant {
		return Result{State: StateDormant}
	}
	if err := ctx.Err(); err != nil {
		return g.failLocked(FailureCanceled)
	}
	if !validFrame(frame) || (!g.lastFrame.IsZero() && !frame.CapturedAt.After(g.lastFrame)) {
		return g.failLocked(FailureInvalidAudio)
	}
	// Frames captured before the local wake decision belong to the ordinary
	// microphone timeline. Quarantine them without starting Command ASR so a
	// delayed media packet cannot become the spoken command.
	if frame.CapturedAt.Before(g.request.OpenedAt) {
		return Result{Consumed: true, State: StateArmed}
	}
	g.lastFrame = frame.CapturedAt

	if !frame.CapturedAt.Before(g.request.OpenedAt.Add(g.options.WindowTTL)) {
		return g.failLocked(FailureWindowExpired)
	}
	if g.state == StateArmed && !frame.CapturedAt.Before(g.request.OpenedAt.Add(g.options.NoSpeechTimeout)) {
		return g.failLocked(FailureNoSpeech)
	}

	isSpeech := g.deps.Classifier.Speech(frame)
	if g.state == StateArmed {
		if !isSpeech {
			return Result{Consumed: true, State: StateArmed}
		}
		if failure := g.startCaptureLocked(ctx); failure != FailureNone {
			return g.failLocked(failure)
		}
		g.lastSpeech = frame.CapturedAt
	}

	frameDuration := pcmDuration(frame)
	if g.audioLength+frameDuration > g.options.MaxAudioDuration {
		return g.failLocked(FailureAudioTooLong)
	}
	if err := g.stream.PushAudio(ctx, frame.PCM); err != nil {
		return g.failLocked(classifyContextFailure(ctx, FailureASR))
	}
	g.audioLength += frameDuration
	if isSpeech {
		g.lastSpeech = frame.CapturedAt
		return Result{Consumed: true, State: StateCapturing}
	}
	if frame.CapturedAt.Sub(g.lastSpeech) < g.options.EndSilence {
		return Result{Consumed: true, State: StateCapturing}
	}
	return g.recognizeLocked(ctx)
}

func (g *Gate) startCaptureLocked(ctx context.Context) Failure {
	stream, err := g.deps.ASR.StartStream(ctx, asr.StreamRequest{
		SessionID: g.request.SessionID, TurnID: g.request.CommandID, SourceLanguage: g.request.SourceLanguage,
	})
	if err != nil || stream == nil {
		return classifyContextFailure(ctx, FailureASR)
	}
	g.stream = stream
	g.state = StateCapturing
	return FailureNone
}

func (g *Gate) recognizeLocked(ctx context.Context) Result {
	g.state = StateRecognizing
	result, err := g.stream.Finish(ctx)
	if err != nil {
		return g.failLocked(classifyContextFailure(ctx, FailureASR))
	}
	parsed, err := Parse(result.Text)
	if err != nil {
		return g.failLocked(FailureNotAllowed)
	}
	request := ExecuteRequest{SessionID: g.request.SessionID, CommandID: g.request.CommandID, Command: parsed}
	if err := g.deps.Executor.ExecuteCommand(ctx, request); err != nil {
		return g.failLocked(classifyContextFailure(ctx, FailureExecution))
	}
	g.abandonLocked()
	executed := parsed
	return Result{Consumed: true, State: StateDormant, Executed: &executed}
}

func (g *Gate) failLocked(failure Failure) Result {
	g.abandonLocked()
	return Result{Consumed: true, State: StateDormant, Failure: failure}
}

func (g *Gate) abandonLocked() {
	if g.stream != nil {
		_ = g.stream.Close()
	}
	g.state = StateDormant
	g.request = OpenRequest{}
	g.lastFrame = time.Time{}
	g.lastSpeech = time.Time{}
	g.audioLength = 0
	g.stream = nil
}

func validFrame(frame audio.Frame) bool {
	return len(frame.PCM) > 0 && len(frame.PCM)%2 == 0 && frame.SampleRate == audio.SupportedSampleRate &&
		!frame.CapturedAt.IsZero()
}

func pcmDuration(frame audio.Frame) time.Duration {
	samples := len(frame.PCM) / 2
	return time.Duration(samples) * time.Second / time.Duration(frame.SampleRate)
}

func classifyContextFailure(ctx context.Context, fallback Failure) Failure {
	if ctx.Err() != nil {
		return FailureCanceled
	}
	return fallback
}
