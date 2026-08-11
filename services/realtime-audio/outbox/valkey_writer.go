package outbox

import (
	"context"
	"encoding/hex"
	"fmt"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/redis/go-redis/v9"
)

const usageRecordedTopic = "usage.recorded"

const (
	valkeyWriteConflict int64 = -1
	valkeyWriteReplay   int64 = 0
	valkeyWriteAccepted int64 = 1
)

// acceptScript owns both the idempotency decision and stream append. Redis transactions do not
// roll back earlier commands when a later queued command returns a runtime error, so a MULTI with
// SET followed by XADD can leave a false deduplication marker. The script catches an XADD error and
// removes the marker before returning; successful appends and their marker remain one atomic unit.
var acceptScript = redis.NewScript(`
local stored = redis.call("GET", KEYS[1])
if stored then
    if stored == ARGV[1] then
        return 0
    end
    return -1
end

redis.call("SET", KEYS[1], ARGV[1])
local appended = redis.pcall("XADD", KEYS[2], "*", "payload", ARGV[2])
if type(appended) == "table" and appended.err then
    redis.call("DEL", KEYS[1])
    return redis.error_reply(appended.err)
end
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
	result, err := acceptScript.Run(ctx, w.client, []string{dedupKey, stream}, hashHex, entry.Payload).Int64()
	if err != nil {
		return Ack{}, err
	}
	switch result {
	case valkeyWriteAccepted, valkeyWriteReplay:
		return Ack{Accepted: true}, nil
	case valkeyWriteConflict:
		return Ack{}, ErrConflict
	default:
		return Ack{}, fmt.Errorf("accept outbox entry: unexpected script result %d", result)
	}
}

func (w *ValkeyWriter) dedupKey(stream string, entry Entry) string {
	return stream + ":dedup:" + entry.Topic + "\x00" + entry.IdempotencyKey
}

var _ Writer = (*ValkeyWriter)(nil)
