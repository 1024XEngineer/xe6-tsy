package device

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReconnectorUsesInjectedPolicyAndReportsStates(t *testing.T) {
	attempts := 0
	var states []ConnectionState
	r := Reconnector{
		Policy: ReconnectFunc(func(attempt int, state ConnectionState) (time.Duration, bool) { return 0, attempt <= 2 }),
		Connect: func(context.Context) error {
			attempts++
			if attempts == 1 {
				return errors.New("temporary network failure")
			}
			return nil
		},
		Sleep:   func(context.Context, time.Duration) error { return nil },
		OnState: func(state ConnectionState) { states = append(states, state) },
	}
	if err := r.Run(context.Background(), ConnectionDisconnected); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(states) != 3 || states[0] != ConnectionConnecting || states[len(states)-1] != ConnectionConnected {
		t.Fatalf("attempts=%d states=%v", attempts, states)
	}
}

func TestReconnectorStopsWhenPolicyExhausts(t *testing.T) {
	r := Reconnector{Policy: ReconnectFunc(func(int, ConnectionState) (time.Duration, bool) { return 0, false }), Connect: func(context.Context) error { t.Fatal("connect called"); return nil }}
	if !errors.Is(r.Run(context.Background(), ConnectionFailed), ErrReconnectExhausted) {
		t.Fatal("expected exhausted error")
	}
}
