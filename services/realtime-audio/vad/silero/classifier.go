package silero

import (
	"sync"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

// Classifier implements vad.Classifier with Silero windowed inference and
// start/end hysteresis matching the validated silero_vad_demo behavior.
// Incomplete windows keep the previous speech decision so Segmenter silence
// timing is not reset by partial Opus frames.
type Classifier struct {
	runtime   *Runtime
	threshold float64
	negThresh float64
	inferFn   func([]float32) (float32, []float32, []float32, error)

	mu         sync.Mutex
	state      []float32
	context    []float32
	pending    []float32
	triggered  bool
	lastErr    error
	lastProb   float32
	windowRuns int
}

// Speech reports whether the supplied frame should count as speech for Segmenter.
func (c *Classifier) Speech(frame audio.Frame) bool {
	if c == nil || c.runtime == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastErr != nil {
		return false
	}

	appendPCM16LE(&c.pending, frame.PCM)
	speechInFrame := false
	for len(c.pending) >= WindowSamples {
		window := append([]float32(nil), c.pending[:WindowSamples]...)
		c.pending = c.pending[WindowSamples:]

		prob, nextState, nextContext, err := c.infer(window)
		if err != nil {
			c.lastErr = err
			c.triggered = false
			return false
		}
		c.lastProb = prob
		c.windowRuns++
		copy(c.state, nextState)
		copy(c.context, nextContext)
		if c.applyHysteresis(prob) {
			speechInFrame = true
		}
	}
	if speechInFrame {
		return true
	}
	return c.triggered
}

// Err returns the first inference failure observed by this classifier.
func (c *Classifier) Err() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// LastProbability returns the latest Silero speech probability for diagnostics/tests.
func (c *Classifier) LastProbability() float32 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastProb
}

// WindowRuns returns how many complete 512-sample windows were scored.
func (c *Classifier) WindowRuns() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.windowRuns
}

// Reset clears LSTM state and partial audio so a reused classifier starts clean.
func (c *Classifier) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.state {
		c.state[i] = 0
	}
	for i := range c.context {
		c.context[i] = 0
	}
	c.pending = c.pending[:0]
	c.triggered = false
	c.lastErr = nil
	c.lastProb = 0
	c.windowRuns = 0
}

func (c *Classifier) infer(window []float32) (float32, []float32, []float32, error) {
	if c.inferFn != nil {
		return c.inferFn(window)
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	return c.runtime.inferLocked(window, c.state, c.context)
}

func (c *Classifier) applyHysteresis(prob float32) bool {
	p := float64(prob)
	if p >= c.threshold {
		c.triggered = true
		return true
	}
	if p < c.negThresh {
		c.triggered = false
		return false
	}
	// Between thresholds keep the previous decision (sticky hysteresis).
	return c.triggered
}

func appendPCM16LE(dst *[]float32, pcm []byte) {
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		*dst = append(*dst, float32(sample)/32768.0)
	}
}
