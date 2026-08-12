package pipeline

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func TestLatencyLoggerIncludesModeDimensions(t *testing.T) {
	var output bytes.Buffer
	logger := LatencyLogger{Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	logger.Checkpoint("assistant_reply_done", TurnContext{
		ID: "turn-1", SessionID: "session-1", TraceID: "trace-1",
		Mode: TurnModeSnapshot{
			RuntimeInstanceID: "runtime-1", Mode: realtimev1.ModeAssistant, Generation: 2,
		},
	}, time.Now().Add(-time.Millisecond))

	logs := output.String()
	for _, field := range []string{
		`"stage":"assistant_reply_done"`, `"elapsed_ms":`,
		`"mode":"assistant"`, `"runtime_instance_id":"runtime-1"`, `"generation":2`,
	} {
		if !strings.Contains(logs, field) {
			t.Fatalf("latency log = %s, missing %s", logs, field)
		}
	}
}

func TestLatencyLoggerIncludesTurnCorrelation(t *testing.T) {
	var output bytes.Buffer
	logger := LatencyLogger{Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	turn := TurnContext{
		ID: "turn-1", SessionID: "session-1", TraceID: "trace-1",
		Mode: TurnModeSnapshot{
			RuntimeInstanceID: "runtime-1", Mode: realtimev1.ModeAssistant, Generation: 3,
		},
	}

	logger.ProviderFailure("assistant_llm", turn, "aliyun", errors.New("provider unavailable"))
	fields := decodeLogFields(t, output.Bytes())
	for key, want := range map[string]any{
		"session_id": "session-1", "turn_id": "turn-1", "trace_id": "trace-1",
		"runtime_instance_id": "runtime-1", "mode": "assistant", "generation": float64(3),
		"provider": "aliyun",
	} {
		if fields[key] != want {
			t.Fatalf("field %s = %#v, want %#v", key, fields[key], want)
		}
	}
}

func TestLatencyLoggerOmitsUnavailableCorrelation(t *testing.T) {
	var output bytes.Buffer
	logger := LatencyLogger{Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	logger.ProviderFailure("asr_start", TurnContext{SessionID: "session-1", ID: "turn-1"}, "", errors.New("provider unavailable"))
	fields := decodeLogFields(t, output.Bytes())
	for _, key := range []string{"trace_id", "runtime_instance_id", "mode", "generation", "provider"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("field %s unexpectedly present: %#v", key, fields[key])
		}
	}
}

func TestLatencyLoggerNotifiesFailureObserverWithoutLogger(t *testing.T) {
	observer := &recordingProviderFailureObserver{}
	LatencyLogger{Observer: observer}.ProviderFailure("asr_finish", TurnContext{}, "aliyun", errors.New("failed"))
	if observer.stage != "asr_finish" || observer.provider != "aliyun" || observer.calls != 1 {
		t.Fatalf("observer = %#v", observer)
	}
}

type recordingProviderFailureObserver struct {
	stage, provider string
	calls           int
}

func (o *recordingProviderFailureObserver) RecordProviderFailure(stage, provider string) {
	o.stage, o.provider, o.calls = stage, provider, o.calls+1
}

func decodeLogFields(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &fields); err != nil {
		t.Fatalf("decode structured log: %v (%s)", err, data)
	}
	return fields
}
