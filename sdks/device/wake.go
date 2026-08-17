package device

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrWakeWordSignalUnavailable = errors.New("device wake-word signal unavailable")

// WakeWordEvent is emitted by a platform-local engine. The engine never
// decides a business mode; it only reports that the fixed local wake word fired.
type WakeWordEvent struct {
	DetectedAt time.Time
}

// WakeWordEngine is implemented by a chip/OS-specific local wake-word stack.
type WakeWordEngine interface {
	Start(context.Context, func(WakeWordEvent)) error
	Stop() error
}

// WakeWordSignalSender writes the shared wake_word.detected contract to the
// reliable ordered DataChannel owned by the platform WebRTC adapter. Send must
// not create or close a PeerConnection and must not stop microphone uplink.
type WakeWordSignalSender interface {
	SendWakeWordDetected(context.Context, WakeWordDetectedSignal) error
}

// WakeCommandController bridges a platform-local KWS callback to the shared
// server signal. The backend owns command capture and semantic decisions; this
// controller never opens a local command window or interprets business intent.
type WakeCommandController struct {
	Engine WakeWordEngine
	Sender WakeWordSignalSender

	mu          sync.Mutex
	enabled     bool
	started     bool
	epoch       uint64
	runCtx      context.Context
	cancel      context.CancelFunc
	lastErr     error
	now         func() time.Time
	newSignalID func() (string, error)
	callbacks   sync.WaitGroup
}

func NewWakeCommandController(engine WakeWordEngine, sender WakeWordSignalSender) *WakeCommandController {
	return &WakeCommandController{
		Engine: engine, Sender: sender, enabled: engine != nil && sender != nil,
		now: func() time.Time { return time.Now().UTC() }, newSignalID: newWakeWordSignalID,
	}
}

func (c *WakeCommandController) Start(ctx context.Context) error {
	if c == nil || !c.Enabled() {
		return nil
	}
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.started = true
	c.epoch++
	epoch := c.epoch
	c.runCtx = runCtx
	c.cancel = cancel
	c.mu.Unlock()
	if err := c.Engine.Start(runCtx, func(event WakeWordEvent) { c.handleWake(epoch, event) }); err != nil {
		cancel()
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
	cancel := c.cancel
	c.cancel = nil
	c.runCtx = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	// Engine shutdown stays outside the lifecycle lock because platform engines
	// may wait for an in-flight callback. The callback has already been fenced.
	var stopErr error
	if c.Engine != nil {
		stopErr = c.Engine.Stop()
	}
	c.callbacks.Wait()
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

func (c *WakeCommandController) handleWake(epoch uint64, event WakeWordEvent) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if !c.enabled || !c.started || c.epoch != epoch {
		c.mu.Unlock()
		return
	}
	ctx := c.runCtx
	now := c.now
	newSignalID := c.newSignalID
	c.callbacks.Add(1)
	c.mu.Unlock()
	defer c.callbacks.Done()

	signalID, err := newSignalID()
	if err == nil {
		detectedAt := event.DetectedAt
		if detectedAt.IsZero() {
			detectedAt = now()
		}
		err = c.Sender.SendWakeWordDetected(ctx, WakeWordDetectedSignal{
			Type: WakeWordDetectedType, EventVersion: WakeWordDetectedEventVersion,
			SignalID: signalID, DetectedAt: detectedAt.UTC(),
		})
	}
	if err != nil {
		c.mu.Lock()
		if c.started && c.epoch == epoch {
			// A transient DataChannel failure must not disable KWS or ordinary audio;
			// the next physical wake produces a fresh signal and retries naturally.
			c.lastErr = errors.Join(ErrWakeWordSignalUnavailable, err)
		}
		c.mu.Unlock()
	}
}

func newWakeWordSignalID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("create wake-word signal id: %w", err)
	}
	return "wake-" + hex.EncodeToString(random[:]), nil
}

func (c *WakeCommandController) disable(err error) {
	c.mu.Lock()
	c.enabled = false
	c.started = false
	c.epoch++
	c.lastErr = err
	c.mu.Unlock()
}
