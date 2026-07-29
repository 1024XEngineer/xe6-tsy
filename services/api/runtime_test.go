package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
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
