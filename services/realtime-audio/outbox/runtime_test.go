package outbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestOpenRuntimeFromEnvUsesMemoryByDefault(t *testing.T) {
	t.Setenv("APP_ENV", "local")
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
	t.Setenv("APP_ENV", "local")
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

func TestOpenRuntimeFromEnvRestrictsMemoryToDevelopmentEnvironments(t *testing.T) {
	for _, environment := range []string{"", "production", "prod", "staging", "typo"} {
		for _, backend := range []string{"", RuntimeMemory} {
			t.Setenv("APP_ENV", environment)
			t.Setenv("REALTIME_OUTBOX", backend)
			if _, err := OpenRuntimeFromEnv(t.Context()); err == nil || !strings.Contains(err.Error(), "requires APP_ENV") {
				t.Fatalf("OpenRuntimeFromEnv(APP_ENV=%q, REALTIME_OUTBOX=%q) error = %v", environment, backend, err)
			}
		}
	}
}

func TestOpenRedisClientSelectsConfiguredTopology(t *testing.T) {
	standalone, err := openRedisClient("redis://localhost:6379/0", RedisModeStandalone)
	if err != nil {
		t.Fatalf("open standalone client: %v", err)
	}
	t.Cleanup(func() { _ = standalone.Close() })
	if _, ok := standalone.(*redis.Client); !ok {
		t.Fatalf("standalone client type = %T", standalone)
	}

	cluster, err := openRedisClient("redis://user:secret@localhost:6379?addr=localhost:6380", RedisModeCluster)
	if err != nil {
		t.Fatalf("open cluster client: %v", err)
	}
	t.Cleanup(func() { _ = cluster.Close() })
	clusterClient, ok := cluster.(*redis.ClusterClient)
	if !ok || len(clusterClient.Options().Addrs) != 2 {
		t.Fatalf("cluster client = %T, options = %#v", cluster, clusterClient)
	}
	if _, err := openRedisClient("redis://localhost:6379", "sentinel"); err == nil {
		t.Fatal("unsupported Redis mode error = nil")
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
	t.Setenv("MODE_CHANGED_STREAM", "lingow:mode:configured")
	t.Setenv("LINGOW_MODE_CHANGED_STREAM", "")

	runtime, err := OpenRuntimeFromEnv(context.Background())
	if err != nil {
		t.Fatalf("OpenRuntimeFromEnv() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if runtime.Outbox == nil {
		t.Fatal("Outbox = nil, want valkey-backed outbox")
	}
	adapter := runtime.Outbox.(*Adapter)
	writer := adapter.writer.(*ValkeyWriter)
	if writer.streams[realtimev1.ModeChangedTopic] != "lingow:mode:configured" {
		t.Fatalf("mode stream = %q", writer.streams[realtimev1.ModeChangedTopic])
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
	t.Setenv("MODE_CHANGED_STREAM", "")
	t.Setenv("LINGOW_MODE_CHANGED_STREAM", "lingow:mode:fallback")

	runtime, err := OpenRuntimeFromEnv(context.Background())
	if err != nil {
		t.Fatalf("OpenRuntimeFromEnv() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if runtime.Outbox == nil {
		t.Fatal("Outbox = nil, want valkey-backed outbox")
	}
	adapter := runtime.Outbox.(*Adapter)
	writer := adapter.writer.(*ValkeyWriter)
	if writer.streams[realtimev1.ModeChangedTopic] != "lingow:mode:fallback" {
		t.Fatalf("mode stream = %q", writer.streams[realtimev1.ModeChangedTopic])
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
