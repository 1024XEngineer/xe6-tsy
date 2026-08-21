package silero

import (
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

func TestClassifierHysteresisStateTransitions(t *testing.T) {
	tests := []struct {
		name          string
		triggered     bool
		probability   float32
		wantSpeech    bool
		wantTriggered bool
	}{
		{name: "starts at threshold", probability: 0.5, wantSpeech: true, wantTriggered: true},
		{name: "holds between thresholds", triggered: true, probability: 0.4, wantSpeech: true, wantTriggered: true},
		{name: "ends below negative threshold", triggered: true, probability: 0.34, wantTriggered: false},
		{name: "stays inactive between thresholds", probability: 0.4, wantTriggered: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classifier := &Classifier{threshold: 0.5, negThresh: 0.35, triggered: tt.triggered}
			if got := classifier.applyHysteresis(tt.probability); got != tt.wantSpeech {
				t.Fatalf("applyHysteresis() = %v, want %v", got, tt.wantSpeech)
			}
			if classifier.triggered != tt.wantTriggered {
				t.Fatalf("triggered = %v, want %v", classifier.triggered, tt.wantTriggered)
			}
		})
	}
}

func TestClassifierInferenceFailureClearsTriggeredState(t *testing.T) {
	classifier := &Classifier{
		runtime:   &Runtime{closed: true},
		threshold: 0.5,
		negThresh: 0.35,
		state:     make([]float32, stateSize),
		context:   make([]float32, contextSamples),
		triggered: true,
	}
	frame, err := audio.NewFrame(make([]byte, WindowSamples*2), audio.SupportedSampleRate, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	if classifier.Speech(frame) {
		t.Fatal("inference failure should not report speech")
	}
	if !errors.Is(classifier.Err(), ErrRuntimeClosed) {
		t.Fatalf("Err() = %v, want %v", classifier.Err(), ErrRuntimeClosed)
	}
	if classifier.triggered {
		t.Fatal("inference failure should clear triggered state")
	}
}

func TestClassifierSpeechSuccessPath(t *testing.T) {
	nextState := make([]float32, stateSize)
	nextState[0] = 0.25
	nextContext := make([]float32, contextSamples)
	nextContext[0] = 0.5
	calls := 0
	classifier := &Classifier{
		runtime:   &Runtime{},
		threshold: 0.5,
		negThresh: 0.35,
		state:     make([]float32, stateSize),
		context:   make([]float32, contextSamples),
		inferFn: func(window []float32) (float32, []float32, []float32, error) {
			calls++
			if len(window) != WindowSamples {
				t.Fatalf("inference window length = %d, want %d", len(window), WindowSamples)
			}
			return 0.8, nextState, nextContext, nil
		},
	}
	frame, err := audio.NewFrame(make([]byte, WindowSamples*2), audio.SupportedSampleRate, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	if !classifier.Speech(frame) {
		t.Fatal("successful speech inference should report speech")
	}
	if calls != 1 || classifier.WindowRuns() != 1 {
		t.Fatalf("inference calls = %d, window runs = %d, want 1, 1", calls, classifier.WindowRuns())
	}
	if classifier.LastProbability() != 0.8 || !classifier.triggered {
		t.Fatalf("classifier state = probability %v, triggered %v", classifier.LastProbability(), classifier.triggered)
	}
	if classifier.state[0] != nextState[0] || classifier.context[0] != nextContext[0] {
		t.Fatalf("inference state was not copied: state[0]=%v context[0]=%v", classifier.state[0], classifier.context[0])
	}
}
