package outbox

import (
	"context"
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

func TestOpenRuntimeFromEnvUsesValkeyWhenConfigured(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	t.Setenv("REALTIME_OUTBOX", RuntimeValkey)
	t.Setenv("REDIS_URL", "redis://"+server.Addr())
	t.Setenv("USAGE_STREAM", "lingow:usage:recorded")

	runtime, err := OpenRuntimeFromEnv(context.Background())
	if err != nil {
		t.Fatalf("OpenRuntimeFromEnv() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if runtime.Outbox == nil {
		t.Fatal("Outbox = nil, want valkey-backed outbox")
	}
}
