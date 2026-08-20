package silero

import (
	"errors"
	"strings"
	"testing"

	ort "github.com/getcharzp/onnxruntime_purego"
)

func TestNormalizeThresholds(t *testing.T) {
	tests := []struct {
		name          string
		threshold     float64
		neg           float64
		wantThreshold float64
		wantNeg       float64
		wantErr       string
	}{
		{name: "zero threshold uses default and fallback negative threshold", wantThreshold: 0.5, wantNeg: 0.35},
		{name: "negative threshold uses default", threshold: -1, neg: 0.2, wantThreshold: 0.5, wantNeg: 0.2},
		{name: "zero negative threshold uses threshold offset", threshold: 0.8, wantThreshold: 0.8, wantNeg: 0.65},
		{name: "negative threshold fallback is clamped", threshold: 0.1, wantThreshold: 0.1, wantNeg: 0.01},
		{name: "configured negative threshold at clamp remains unchanged", threshold: 0.8, neg: 0.01, wantThreshold: 0.8, wantNeg: 0.01},
		{name: "configured thresholds remain unchanged", threshold: 0.8, neg: 0.4, wantThreshold: 0.8, wantNeg: 0.4},
		{name: "negative threshold cannot exceed threshold", threshold: 0.2, neg: 0.5, wantErr: "must be <= Threshold"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			threshold, neg, err := normalizeThresholds(tt.threshold, tt.neg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("normalizeThresholds() error = %v, want %q", err, tt.wantErr)
				}
				if threshold != 0 || neg != 0 {
					t.Fatalf("normalizeThresholds() values = %v, %v, want zero values", threshold, neg)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeThresholds() error = %v", err)
			}
			if threshold != tt.wantThreshold || neg != tt.wantNeg {
				t.Fatalf("normalizeThresholds() = %v, %v, want %v, %v", threshold, neg, tt.wantThreshold, tt.wantNeg)
			}
		})
	}
}

func TestInferLockedRejectsInvalidInputsWithEmptyResults(t *testing.T) {
	window := make([]float32, WindowSamples)
	state := make([]float32, stateSize)
	context := make([]float32, contextSamples)
	tests := []struct {
		name    string
		runtime *Runtime
		window  []float32
		state   []float32
		context []float32
		wantErr error
		message string
	}{
		{
			name:    "closed runtime",
			runtime: &Runtime{closed: true, session: &ort.Session{}},
			window:  window,
			state:   state,
			context: context,
			wantErr: ErrRuntimeClosed,
		},
		{
			name:    "window length",
			runtime: &Runtime{session: &ort.Session{}},
			window:  window[:WindowSamples-1],
			state:   state,
			context: context,
			message: "window must contain",
		},
		{
			name:    "state length",
			runtime: &Runtime{session: &ort.Session{}},
			window:  window,
			state:   state[:stateSize-1],
			context: context,
			message: "state must contain",
		},
		{
			name:    "context length",
			runtime: &Runtime{session: &ort.Session{}},
			window:  window,
			state:   state,
			context: context[:contextSamples-1],
			message: "context must contain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probability, nextState, nextContext, err := tt.runtime.inferLocked(tt.window, tt.state, tt.context)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("inferLocked() error = %v, want %v", err, tt.wantErr)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("inferLocked() error = %v, want message containing %q", err, tt.message)
			}
			if probability != 0 || nextState != nil || nextContext != nil {
				t.Fatalf("inferLocked() values = %v, %v, %v, want 0, nil, nil", probability, nextState, nextContext)
			}
		})
	}
}
