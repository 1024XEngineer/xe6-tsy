package silero

import (
	"strings"
	"testing"
)

func TestBuildInferenceResult(t *testing.T) {
	inputData := make([]float32, inputSamples)
	for i := range inputData {
		inputData[i] = float32(i)
	}
	state := make([]float32, stateSize)
	for i := range state {
		state[i] = float32(i) / 10
	}

	probability, nextState, nextContext, err := buildInferenceResult([]float32{0.73, 0.12}, state, inputData)
	if err != nil {
		t.Fatalf("buildInferenceResult() error = %v", err)
	}
	if probability != 0.73 {
		t.Fatalf("probability = %v, want 0.73", probability)
	}
	if len(nextState) != stateSize || nextState[0] != state[0] || nextState[stateSize-1] != state[stateSize-1] {
		t.Fatalf("nextState = len %d, first %v, last %v", len(nextState), nextState[0], nextState[len(nextState)-1])
	}
	if len(nextContext) != contextSamples || nextContext[0] != inputData[inputSamples-contextSamples] || nextContext[contextSamples-1] != inputData[inputSamples-1] {
		t.Fatalf("nextContext = len %d, first %v, last %v", len(nextContext), nextContext[0], nextContext[len(nextContext)-1])
	}

	state[0] = 99
	inputData[inputSamples-contextSamples] = 99
	if nextState[0] == 99 || nextContext[0] == 99 {
		t.Fatal("buildInferenceResult() should copy state and context")
	}
}

func TestBuildInferenceResultRejectsInvalidOutputs(t *testing.T) {
	tests := []struct {
		name    string
		probs   []float32
		state   []float32
		message string
	}{
		{name: "empty probability", state: make([]float32, stateSize), message: "probability is empty"},
		{name: "wrong state length", probs: []float32{0.4}, state: make([]float32, stateSize-1), message: "stateN size"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, nextState, nextContext, err := buildInferenceResult(tt.probs, tt.state, make([]float32, inputSamples))
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("buildInferenceResult() error = %v, want message containing %q", err, tt.message)
			}
			if nextState != nil || nextContext != nil {
				t.Fatalf("buildInferenceResult() values = %v, %v, want nil, nil", nextState, nextContext)
			}
		})
	}
}
