package outbox

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/redis/go-redis/v9"
)

const (
	RuntimeMemory = "memory"
	RuntimeValkey = "valkey"
)

// Runtime exposes a durable outbox boundary and optional cleanup for process shutdown.
type Runtime struct {
	Outbox pipeline.DurableOutbox
	close  func() error
}

// Close releases any background resources owned by the runtime.
func (r *Runtime) Close() error {
	if r == nil || r.close == nil {
		return nil
	}
	return r.close()
}

// OpenRuntimeFromEnv selects an outbox backend from REALTIME_OUTBOX and REDIS_URL.
func OpenRuntimeFromEnv(ctx context.Context) (*Runtime, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("REALTIME_OUTBOX")))
	production := strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production")
	if production && (backend == "" || backend == RuntimeMemory) {
		return nil, fmt.Errorf("initialize realtime outbox: REALTIME_OUTBOX=valkey is required in production")
	}
	switch backend {
	case "", RuntimeMemory:
		memory := NewMemoryOutbox()
		return &Runtime{Outbox: memory, close: func() error { return nil }}, nil
	case RuntimeValkey:
		redisURL := os.Getenv("REDIS_URL")
		if redisURL == "" {
			return nil, fmt.Errorf("initialize realtime outbox: REDIS_URL is required when REALTIME_OUTBOX=valkey")
		}
		options, err := redis.ParseURL(redisURL)
		if err != nil {
			return nil, fmt.Errorf("initialize realtime outbox: %w", err)
		}
		client := redis.NewClient(options)
		if err := client.Ping(ctx).Err(); err != nil {
			client.Close()
			return nil, fmt.Errorf("initialize realtime outbox: %w", err)
		}
		stream := os.Getenv("USAGE_STREAM")
		if stream == "" {
			stream = os.Getenv("LINGOW_USAGE_STREAM")
		}
		modeStream := os.Getenv("MODE_CHANGED_STREAM")
		if modeStream == "" {
			modeStream = os.Getenv("LINGOW_MODE_CHANGED_STREAM")
		}
		writer, err := NewValkeyWriter(client, stream, modeStream)
		if err != nil {
			client.Close()
			return nil, fmt.Errorf("initialize realtime outbox: %w", err)
		}
		return &Runtime{
			Outbox: NewAdapter(writer),
			close:  client.Close,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported REALTIME_OUTBOX %q (supported: memory, valkey)", backend)
	}
}
