package pipeline

import (
	"bytes"
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
