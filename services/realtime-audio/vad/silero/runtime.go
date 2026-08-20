package silero

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ort "github.com/getcharzp/onnxruntime_purego"
)

const (
	// WindowSamples is the Silero VAD v5 audio hop at 16 kHz (~32 ms).
	WindowSamples = 512
	// contextSamples is the rolling left-context Silero concatenates before each window.
	contextSamples   = 64
	inputSamples     = contextSamples + WindowSamples
	stateSize        = 2 * 1 * 128
	defaultThreshold = 0.5
)

var (
	ErrLibraryRequired = errors.New("onnxruntime shared library path is required")
	ErrModelRequired   = errors.New("silero VAD model path is required")
	ErrRuntimeClosed   = errors.New("silero VAD runtime is closed")
)

// RuntimeConfig wires the ONNX Runtime shared library and Silero model file.
type RuntimeConfig struct {
	LibraryPath string
	ModelPath   string
	// Threshold is the speech-start probability. Zero defaults to 0.5.
	Threshold float64
	// NegThreshold is the speech-end probability. Zero defaults to Threshold-0.15.
	NegThreshold   float64
	IntraOpThreads int32
}

// Runtime owns one process-wide ONNX session shared by per-session classifiers.
// Inference is serialized; each Classifier keeps its own LSTM state tensors.
type Runtime struct {
	mu        sync.Mutex
	engine    *ort.Engine
	session   *ort.Session
	threshold float64
	negThresh float64
	closed    bool
}

// NewRuntime loads ONNX Runtime and the Silero model. Call Close when shutting down.
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	libraryPath := filepath.Clean(cfg.LibraryPath)
	modelPath := filepath.Clean(cfg.ModelPath)
	if libraryPath == "" || libraryPath == "." {
		return nil, ErrLibraryRequired
	}
	if modelPath == "" || modelPath == "." {
		return nil, ErrModelRequired
	}
	if _, err := os.Stat(libraryPath); err != nil {
		return nil, fmt.Errorf("onnxruntime library %q: %w", libraryPath, err)
	}
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("silero model %q: %w", modelPath, err)
	}

	threshold, neg, err := normalizeThresholds(cfg.Threshold, cfg.NegThreshold)
	if err != nil {
		return nil, err
	}

	engine, err := ort.NewEngine(libraryPath)
	if err != nil {
		return nil, fmt.Errorf("init onnxruntime: %w", err)
	}
	opts, err := engine.NewSessionOptions()
	if err != nil {
		engine.Destroy()
		return nil, fmt.Errorf("create session options: %w", err)
	}
	threads := cfg.IntraOpThreads
	if threads <= 0 {
		threads = 1
	}
	if err := opts.SetIntraOpNumThreads(threads); err != nil {
		opts.Destroy()
		engine.Destroy()
		return nil, fmt.Errorf("set intra-op threads: %w", err)
	}
	session, err := engine.NewSession(modelPath, opts)
	opts.Destroy()
	if err != nil {
		engine.Destroy()
		return nil, fmt.Errorf("load silero model: %w", err)
	}

	rt := &Runtime{
		engine:    engine,
		session:   session,
		threshold: threshold,
		negThresh: neg,
	}
	runtime.SetFinalizer(rt, func(r *Runtime) { _ = r.Close() })
	return rt, nil
}

func normalizeThresholds(threshold, neg float64) (float64, float64, error) {
	if threshold <= 0 {
		threshold = defaultThreshold
	}
	if neg <= 0 {
		neg = threshold - 0.15
		if neg < 0.01 {
			neg = 0.01
		}
	}
	if neg > threshold {
		return 0, 0, fmt.Errorf("silero NegThreshold (%v) must be <= Threshold (%v)", neg, threshold)
	}
	return threshold, neg, nil
}

// NewClassifier returns an isolated speech classifier for one realtime session.
func (r *Runtime) NewClassifier() (*Classifier, error) {
	if r == nil {
		return nil, ErrRuntimeClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.session == nil {
		return nil, ErrRuntimeClosed
	}
	return &Classifier{
		runtime:   r,
		threshold: r.threshold,
		negThresh: r.negThresh,
		state:     make([]float32, stateSize),
		context:   make([]float32, contextSamples),
		pending:   make([]float32, 0, WindowSamples),
	}, nil
}

// Close releases the ONNX session and engine.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.session != nil {
		r.session.Destroy()
		r.session = nil
	}
	if r.engine != nil {
		r.engine.Destroy()
		r.engine = nil
	}
	runtime.SetFinalizer(r, nil)
	return nil
}

func (r *Runtime) inferLocked(window, state, context []float32) (float32, []float32, []float32, error) {
	if r.closed || r.session == nil {
		return 0, nil, nil, ErrRuntimeClosed
	}
	if len(window) != WindowSamples {
		return 0, nil, nil, fmt.Errorf("silero window must contain %d samples, got %d", WindowSamples, len(window))
	}
	if len(state) != stateSize {
		return 0, nil, nil, fmt.Errorf("silero state must contain %d values, got %d", stateSize, len(state))
	}
	if len(context) != contextSamples {
		return 0, nil, nil, fmt.Errorf("silero context must contain %d values, got %d", contextSamples, len(context))
	}

	// Keep backing arrays alive across Run; CreateTensorWithDataAsOrtValue borrows pointers.
	// Silero v5 expects [context|window] = 64+512 samples at 16 kHz.
	inputData := make([]float32, 0, inputSamples)
	inputData = append(inputData, context...)
	inputData = append(inputData, window...)
	stateData := append([]float32(nil), state...)
	srData := []int64{16000}

	inputTensor, err := ort.NewTensor([]int64{1, inputSamples}, inputData)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("create input tensor: %w", err)
	}
	defer inputTensor.Destroy()

	stateTensor, err := ort.NewTensor([]int64{2, 1, 128}, stateData)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("create state tensor: %w", err)
	}
	defer stateTensor.Destroy()

	srTensor, err := ort.NewTensor([]int64{}, srData)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("create sr tensor: %w", err)
	}
	defer srTensor.Destroy()

	outputs, err := r.session.Run(map[string]*ort.Value{
		"input": inputTensor,
		"state": stateTensor,
		"sr":    srTensor,
	})
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() {
		for _, value := range outputs {
			value.Destroy()
		}
	}()

	probOut, ok := outputs["output"]
	if !ok {
		return 0, nil, nil, errors.New("silero output tensor missing")
	}
	probs, err := ort.GetTensorData[float32](probOut)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read speech probability: %w", err)
	}
	if len(probs) == 0 {
		return 0, nil, nil, errors.New("silero output probability is empty")
	}

	stateOut, ok := outputs["stateN"]
	if !ok {
		return 0, nil, nil, errors.New("silero stateN tensor missing")
	}
	nextState, err := ort.GetTensorData[float32](stateOut)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read silero state: %w", err)
	}
	if len(nextState) != stateSize {
		return 0, nil, nil, fmt.Errorf("silero stateN size = %d, want %d", len(nextState), stateSize)
	}
	nextContext := append([]float32(nil), inputData[len(inputData)-contextSamples:]...)
	return probs[0], append([]float32(nil), nextState...), nextContext, nil
}
