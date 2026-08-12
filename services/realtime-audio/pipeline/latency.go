package pipeline

import (
	"log/slog"
	"time"
)

type LatencyLogger struct {
	Logger *slog.Logger
}

func (l LatencyLogger) Checkpoint(stage string, turn TurnContext, since time.Time, attrs ...any) {
	if l.Logger == nil {
		return
	}
	fields := []any{
		"stage", stage,
		"session_id", turn.SessionID,
		"turn_id", turn.ID,
		"trace_id", turn.TraceID,
		"mode", turn.Mode.Mode,
		"runtime_instance_id", turn.Mode.RuntimeInstanceID,
		"generation", turn.Mode.Generation,
	}
	if !since.IsZero() {
		fields = append(fields, "elapsed_ms", time.Since(since).Milliseconds())
	}
	fields = append(fields, attrs...)
	l.Logger.Info("realtime latency checkpoint", fields...)
}
