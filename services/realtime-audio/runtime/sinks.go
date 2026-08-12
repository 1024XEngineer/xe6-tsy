package runtime

import (
	"context"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/outbox"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

// Sinks groups durable downstream event publishers for one realtime runtime.
type Sinks struct {
	FinalTurns  recordsv1.FinalTurnSink
	ModeChanges ModeChangedSink
	Usage       pipeline.UsageFactSink
	Outbox      *outbox.Runtime
}

// OpenSinksFromEnv opens the configured outbox and returns typed sinks for the media pipeline.
func OpenSinksFromEnv(ctx context.Context) (*Sinks, error) {
	runtime, err := outbox.OpenRuntimeFromEnv(ctx)
	if err != nil {
		return nil, err
	}
	return &Sinks{
		FinalTurns:  pipeline.NewOutboxFinalTurnSink(runtime.Outbox),
		ModeChanges: NewOutboxModeChangedSink(runtime.Outbox),
		Usage:       pipeline.NewOutboxUsageFactSink(runtime.Outbox),
		Outbox:      runtime,
	}, nil
}

type outboxModeChangedSink struct {
	outbox pipeline.DurableOutbox
}

// NewOutboxModeChangedSink publishes validated mode facts through the shared durable outbox.
func NewOutboxModeChangedSink(outbox pipeline.DurableOutbox) ModeChangedSink {
	return &outboxModeChangedSink{outbox: outbox}
}

func (s *outboxModeChangedSink) Publish(ctx context.Context, event realtimev1.ModeChangedEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if s == nil || s.outbox == nil {
		return pipeline.ErrOutboxRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.outbox.Append(ctx, realtimev1.ModeChangedTopic, event.EventID, event)
}

var _ ModeChangedSink = (*outboxModeChangedSink)(nil)
