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
