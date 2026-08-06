package runtime

import (
	"context"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/outbox"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

// Sinks groups durable downstream event publishers for one realtime runtime.
type Sinks struct {
	FinalTurns recordsv1.FinalTurnSink
	Usage      pipeline.UsageFactSink
	Outbox     *outbox.Runtime
}

// OpenSinksFromEnv opens the configured outbox and returns typed sinks for the media pipeline.
func OpenSinksFromEnv(ctx context.Context) (*Sinks, error) {
	runtime, err := outbox.OpenRuntimeFromEnv(ctx)
	if err != nil {
		return nil, err
	}
	return &Sinks{
		FinalTurns: pipeline.NewOutboxFinalTurnSink(runtime.Outbox),
		Usage:      pipeline.NewOutboxUsageFactSink(runtime.Outbox),
		Outbox:     runtime,
	}, nil
}
