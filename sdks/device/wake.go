package device

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrCommandWindowUnavailable = errors.New("device command window unavailable")

// WakeWordEvent is emitted by a platform-local engine. The engine never
// decides a business mode; it only requests a bounded command window.
type WakeWordEvent struct {
	Phrase     string
	DetectedAt time.Time
}

// WakeWordEngine is implemented by a chip/OS-specific local wake-word stack.
type WakeWordEngine interface {
	Start(context.Context, func(WakeWordEvent)) error
	Stop() error
}

// CommandWindow is the boundary to the future server-side Command Gate. This
// stage only models its lifecycle and does not send audio or parse commands.
type CommandWindow interface {
	Open(context.Context, time.Duration) error
	Close(context.Context) error
	Active() bool
}

// WakeCommandController makes wake-word failures fail open: normal microphone
// audio and the legacy interpretation flow remain available when local KWS or
// command-window support is unavailable.
type WakeCommandController struct {
	Engine WakeWordEngine
	Window CommandWindow

	mu      sync.Mutex
	enabled bool
	started bool
	epoch   uint64
	lastErr error
}

func NewWakeCommandController(engine WakeWordEngine, window CommandWindow) *WakeCommandController {
	return &WakeCommandController{Engine: engine, Window: window, enabled: engine != nil && window != nil}
}

func (c *WakeCommandController) Start(ctx context.Context) error {
	if c == nil || !c.Enabled() {
		return nil
	}
	c.mu.Lock()
	c.started = true
	c.epoch++
	epoch := c.epoch
	c.mu.Unlock()
	if err := c.Engine.Start(ctx, func(event WakeWordEvent) { c.handleWake(epoch, event) }); err != nil {
		c.disable(err)
		return nil
	}
	return nil
}

func (c *WakeCommandController) Stop() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.started = false
	c.epoch++
	var stopErr error
	if c.Window != nil && c.Window.Active() {
		stopErr = c.Window.Close(context.Background())
	}
	c.mu.Unlock()

	// Engine shutdown stays outside the lifecycle lock because platform engines
	// may wait for an in-flight callback. The callback has already been fenced.
	if c.Engine != nil {
		stopErr = errors.Join(stopErr, c.Engine.Stop())
	}
	if stopErr != nil {
		c.disable(stopErr)
	}
	return nil
}

func (c *WakeCommandController) Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

func (c *WakeCommandController) LastError() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

func (c *WakeCommandController) handleWake(epoch uint64, _ WakeWordEvent) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled || !c.started || c.epoch != epoch {
		return
	}
	if err := c.Window.Open(context.Background(), 5*time.Second); err != nil {
		c.enabled = false
		c.started = false
		c.epoch++
		c.lastErr = errors.Join(ErrCommandWindowUnavailable, err)
	}
}

func (c *WakeCommandController) disable(err error) {
	c.mu.Lock()
	c.enabled = false
	c.started = false
	c.epoch++
	c.lastErr = err
	c.mu.Unlock()
}
