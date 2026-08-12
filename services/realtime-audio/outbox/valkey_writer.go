package outbox

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/redis/go-redis/v9"
)

const usageRecordedTopic = "usage.recorded"

// appendEntryScript keeps the stream append and dedup marker in one server-side critical
// section. XADD deliberately runs first: when the stream key has the wrong type or XADD otherwise
// fails, Redis aborts the script before SET can leave a dedup-only false acknowledgement.
var appendEntryScript = redis.NewScript(`
local stored = redis.call("GET", KEYS[1])
if stored then
  if stored == ARGV[1] then
    return 0
  end
  return -1
end

redis.call("XADD", KEYS[2], "*", "payload", ARGV[2])
redis.call("SET", KEYS[1], ARGV[1])
return 1
`)

// ValkeyWriter publishes canonical outbox entries to a Redis/Valkey stream.
type ValkeyWriter struct {
	client  *redis.Client
	streams map[string]string
}

// NewValkeyWriter constructs a writer for usage and mode-change streams. The optional mode stream
// keeps existing usage-only callers source-compatible while production wiring configures both.
func NewValkeyWriter(client *redis.Client, usageStream string, modeStreams ...string) (*ValkeyWriter, error) {
	if client == nil {
		return nil, ErrWriterRequired
	}
	if len(modeStreams) > 1 {
		return nil, fmt.Errorf("configure valkey writer: at most one mode stream is supported")
	}
	if usageStream == "" {
		usageStream = "lingow:usage:recorded"
	}
	modeStream := "lingow:realtime:mode:changed"
	if len(modeStreams) == 1 && modeStreams[0] != "" {
		modeStream = modeStreams[0]
	}
	return &ValkeyWriter{client: client, streams: map[string]string{
		usageRecordedTopic:          usageStream,
		realtimev1.ModeChangedTopic: modeStream,
	}}, nil
}

// Accept publishes one durable entry to the configured stream.
func (w *ValkeyWriter) Accept(ctx context.Context, entry Entry) (Ack, error) {
	if err := ctx.Err(); err != nil {
		return Ack{}, err
	}
	if w == nil || w.client == nil {
		return Ack{}, ErrWriterRequired
	}
	stream, ok := w.streams[entry.Topic]
	if !ok || stream == "" {
		return Ack{}, fmt.Errorf("%w: topic %q", ErrUnsupportedPayload, entry.Topic)
	}
	dedupKey := w.dedupKey(stream, entry)
	hashHex := hex.EncodeToString(entry.PayloadHash[:])
	result, err := appendEntryScript.Run(
		ctx,
		w.client,
		[]string{dedupKey, stream},
		hashHex,
		entry.Payload,
	).Int64()
	if err != nil {
		return Ack{}, err
	}
	if result == -1 {
		return Ack{}, ErrConflict
	}
	if result != 0 && result != 1 {
		return Ack{}, fmt.Errorf("append valkey outbox entry: unexpected script result %d", result)
	}
	return Ack{Accepted: true}, nil
}

func (w *ValkeyWriter) dedupKey(stream string, entry Entry) string {
	// Redis Cluster scripts require every key in one hash slot. A plain Stream hashes its whole
	// name; wrapping that name as a hash tag gives the dedup key the same slot. If the Stream already
	// has a hash tag, preserve its tag instead.
	tag := stream
	if start := strings.IndexByte(stream, '{'); start >= 0 {
		if end := strings.IndexByte(stream[start+1:], '}'); end > 0 {
			tag = stream[start+1 : start+1+end]
		}
	}
	return "{" + tag + "}:dedup:" + entry.Topic + "\x00" + entry.IdempotencyKey
}

var _ Writer = (*ValkeyWriter)(nil)
