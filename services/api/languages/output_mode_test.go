package languages

import "testing"

func TestOutputModeForRoutes(t *testing.T) {
	tests := []struct {
		name   string
		routes []OutputRoute
		want   InterpretationOutputMode
	}{
		{
			name: "bidirectional",
			routes: []OutputRoute{
				{TargetLanguage: "en-US", TTSEnabled: true},
				{TargetLanguage: "zh-CN", TTSEnabled: true},
			},
			want: InterpretationOutputModeBidirectional,
		},
		{
			name: "single",
			routes: []OutputRoute{
				{TargetLanguage: "en-US", TTSEnabled: true},
				{TargetLanguage: "zh-CN", DeliveryEnabled: true},
			},
			want: InterpretationOutputModeSingle,
		},
		{name: "incomplete", routes: []OutputRoute{{TargetLanguage: "en-US", TTSEnabled: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := outputModeForRoutes(tt.routes); got != tt.want {
				t.Fatalf("outputModeForRoutes() = %q, want %q", got, tt.want)
			}
		})
	}
}
