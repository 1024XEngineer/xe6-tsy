package localruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

var (
	ErrCommandResultMediaUnavailable   = errors.New("command result media is unavailable")
	ErrCommandResultChannelUnavailable = errors.New("command result data channel is unavailable")
)

// DataChannelCommandResultSink returns typed command acknowledgements over the same ordered
// session-bound event channel. Failure affects observability only; the command is never replayed.
type DataChannelCommandResultSink struct {
	Media    MediaLookup
	Failures DataChannelFailureObserver
	queue    chan realtimev1.CommandResultEvent
}

const commandResultQueueCapacity = 32

// NewDataChannelCommandResultSink starts one process-lifetime delivery worker. Publish only
// enqueues, so a closed or slow DataChannel can neither block audio ingress nor replay execution.
func NewDataChannelCommandResultSink(media MediaLookup, failures DataChannelFailureObserver) *DataChannelCommandResultSink {
	sink := &DataChannelCommandResultSink{
		Media: media, Failures: failures,
		queue: make(chan realtimev1.CommandResultEvent, commandResultQueueCapacity),
	}
	go sink.run()
	return sink
}

func (s DataChannelCommandResultSink) Publish(ctx context.Context, event realtimev1.CommandResultEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if s.queue != nil {
		select {
		case s.queue <- event:
			return nil
		default:
			s.recordFailure()
			return nil
		}
	}
	return s.publishNow(ctx, event)
}

func (s *DataChannelCommandResultSink) run() {
	for event := range s.queue {
		publishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.publishNow(publishCtx, event)
		cancel()
	}
}

func (s DataChannelCommandResultSink) publishNow(ctx context.Context, event realtimev1.CommandResultEvent) error {
	if s.Media == nil {
		s.recordFailure()
		return ErrCommandResultMediaUnavailable
	}
	media, err := s.Media.CurrentMedia(ctx, event.SessionID)
	if err != nil {
		s.recordFailure()
		return fmt.Errorf("resolve command result media: %w", err)
	}
	if media == nil || media.TranslationEvents() == nil {
		s.recordFailure()
		return ErrCommandResultChannelUnavailable
	}
	if err := media.TranslationEvents().PublishJSON(ctx, event); err != nil {
		s.recordFailure()
		if errors.Is(err, webrtc.ErrMediaUnavailable) {
			return errors.Join(ErrCommandResultChannelUnavailable, fmt.Errorf("publish command result: %w", err))
		}
		return fmt.Errorf("publish command result: %w", err)
	}
	return nil
}

func (s DataChannelCommandResultSink) recordFailure() {
	if s.Failures != nil {
		s.Failures.RecordDataChannelFailure()
	}
}

var _ command.ResultSink = (*DataChannelCommandResultSink)(nil)
