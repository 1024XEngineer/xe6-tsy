package languagesv1

import "testing"

func TestInterpretationOutputModeValid(t *testing.T) {
	tests := []struct {
		name string
		mode InterpretationOutputMode
		want bool
	}{
		{name: "bidirectional", mode: InterpretationOutputModeBidirectional, want: true},
		{name: "single", mode: InterpretationOutputModeSingle, want: true},
		{name: "unknown", mode: InterpretationOutputMode("unknown"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mode.Valid(); got != tt.want {
				t.Fatalf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}
