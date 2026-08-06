package outbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestOpenRuntimeFromEnvUsesMemoryByDefault(t *testing.T) {
	t.Setenv("REALTIME_OUTBOX", "")
	runtime, err := OpenRuntimeFromEnv(context.Background())
	if err != nil {
		t.Fatalf("OpenRuntimeFromEnv() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if runtime.Outbox == nil {
		t.Fatal("Outbox = nil, want memory outbox")
	}
}

func TestOpenRuntimeFromEnvUsesExplicitMemory(t *testing.T) {
	t.Setenv("REALTIME_OUTBOX", RuntimeMemory)
	runtime, err := OpenRuntimeFromEnv(context.Background())
	if err != nil {
		t.Fatalf("OpenRuntimeFromEnv() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if runtime.Outbox == nil {
		t.Fatal("Outbox = nil, want memory outbox")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRuntimeCloseNilSafe(t *testing.T) {
	var runtime *Runtime
	if err := runtime.Close(); err != nil {
		t.Fatalf("nil Close() error = %v", err)
	}
	runtime = &Runtime{}
	if err := runtime.Close(); err != nil {
		t.Fatalf("empty Close() error = %v", err)
	}
}

func TestOpenRuntimeFromEnvRejectsUnsupportedBackend(t *testing.T) {
	t.Setenv("REALTIME_OUTBOX", "sqlite")
	_, err := OpenRuntimeFromEnv(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported REALTIME_OUTBOX") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenRuntimeFromEnvValkeyRequiresRedisURL(t *testing.T) {
	t.Setenv("REALTIME_OUTBOX", RuntimeValkey)
	t.Setenv("REDIS_URL", "")
	_, err := OpenRuntimeFromEnv(context.Background())
	if err == nil || !strings.Contains(err.Error(), "REDIS_URL is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenRuntimeFromEnvValkeyRejectsBadRedisURL(t *testing.T) {
	t.Setenv("REALTIME_OUTBOX", RuntimeValkey)
	t.Setenv("REDIS_URL", "://bad")
	_, err := OpenRuntimeFromEnv(context.Background())
	if err == nil || !strings.Contains(err.Error(), "initialize realtime outbox") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenRuntimeFromEnvUsesValkeyWhenConfigured(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	t.Setenv("REALTIME_OUTBOX", RuntimeValkey)
	t.Setenv("REDIS_URL", "redis://"+server.Addr())
	t.Setenv("USAGE_STREAM", "lingow:usage:recorded")
	t.Setenv("LINGOW_USAGE_STREAM", "")

	runtime, err := OpenRuntimeFromEnv(context.Background())
	if err != nil {
		t.Fatalf("OpenRuntimeFromEnv() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if runtime.Outbox == nil {
		t.Fatal("Outbox = nil, want valkey-backed outbox")
	}
}

func TestOpenRuntimeFromEnvFallsBackToLingowUsageStream(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	t.Setenv("REALTIME_OUTBOX", RuntimeValkey)
	t.Setenv("REDIS_URL", "redis://"+server.Addr())
	t.Setenv("USAGE_STREAM", "")
	t.Setenv("LINGOW_USAGE_STREAM", "lingow:usage:fallback")

	runtime, err := OpenRuntimeFromEnv(context.Background())
	if err != nil {
		t.Fatalf("OpenRuntimeFromEnv() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if runtime.Outbox == nil {
		t.Fatal("Outbox = nil, want valkey-backed outbox")
	}
}

func TestOpenRuntimeFromEnvValkeyPingFailure(t *testing.T) {
	t.Setenv("REALTIME_OUTBOX", RuntimeValkey)
	t.Setenv("REDIS_URL", "redis://127.0.0.1:1")
	t.Setenv("USAGE_STREAM", "lingow:usage:recorded")

	_, err := OpenRuntimeFromEnv(context.Background())
	if err == nil {
		t.Fatal("expected ping failure")
	}
	if !strings.Contains(err.Error(), "initialize realtime outbox") {
		t.Fatalf("error = %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected canceled error: %v", err)
	}
}
