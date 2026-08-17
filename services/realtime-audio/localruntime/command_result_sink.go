package localruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	dispatch *commandResultDispatcher
}

const (
	commandResultQueueCapacity  = 32
	commandResultPublishTimeout = 2 * time.Second
	commandResultWorkerIdleTTL  = 30 * time.Second
)

type commandResultDispatcher struct {
	mu         sync.Mutex
	queues     map[string]*commandResultSessionQueue
	workerIdle time.Duration
}

type commandResultSessionQueue struct {
	events chan realtimev1.CommandResultEvent
}

// NewDataChannelCommandResultSink creates independent ordered delivery queues per session. Publish
// only enqueues, so one closed or slow DataChannel cannot block audio ingress or another session.
// Idle workers retire themselves and are recreated on the next result for that session.
func NewDataChannelCommandResultSink(media MediaLookup, failures DataChannelFailureObserver) *DataChannelCommandResultSink {
	return &DataChannelCommandResultSink{
		Media: media, Failures: failures,
		dispatch: &commandResultDispatcher{
			queues: make(map[string]*commandResultSessionQueue), workerIdle: commandResultWorkerIdleTTL,
		},
	}
}

func (s DataChannelCommandResultSink) Publish(ctx context.Context, event realtimev1.CommandResultEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if s.dispatch == nil {
		return s.publishNow(ctx, event)
	}
	s.enqueue(event)
	return nil
}

func (s DataChannelCommandResultSink) enqueue(event realtimev1.CommandResultEvent) {
	dispatch := s.dispatch
	dispatch.mu.Lock()
	queue := dispatch.queues[event.SessionID]
	if queue == nil {
		queue = &commandResultSessionQueue{events: make(chan realtimev1.CommandResultEvent, commandResultQueueCapacity)}
		dispatch.queues[event.SessionID] = queue
		go s.runSession(event.SessionID, queue)
	}
	dropped := false
	select {
	case queue.events <- event:
	default:
		dropped = true
	}
	dispatch.mu.Unlock()
	if dropped {
		s.recordFailure()
	}
}

func (s DataChannelCommandResultSink) runSession(sessionID string, queue *commandResultSessionQueue) {
	idle := time.NewTimer(s.dispatch.workerIdle)
	defer idle.Stop()
	for {
		select {
		case event := <-queue.events:
			s.publishQueued(event)
			resetCommandResultTimer(idle, s.dispatch.workerIdle)
		case <-idle.C:
			pending, retired := s.retireSessionWorker(sessionID, queue)
			if retired {
				return
			}
			if pending != nil {
				s.publishQueued(*pending)
			}
			resetCommandResultTimer(idle, s.dispatch.workerIdle)
		}
	}
}

// retireSessionWorker checks the queue while holding the same lock used by enqueue. An event that
// races with idle expiry is either drained here or observes no worker and creates a fresh one.
func (s DataChannelCommandResultSink) retireSessionWorker(
	sessionID string,
	queue *commandResultSessionQueue,
) (*realtimev1.CommandResultEvent, bool) {
	dispatch := s.dispatch
	dispatch.mu.Lock()
	defer dispatch.mu.Unlock()
	if dispatch.queues[sessionID] != queue {
		return nil, true
	}
	select {
	case event := <-queue.events:
		return &event, false
	default:
		delete(dispatch.queues, sessionID)
		return nil, true
	}
}

func (s DataChannelCommandResultSink) publishQueued(event realtimev1.CommandResultEvent) {
	publishCtx, cancel := context.WithTimeout(context.Background(), commandResultPublishTimeout)
	defer cancel()
	_ = s.publishNow(publishCtx, event)
}

func resetCommandResultTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
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
