package pipeline

import (
	"log/slog"
	"strings"
	"time"
)

type LatencyLogger struct {
	Logger   *slog.Logger
	Observer ProviderFailureObserver
}

// ProviderFailureObserver receives bounded provider/stage counters. It must
// not retain Turn identifiers or other high-cardinality request data.
type ProviderFailureObserver interface {
	RecordProviderFailure(stage, provider string)
}

func (l LatencyLogger) Checkpoint(stage string, turn TurnContext, since time.Time, attrs ...any) {
	if l.Logger == nil {
		return
	}
	fields := turnLogFields(stage, turn)
	if !since.IsZero() {
		fields = append(fields, "elapsed_ms", time.Since(since).Milliseconds())
	}
	fields = append(fields, attrs...)
	l.Logger.Info("realtime latency checkpoint", fields...)
}

// ProviderCheckpoint adds the configured provider only when provider selection
// is known at the composition boundary.
func (l LatencyLogger) ProviderCheckpoint(stage string, turn TurnContext, since time.Time, provider, model string, attrs ...any) {
	if provider = strings.TrimSpace(provider); provider != "" {
		attrs = append([]any{"provider", provider}, attrs...)
	}
	if model = strings.TrimSpace(model); model != "" {
		attrs = append([]any{"model", model}, attrs...)
	}
	l.Checkpoint(stage, turn, since, attrs...)
}

// ProviderFailure logs the immutable Turn snapshot at the provider boundary.
// Operation IDs are intentionally absent because a Turn does not own the
// session lifecycle or mode command operation that happened to precede it.
func (l LatencyLogger) ProviderFailure(stage string, turn TurnContext, provider, model string, err error) {
	if err == nil {
		return
	}
	provider = strings.TrimSpace(provider)
	if l.Observer != nil {
		l.Observer.RecordProviderFailure(strings.TrimSpace(stage), provider)
	}
	if l.Logger == nil {
		return
	}
	fields := turnLogFields(stage, turn)
	if provider != "" {
		fields = append(fields, "provider", provider)
	}
	if model = strings.TrimSpace(model); model != "" {
		fields = append(fields, "model", model)
	}
	fields = append(fields, "error", err)
	l.Logger.Error("realtime provider failed", fields...)
}

func turnLogFields(stage string, turn TurnContext) []any {
	fields := make([]any, 0, 14)
	if stage = strings.TrimSpace(stage); stage != "" {
		fields = append(fields, "stage", stage)
	}
	if turn.SessionID != "" {
		fields = append(fields, "session_id", turn.SessionID)
	}
	if turn.ID != "" {
		fields = append(fields, "turn_id", turn.ID)
	}
	if turn.TraceID != "" {
		fields = append(fields, "trace_id", turn.TraceID)
	}
	if turn.Mode.RuntimeInstanceID != "" {
		fields = append(fields, "runtime_instance_id", turn.Mode.RuntimeInstanceID)
	}
	if turn.Mode.Mode.Valid() {
		fields = append(fields, "mode", turn.Mode.Mode)
	}
	if turn.Mode.Generation > 0 {
		fields = append(fields, "generation", turn.Mode.Generation)
	}
	return fields
}

func observedProvider(configured, observed string) string {
	if observed = strings.TrimSpace(observed); observed != "" {
		return observed
	}
	return strings.TrimSpace(configured)
}

func observedModel(configured, observed string) string {
	if strings.TrimSpace(observed) != "" {
		return observed
	}
	return configured
}
