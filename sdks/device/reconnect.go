package device

import (
	"context"
	"errors"
	"time"
)

var ErrReconnectExhausted = errors.New("device reconnect attempts exhausted")

// ReconnectPolicy decides whether and when a disconnected PeerConnection may
// be recreated. It is deliberately injected because embedded devices have
// different battery, network, and backoff constraints.
type ReconnectPolicy interface {
	Delay(attempt int, state ConnectionState) (time.Duration, bool)
}

// ReconnectFunc adapts a policy function.
type ReconnectFunc func(attempt int, state ConnectionState) (time.Duration, bool)

func (f ReconnectFunc) Delay(attempt int, state ConnectionState) (time.Duration, bool) {
	if f == nil {
		return 0, false
	}
	return f(attempt, state)
}

// Reconnector coordinates retry timing without knowing how a platform creates
// a WebRTC offer. Connect must perform one complete platform reconnect.
type Reconnector struct {
	Policy  ReconnectPolicy
	Connect func(context.Context) error
	Sleep   func(context.Context, time.Duration) error
	OnState func(ConnectionState)
}

func (r Reconnector) Run(ctx context.Context, state ConnectionState) error {
	if state == ConnectionConnected {
		return nil
	}
	if r.Policy == nil || r.Connect == nil {
		return ErrReconnectExhausted
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	for attempt := 1; ; attempt++ {
		delay, ok := r.Policy.Delay(attempt, state)
		if !ok {
			return ErrReconnectExhausted
		}
		if err := sleep(ctx, delay); err != nil {
			return err
		}
		if r.OnState != nil {
			r.OnState(ConnectionConnecting)
		}
		if err := r.Connect(ctx); err == nil {
			if r.OnState != nil {
				r.OnState(ConnectionConnected)
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
