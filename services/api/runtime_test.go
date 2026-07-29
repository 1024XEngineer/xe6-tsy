package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/1024XEngineer/xe6-tsy/services/api/config"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
)

type runtimeTestQueue struct{}

func (runtimeTestQueue) Enqueue(context.Context, string, string) error { return nil }

func (runtimeTestQueue) Receive(ctx context.Context) (delivery.QueueMessage, error) {
	<-ctx.Done()
	return delivery.QueueMessage{}, ctx.Err()
}

func (runtimeTestQueue) Ack(context.Context, string) error { return nil }

func (runtimeTestQueue) Nack(context.Context, string, time.Time) error { return nil }

type runtimeTestOutboxRepo struct{}

func (runtimeTestOutboxRepo) ClaimOutbox(context.Context, int) ([]delivery.OutboxRecord, error) {
	return nil, nil
}

func (runtimeTestOutboxRepo) MarkOutboxPublished(context.Context, string) error { return nil }

func (runtimeTestOutboxRepo) MarkOutboxFailed(context.Context, string, string) error { return nil }

type runtimeTestDestinationReader struct{}

func (runtimeTestDestinationReader) ResolveVerifiedDestination(context.Context, string, delivery.Channel, string) (delivery.VerifiedDestination, error) {
	return delivery.VerifiedDestination{}, nil
}

type runtimeTestWorkerRepo struct{}

func (runtimeTestWorkerRepo) CreateMessage(context.Context, delivery.CreateMessageRecord) error { return nil }
func (runtimeTestWorkerRepo) GetMessage(context.Context, string, string) (delivery.Message, error) {
	return delivery.Message{}, nil
}
func (runtimeTestWorkerRepo) CreateRetry(context.Context, delivery.CreateRetryRecord) (delivery.Message, error) {
	return delivery.Message{}, nil
}
func (runtimeTestWorkerRepo) GetAttempt(context.Context, string) (delivery.DeliveryAttempt, error) {
	return delivery.DeliveryAttempt{}, nil
}
func (runtimeTestWorkerRepo) ClaimAttempt(context.Context, string) (delivery.DeliveryAttempt, error) {
	return delivery.DeliveryAttempt{}, nil
}
func (runtimeTestWorkerRepo) RequeueAttempt(context.Context, string, time.Time) error { return nil }
func (runtimeTestWorkerRepo) CompleteAttempt(context.Context, string, string, delivery.DeliveryAttemptStatus, delivery.MessageStatus, *string) error {
	return nil
}
func (runtimeTestWorkerRepo) SetMessageStatus(context.Context, string, delivery.MessageStatus, *string) error {
	return nil
}
func (runtimeTestWorkerRepo) SetAttemptStatus(context.Context, string, delivery.DeliveryAttemptStatus, *string) error {
	return nil
}
func (runtimeTestWorkerRepo) ListPreferences(context.Context, string) ([]delivery.Preference, error) {
	return nil, nil
}
func (runtimeTestWorkerRepo) PutPreference(context.Context, delivery.Preference) (delivery.Preference, error) {
	return delivery.Preference{}, nil
}
func (runtimeTestWorkerRepo) GetMessageForWorker(context.Context, string) (delivery.Message, error) {
	return delivery.Message{}, nil
}

func testConfiguredRuntimePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return reflect.New(reflect.TypeOf(pgxpool.Pool{})).Interface().(*pgxpool.Pool)
}

func newRuntimeServeFixture(t *testing.T) *configuredRuntime {
	t.Helper()
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	t.Cleanup(func() { _ = redisClient.Close() })
	queue := runtimeTestQueue{}
	return &configuredRuntime{
		pool:       testConfiguredRuntimePool(t),
		redis:      redisClient,
		dispatcher: delivery.NewOutboxDispatcher(runtimeTestOutboxRepo{}, queue, time.Second),
		worker: delivery.NewConfiguredWorker(queue, delivery.WorkerDependencies{
			Repository:   nil,
			Destinations: nil,
			Provider:     delivery.UnconfiguredProvider{},
		}),
	}
}

func newRuntimeBlockingServeFixture(t *testing.T) *configuredRuntime {
	t.Helper()
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	t.Cleanup(func() { _ = redisClient.Close() })
	queue := runtimeTestQueue{}
	return &configuredRuntime{
		pool:       testConfiguredRuntimePool(t),
		redis:      redisClient,
		dispatcher: delivery.NewOutboxDispatcher(runtimeTestOutboxRepo{}, queue, time.Second),
		worker: delivery.NewConfiguredWorker(queue, delivery.WorkerDependencies{
			Repository:   runtimeTestWorkerRepo{},
			Destinations: runtimeTestDestinationReader{},
			Provider:     delivery.UnconfiguredProvider{},
		}),
	}
}

func testRuntimeConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		APIAddr:        "127.0.0.1:0",
		DatabaseURL:    "",
		RedisURL:       "redis://127.0.0.1:6379/0",
		JWTSecret:      strings.Repeat("x", 32),
		JWTIssuer:      "lingow-api",
		JWTAudience:    "lingow-client",
		DestinationKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
	}
}

func TestRunConfiguredFailsWhenDatabaseURLMissing(t *testing.T) {
	if err := runConfigured(testRuntimeConfig(t)); err == nil {
		t.Fatal("runConfigured() succeeded without database URL")
	}
}

func TestNewConfiguredRuntimeRejectsMissingDatabaseURL(t *testing.T) {
	_, _, err := newConfiguredRuntime(context.Background(), testRuntimeConfig(t))
	if err == nil {
		t.Fatal("newConfiguredRuntime() succeeded without database URL")
	}
}

func TestConfiguredProviderDefaultsToFailClosed(t *testing.T) {
	provider, err := configuredProvider("unconfigured")
	if err != nil {
		t.Fatalf("configuredProvider() error = %v", err)
	}
	idempotent, ok := provider.(delivery.IdempotentProvider)
	if provider == nil || !ok || idempotent.SupportsProviderIdempotency() {
		t.Fatal("unconfigured provider must be non-idempotent and non-nil")
	}
	if err := provider.Send(t.Context(), delivery.SendRequest{}); !errors.Is(err, delivery.ErrProviderNotConfigured) {
		t.Fatalf("Send() error = %v, want ErrProviderNotConfigured", err)
	}
}

func TestConfiguredProviderAcceptsFakeEmail(t *testing.T) {
	provider, err := configuredProvider("fake_email")
	if err != nil {
		t.Fatalf("configuredProvider() error = %v", err)
	}
	if provider == nil {
		t.Fatal("configuredProvider() returned nil fake email provider")
	}
}

func TestConfiguredProviderRejectsUnknownName(t *testing.T) {
	if _, err := configuredProvider("smtp"); err == nil {
		t.Fatal("configuredProvider() succeeded for unknown provider")
	}
}

func TestConfiguredRuntimeHonorsCanceledStartupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := newConfiguredRuntime(ctx, config.Config{
		DatabaseURL: "postgres://127.0.0.1:1/lingow",
	})
	if err == nil {
		t.Fatal("newConfiguredRuntime() succeeded with canceled startup context")
	}
}

func TestConfiguredRuntimeCloseIsSafeOnEmptyReceiver(t *testing.T) {
	var runtime configuredRuntime
	runtime.Close()
}

func TestConfiguredRuntimeCloseClosesRedis(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	runtime := &configuredRuntime{redis: client}
	runtime.Close()
}

func TestConfiguredRuntimeServeRejectsIncompleteRuntime(t *testing.T) {
	tests := []struct {
		name    string
		runtime *configuredRuntime
		handler http.Handler
	}{
		{name: "nil runtime", runtime: nil, handler: http.NewServeMux()},
		{name: "missing pool", runtime: &configuredRuntime{}, handler: http.NewServeMux()},
		{
			name: "missing worker",
			runtime: &configuredRuntime{
				pool:       testConfiguredRuntimePool(t),
				redis:      redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"}),
				dispatcher: delivery.NewOutboxDispatcher(runtimeTestOutboxRepo{}, runtimeTestQueue{}, time.Second),
			},
			handler: http.NewServeMux(),
		},
		{name: "missing handler", runtime: newRuntimeServeFixture(t), handler: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.runtime == nil {
				var nilRuntime *configuredRuntime
				if err := nilRuntime.Serve(":0", test.handler); err == nil {
					t.Fatal("Serve() succeeded for nil runtime")
				}
				return
			}
			if err := test.runtime.Serve("127.0.0.1:0", test.handler); err == nil {
				t.Fatal("Serve() succeeded for incomplete runtime")
			}
		})
	}
}

func TestConfiguredRuntimeServeStopsWhenWorkerIsNotConfigured(t *testing.T) {
	runtime := newRuntimeServeFixture(t)
	err := runtime.Serve("127.0.0.1:0", http.NewServeMux())
	if err == nil || !errors.Is(err, delivery.ErrWorkerNotConfigured) {
		t.Fatalf("Serve() error = %v, want ErrWorkerNotConfigured", err)
	}
}

func TestRunDeliveryComponentStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	errs := make(chan error, 1)
	var components sync.WaitGroup
	components.Add(1)
	go runDeliveryComponent(ctx, "worker", func(context.Context) error {
		return errors.New("transient")
	}, errs, &components)
	components.Wait()

	select {
	case err := <-errs:
		t.Fatalf("runDeliveryComponent() sent error %v on canceled context", err)
	default:
	}
}

func TestRunDeliveryComponentReportsMissingWorkerDependencies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errs := make(chan error, 1)
	var components sync.WaitGroup
	components.Add(1)
	go runDeliveryComponent(ctx, "delivery worker", func(context.Context) error {
		return delivery.ErrWorkerNotConfigured
	}, errs, &components)
	components.Wait()

	err := <-errs
	if err == nil || !errors.Is(err, delivery.ErrWorkerNotConfigured) {
		t.Fatalf("runDeliveryComponent() error = %v, want ErrWorkerNotConfigured", err)
	}
}

func TestRunDeliveryComponentRetriesTransientErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	errs := make(chan error, 1)
	var components sync.WaitGroup
	components.Add(1)
	go runDeliveryComponent(ctx, "outbox dispatcher", func(context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("transient")
		}
		cancel()
		return context.Canceled
	}, errs, &components)
	components.Wait()

	if calls.Load() < 2 {
		t.Fatalf("calls = %d, want at least 2", calls.Load())
	}
	select {
	case err := <-errs:
		t.Fatalf("runDeliveryComponent() sent error %v after cancel", err)
	default:
	}
}

func TestRunDeliveryComponentStopsTimerOnContextCancelDuringRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	errs := make(chan error, 1)
	var components sync.WaitGroup
	components.Add(1)
	go runDeliveryComponent(ctx, "worker", func(context.Context) error {
		return errors.New("transient")
	}, errs, &components)
	time.Sleep(20 * time.Millisecond)
	cancel()
	components.Wait()

	select {
	case err := <-errs:
		t.Fatalf("runDeliveryComponent() sent error %v on canceled context", err)
	default:
	}
}

func TestConfiguredRuntimeServeStopsOnTerminationSignal(t *testing.T) {
	runtime := newRuntimeBlockingServeFixture(t)
	done := make(chan error, 1)
	go func() {
		done <- runtime.Serve("127.0.0.1:0", http.NewServeMux())
	}()

	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v, want graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not stop after SIGTERM")
	}
}

func TestShutdownConfiguredServerWaitsForComponents(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	server := &http.Server{Handler: http.NewServeMux()}
	go func() {
		_ = server.Serve(listener)
	}()

	var components sync.WaitGroup
	components.Add(1)
	go func() {
		defer components.Done()
		time.Sleep(10 * time.Millisecond)
	}()

	if err := shutdownConfiguredServer(server, &components); err != nil {
		t.Fatalf("shutdownConfiguredServer() error = %v", err)
	}
}
