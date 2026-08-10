// Package ingress owns the internal audio handoff between media capture and
// downstream interpretation capabilities.
package ingress

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

var (
	ErrDependencyRequired     = errors.New("audio ingress dependency is required")
	ErrInvalidCommandCapture  = errors.New("audio ingress command capture id is invalid")
	ErrInvalidCommandWindow   = errors.New("audio ingress command window is invalid")
	ErrCommandCaptureActive   = errors.New("audio ingress command capture is already active")
	ErrCommandCaptureNotFound = errors.New("audio ingress command capture is not active")
)

const (
	DefaultCommandWindow = 10 * time.Second
	MaxCommandWindow     = 10 * time.Second
)

// Mode controls which downstream capability receives finalized audio.
type Mode string

const (
	ModeTranslation    Mode = "translation"
	ModeCommandCapture Mode = "command_capture"
)

// CommandWindow is the immutable state returned when command capture is armed.
type CommandWindow struct {
	Mode       Mode
	CaptureID  string
	StartedAt  time.Time
	Deadline   time.Time
	Generation uint64
}

// CommandCapture is the raw audio snapshot handed to the future ASR/intent
// adapter. It is intentionally independent of the translation Turn contract.
type CommandCapture struct {
	CaptureID      string
	SessionID      string
	AccountID      string
	TraceID        string
	SourceLanguage string
	StartedAt      time.Time
	EndedAt        time.Time
	AudioChunks    [][]byte
}

// TranslationHandler is the existing ASR-to-translation boundary.
type TranslationHandler interface {
	ProcessAudio(context.Context, pipeline.TurnProcessRequest) (pipeline.TurnContext, error)
}

// CommandSink is the next-stage boundary. The current phase only captures
// audio; ASR text and LLM intent classification are implemented later.
type CommandSink interface {
	PublishCommand(context.Context, CommandCapture) error
}

// DiscardCommandSink is an explicit development adapter used until the intent
// classifier is wired. It still prevents command audio from entering translation.
type DiscardCommandSink struct{}

func (DiscardCommandSink) PublishCommand(context.Context, CommandCapture) error { return nil }

type DispatcherDependencies struct {
	Translation TranslationHandler
	Commands    CommandSink
	Now         func() time.Time
}

// Dispatcher routes one session's VAD-finalized audio without adding a new
// process. It also implements the segment.Boundary shape structurally.
type Dispatcher struct {
	mu          sync.Mutex
	translation TranslationHandler
	commands    CommandSink
	now         func() time.Time
	mode        Mode
	capture     *commandBuffer
	generation  uint64
}

type commandBuffer struct {
	window  CommandWindow
	capture CommandCapture
}

func NewDispatcher(deps DispatcherDependencies) (*Dispatcher, error) {
	if deps.Translation == nil {
		return nil, ErrDependencyRequired
	}
	if deps.Commands == nil {
		deps.Commands = DiscardCommandSink{}
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Dispatcher{translation: deps.Translation, commands: deps.Commands, now: deps.Now, mode: ModeTranslation}, nil
}

func (d *Dispatcher) Generation() uint64 {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.generation
}

func (d *Dispatcher) BeforeFrame(ctx context.Context, capturedAt time.Time) error {
	if d == nil {
		return ErrDependencyRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.capture == nil || capturedAt.Before(d.capture.window.Deadline) {
		d.mu.Unlock()
		return nil
	}
	capture := d.takeCaptureLocked(d.capture.window.Deadline)
	d.mu.Unlock()
	return d.publish(ctx, capture)
}

func (d *Dispatcher) ArmCommandCapture(captureID string, duration time.Duration) (CommandWindow, error) {
	if d == nil {
		return CommandWindow{}, ErrDependencyRequired
	}
	if captureID == "" {
		return CommandWindow{}, ErrInvalidCommandCapture
	}
	if duration <= 0 {
		duration = DefaultCommandWindow
	}
	if duration > MaxCommandWindow {
		return CommandWindow{}, ErrInvalidCommandWindow
	}
	now := d.now().UTC()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.capture != nil {
		return CommandWindow{}, ErrCommandCaptureActive
	}
	d.generation++
	window := CommandWindow{Mode: ModeCommandCapture, CaptureID: captureID, StartedAt: now, Deadline: now.Add(duration), Generation: d.generation}
	d.capture = &commandBuffer{window: window}
	return window, nil
}

func (d *Dispatcher) ProcessAudio(ctx context.Context, request pipeline.TurnProcessRequest) (pipeline.TurnContext, error) {
	if d == nil || d.translation == nil {
		return pipeline.TurnContext{}, ErrDependencyRequired
	}
	if err := ctx.Err(); err != nil {
		return pipeline.TurnContext{}, err
	}
	d.mu.Lock()
	if request.Generation != 0 && request.Generation != d.generation {
		d.mu.Unlock()
		return pipeline.TurnContext{}, nil
	}
	if d.capture != nil {
		if request.Generation != 0 && request.Generation != d.capture.window.Generation {
			d.mu.Unlock()
			return pipeline.TurnContext{}, nil
		}
		d.appendLocked(request)
		d.mu.Unlock()
		return pipeline.TurnContext{}, nil
	}
	d.mu.Unlock()
	return d.translation.ProcessAudio(ctx, request)
}

func (d *Dispatcher) CloseCommandCapture(ctx context.Context, captureID string) error {
	if d == nil {
		return ErrDependencyRequired
	}
	d.mu.Lock()
	if d.capture == nil {
		d.mu.Unlock()
		return ErrCommandCaptureNotFound
	}
	if captureID != "" && captureID != d.capture.window.CaptureID {
		d.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrInvalidCommandCapture, captureID)
	}
	capture := d.takeCaptureLocked(d.now().UTC())
	d.mu.Unlock()
	return d.publish(ctx, capture)
}

func (d *Dispatcher) CancelCommandCapture(captureID string) error {
	if d == nil {
		return ErrDependencyRequired
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.capture == nil {
		return ErrCommandCaptureNotFound
	}
	if captureID != "" && captureID != d.capture.window.CaptureID {
		return fmt.Errorf("%w: %s", ErrInvalidCommandCapture, captureID)
	}
	d.capture = nil
	d.generation++
	return nil
}

func (d *Dispatcher) appendLocked(request pipeline.TurnProcessRequest) {
	if d.capture.capture.SessionID == "" {
		d.capture.capture.SessionID = request.SessionID
		d.capture.capture.AccountID = request.AccountID
		d.capture.capture.TraceID = request.TraceID
		d.capture.capture.SourceLanguage = request.SourceLanguage
		d.capture.capture.StartedAt = request.StartedAt
	}
	if d.capture.capture.StartedAt.IsZero() || (!request.StartedAt.IsZero() && request.StartedAt.Before(d.capture.capture.StartedAt)) {
		d.capture.capture.StartedAt = request.StartedAt
	}
	for _, chunk := range request.AudioChunks {
		d.capture.capture.AudioChunks = append(d.capture.capture.AudioChunks, append([]byte(nil), chunk...))
	}
	if !request.StartedAt.IsZero() {
		d.capture.capture.EndedAt = request.StartedAt
	}
}

func (d *Dispatcher) takeCaptureLocked(endedAt time.Time) CommandCapture {
	buffer := d.capture
	d.capture = nil
	d.generation++
	buffer.capture.CaptureID = buffer.window.CaptureID
	if buffer.capture.StartedAt.IsZero() {
		buffer.capture.StartedAt = buffer.window.StartedAt
	}
	if endedAt.IsZero() || endedAt.Before(buffer.capture.StartedAt) {
		endedAt = buffer.window.Deadline
	}
	buffer.capture.EndedAt = endedAt
	return buffer.capture
}

func (d *Dispatcher) publish(ctx context.Context, capture CommandCapture) error {
	if len(capture.AudioChunks) == 0 {
		return nil
	}
	return d.commands.PublishCommand(ctx, capture)
}
