package delivery

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestUnconfiguredProviderFailsClosed(t *testing.T) {
	provider := UnconfiguredProvider{}
	err := provider.Send(context.Background(), SendRequest{})
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("Send() error = %v, want ErrProviderNotConfigured", err)
	}
	if errors.Is(err, ErrProviderRejected) || errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("Send() error = %v, must keep configuration failure separate", err)
	}
	if provider.SupportsProviderIdempotency() {
		t.Fatal("unconfigured provider must not claim idempotency")
	}
}

func TestUnconfiguredProviderHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (UnconfiguredProvider{}).Send(ctx, SendRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send() error = %v, want context.Canceled", err)
	}
}

func TestFakeEmailProviderDeduplicatesWithinInstanceAndDoesNotExposeTarget(t *testing.T) {
	var calls atomic.Int32
	provider := NewFakeEmailProvider(FakeEmailProviderConfig{
		SendFunc: func(context.Context, SendRequest) error {
			calls.Add(1)
			return nil
		},
	})
	request := validFakeRequest()
	if err := provider.Send(context.Background(), request); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	request.Message.Turns[0].SourceLanguage = "changed-after-send"
	if err := provider.Send(context.Background(), request); err != nil {
		t.Fatalf("duplicate Send() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("SendFunc calls = %d, want 1", got)
	}
	requests := provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("Requests() length = %d, want 1", len(requests))
	}
	if requests[0].Destination.ProviderTarget != "" {
		t.Fatal("Requests() exposed ProviderTarget")
	}
	if requests[0].Message.Turns[0].SourceLanguage != "zh-CN" {
		t.Fatalf("recorded message was not isolated: source_language = %q", requests[0].Message.Turns[0].SourceLanguage)
	}
	if provider.SupportsProviderIdempotency() {
		t.Fatal("in-memory fake provider must not claim crash-safe idempotency")
	}
}

func TestFakeEmailProviderAllowsRetryAfterFailure(t *testing.T) {
	var calls atomic.Int32
	provider := NewFakeEmailProvider(FakeEmailProviderConfig{
		SendFunc: func(context.Context, SendRequest) error {
			if calls.Add(1) == 1 {
				return errors.New("temporary provider failure")
			}
			return nil
		},
	})
	request := validFakeRequest()
	if err := provider.Send(context.Background(), request); err == nil {
		t.Fatal("first Send() succeeded, want injected failure")
	}
	if err := provider.Send(context.Background(), request); err != nil {
		t.Fatalf("retry Send() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("SendFunc calls = %d, want 2", got)
	}
	if got := len(provider.Requests()); got != 2 {
		t.Fatalf("Requests() length = %d, want 2 after failed retry", got)
	}
}

func TestFakeEmailProviderRejectsIdempotencyKeyReuse(t *testing.T) {
	provider := NewFakeEmailProvider(FakeEmailProviderConfig{})
	request := validFakeRequest()
	if err := provider.Send(context.Background(), request); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	request.Message.ID = "message-2"
	request.Attempt.MessageID = "message-2"
	err := provider.Send(context.Background(), request)
	if !errors.Is(err, ErrProviderRejected) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate key error = %v, want provider rejection and conflict", err)
	}
	if got := len(provider.Requests()); got != 1 {
		t.Fatalf("Requests() length = %d, want 1", got)
	}
}

func TestFakeEmailProviderInitializesZeroValueState(t *testing.T) {
	var provider FakeEmailProvider
	request := validFakeRequest()
	if err := provider.Send(context.Background(), request); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if got := len(provider.Requests()); got != 1 {
		t.Fatalf("Requests() length = %d, want 1", got)
	}
}

func TestFakeEmailProviderConcurrentDuplicateInvokesOnce(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	provider := NewFakeEmailProvider(FakeEmailProviderConfig{
		SendFunc: func(context.Context, SendRequest) error {
			calls.Add(1)
			close(started)
			<-release
			return nil
		},
	})
	request := validFakeRequest()
	firstDone := make(chan error, 1)
	go func() { firstDone <- provider.Send(context.Background(), request) }()
	<-started
	secondDone := make(chan error, 1)
	go func() { secondDone <- provider.Send(context.Background(), request) }()
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("duplicate Send() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("SendFunc calls = %d, want 1", got)
	}
}

func TestFakeEmailProviderRejectsMismatchedInFlightRequestIdentity(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	provider := NewFakeEmailProvider(FakeEmailProviderConfig{
		SendFunc: func(context.Context, SendRequest) error {
			close(started)
			<-release
			return nil
		},
	})
	request := validFakeRequest()
	firstDone := make(chan error, 1)
	go func() { firstDone <- provider.Send(context.Background(), request) }()
	<-started
	second := validFakeRequest()
	second.Message.ID = "message-2"
	second.Attempt.MessageID = "message-2"
	second.ProviderIdempotencyKey = request.ProviderIdempotencyKey
	second.Attempt.ID = request.Attempt.ID
	err := provider.Send(context.Background(), second)
	close(release)
	if !errors.Is(err, ErrProviderRejected) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Send() error = %v, want provider rejection and conflict", err)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
}

func TestFakeEmailProviderRejectsInvalidRequest(t *testing.T) {
	provider := NewFakeEmailProvider(FakeEmailProviderConfig{})
	request := validFakeRequest()
	request.Destination.ProviderTarget = ""
	err := provider.Send(context.Background(), request)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("Send() error = %v, want domain.ErrInvalidArgument", err)
	}
	if got := len(provider.Requests()); got != 0 {
		t.Fatalf("Requests() length = %d, want 0 for invalid request", got)
	}
}

func TestFakeEmailProviderRejectsMismatchedRequestIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SendRequest)
	}{
		{name: "caller key", mutate: func(request *SendRequest) {
			request.ProviderIdempotencyKey = "caller-key"
		}},
		{name: "attempt message", mutate: func(request *SendRequest) {
			request.Attempt.MessageID = "message-2"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := NewFakeEmailProvider(FakeEmailProviderConfig{})
			request := validFakeRequest()
			test.mutate(&request)
			if err := provider.Send(context.Background(), request); !errors.Is(err, domain.ErrInvalidArgument) {
				t.Fatalf("Send() error = %v, want domain.ErrInvalidArgument", err)
			}
			if got := len(provider.Requests()); got != 0 {
				t.Fatalf("Requests() length = %d, want 0", got)
			}
		})
	}
}

func TestFakeEmailProviderWaiterHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	provider := NewFakeEmailProvider(FakeEmailProviderConfig{
		SendFunc: func(context.Context, SendRequest) error {
			close(started)
			<-release
			return nil
		},
	})
	request := validFakeRequest()
	firstDone := make(chan error, 1)
	go func() { firstDone <- provider.Send(context.Background(), request) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := provider.Send(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting Send() error = %v, want context.Canceled", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
}

func TestWaitForFakeProviderCallReturnsSettledError(t *testing.T) {
	expected := errors.New("provider failed")
	call := &fakeProviderCall{done: make(chan struct{}), err: expected}
	close(call.done)
	if err := waitForFakeProviderCall(context.Background(), call); !errors.Is(err, expected) {
		t.Fatalf("waitForFakeProviderCall() error = %v, want settled error", err)
	}
}

func validFakeRequest() SendRequest {
	return SendRequest{
		Message: Message{
			ID: "message-1", AccountID: "account-1", Channel: ChannelEmail,
			DestinationRef: "destination-1", Turns: []FinalTurnSnapshot{{TurnID: "turn-1", SourceLanguage: "zh-CN"}},
		},
		Attempt: DeliveryAttempt{ID: "attempt-1", MessageID: "message-1"},
		Destination: VerifiedDestination{
			AccountID: "account-1", Channel: ChannelEmail, DestinationRef: "destination-1", ProviderTarget: "target@example.test",
		},
		ProviderIdempotencyKey: "attempt-1",
	}
}
