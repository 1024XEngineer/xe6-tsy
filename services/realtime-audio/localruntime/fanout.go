package localruntime

import (
	"context"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// FanoutFinalTurnSink publishes to a durable sink (Postgres outbox) and a
// best-effort live sink (DataChannel) for the browser demo.
type FanoutFinalTurnSink struct {
	Durable recordsv1.FinalTurnSink
	Live    recordsv1.FinalTurnSink
}

func (s FanoutFinalTurnSink) Publish(ctx context.Context, event recordsv1.FinalTurnEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Live != nil {
		_ = s.Live.Publish(ctx, event)
	}
	if s.Durable != nil {
		return s.Durable.Publish(ctx, event)
	}
	return nil
}
