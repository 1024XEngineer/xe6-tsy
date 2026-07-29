package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	valkeyAttemptIDField    = "attempt_id"
	valkeyIdempotencyField  = "idempotency_key"
	defaultDeliveryStream   = "lingow:delivery"
	defaultDeliveryGroup    = "lingow-delivery"
	defaultDeliveryConsumer = "api"
)

var (
	// ErrValkeyQueueUnavailable means the queue has no usable Redis client.
	ErrValkeyQueueUnavailable = errors.New("valkey delivery queue unavailable")
	// ErrValkeyQueueInvalidMessage means a required broker identity is absent.
	ErrValkeyQueueInvalidMessage = errors.New("invalid valkey delivery message")
	// ErrValkeyQueueInvalidConfig means the queue cannot safely be constructed.
	ErrValkeyQueueInvalidConfig = errors.New("invalid valkey delivery queue configuration")
)

// ValkeyStreamClient is the small Redis Streams surface used by ValkeyQueue.
// Keeping this boundary narrow makes the queue testable without a live broker;
// *redis.Client satisfies it. A Redis Cluster deployment must configure the
// three script keys with one shared hash tag because NACK and promotion use
// multi-key Lua scripts.
type ValkeyStreamClient interface {
	XAdd(context.Context, *redis.XAddArgs) *redis.StringCmd
	XGroupCreateMkStream(context.Context, string, string, string) *redis.StatusCmd
	XReadGroup(context.Context, *redis.XReadGroupArgs) *redis.XStreamSliceCmd
	XAutoClaim(context.Context, *redis.XAutoClaimArgs) *redis.XAutoClaimCmd
	XPendingExt(context.Context, *redis.XPendingExtArgs) *redis.XPendingExtCmd
	XClaim(context.Context, *redis.XClaimArgs) *redis.XMessageSliceCmd
	XAck(context.Context, string, string, ...string) *redis.IntCmd
	Do(context.Context, ...interface{}) *redis.Cmd
}

// ValkeyQueueConfig controls stream names and consumer recovery behavior.
// Consumer must be unique per running worker; duplicate attempt IDs are
// intentionally allowed because Repository.ClaimAttempt is the idempotency
// authority after a broker redelivery.
type ValkeyQueueConfig struct {
	Stream      string
	Group       string
	Consumer    string
	DelayStream string
	DelayKey    string
	ClaimIdle   time.Duration
	Block       time.Duration
	BatchSize   int64
	MaxLen      int64
}

func (c ValkeyQueueConfig) withDefaults() ValkeyQueueConfig {
	if c.Stream == "" {
		c.Stream = defaultDeliveryStream
	}
	if c.Group == "" {
		c.Group = defaultDeliveryGroup
	}
	if c.Consumer == "" {
		c.Consumer = defaultDeliveryConsumer
	}
	if c.DelayStream == "" {
		c.DelayStream = c.Stream + ":delayed"
	}
	if c.DelayKey == "" {
		c.DelayKey = c.Stream + ":delay"
	}
	if c.ClaimIdle <= 0 {
		c.ClaimIdle = 30 * time.Second
	}
	if c.Block == 0 {
		// A finite block lets Receive periodically promote delayed entries even
		// when no fresh stream entry arrives to wake XREADGROUP.
		c.Block = time.Second
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 10
	}
	return c
}

// ValkeyQueue implements Queue on top of a Redis consumer group. Nack moves
// the original entry into a delayed stream and a scored ZSET in one Lua
// transaction; Receive promotes due entries before reading new work. The
// stable attempt_id field intentionally permits duplicate broker entries so
// the durable attempt claim remains the single side-effect gate.
type ValkeyQueue struct {
	client      ValkeyStreamClient
	stream      string
	group       string
	consumer    string
	delayStream string
	delayKey    string
	claimIdle   time.Duration
	block       time.Duration
	batchSize   int64
	maxLen      int64

	groupMu    sync.Mutex
	groupReady bool
	pendingMu  sync.Mutex
	pending    []redis.XMessage
}

var _ Queue = (*ValkeyQueue)(nil)

// NewValkeyQueue creates a queue without contacting Valkey. Connectivity and
// group creation are checked lazily by Enqueue/Receive so startup can compose
// the runtime before the broker is reachable.
func NewValkeyQueue(client ValkeyStreamClient, config ValkeyQueueConfig) *ValkeyQueue {
	config = config.withDefaults()
	return &ValkeyQueue{
		client:      client,
		stream:      config.Stream,
		group:       config.Group,
		consumer:    config.Consumer,
		delayStream: config.DelayStream,
		delayKey:    config.DelayKey,
		claimIdle:   config.ClaimIdle,
		block:       config.Block,
		batchSize:   config.BatchSize,
		maxLen:      config.MaxLen,
	}
}

func (q *ValkeyQueue) validate() error {
	if q == nil || q.client == nil {
		return ErrValkeyQueueUnavailable
	}
	// go-redis uses -1 to explicitly omit BLOCK (useful for deterministic
	// polling); values below -1 are never meaningful and usually indicate a
	// configuration unit/sign error.
	if q.stream == "" || q.group == "" || q.consumer == "" || q.delayStream == "" || q.delayKey == "" || q.stream == q.delayStream || q.stream == q.delayKey || q.delayStream == q.delayKey || q.block < -1 || q.batchSize <= 0 {
		return ErrValkeyQueueInvalidConfig
	}
	return nil
}

func (q *ValkeyQueue) ensureGroup(ctx context.Context) error {
	if err := q.validate(); err != nil {
		return err
	}
	q.groupMu.Lock()
	defer q.groupMu.Unlock()
	if q.groupReady {
		return nil
	}
	err := q.client.XGroupCreateMkStream(ctx, q.stream, q.group, "0").Err()
	if err != nil && !isBusyGroupError(err) {
		return fmt.Errorf("create delivery consumer group: %w", err)
	}
	q.groupReady = true
	return nil
}

// Enqueue appends an attempt event. It does not deduplicate stream entries:
// the attempt ID is copied into every entry and ClaimAttempt resolves races
// after duplicate outbox publishes.
func (q *ValkeyQueue) Enqueue(ctx context.Context, item QueueItem) error {
	attemptID := strings.TrimSpace(item.AttemptID)
	idempotencyKey := strings.TrimSpace(item.IdempotencyKey)
	if attemptID == "" || idempotencyKey == "" {
		return ErrValkeyQueueInvalidMessage
	}
	if err := q.ensureGroup(ctx); err != nil {
		return err
	}
	args := &redis.XAddArgs{
		Stream: q.stream,
		Values: []string{valkeyAttemptIDField, attemptID, valkeyIdempotencyField, idempotencyKey},
	}
	if q.maxLen > 0 {
		args.MaxLen = q.maxLen
		args.Approx = true
	}
	if _, err := q.client.XAdd(ctx, args).Result(); err != nil {
		return fmt.Errorf("enqueue delivery attempt %q: %w", attemptID, err)
	}
	return nil
}

// Receive promotes due delayed entries, recovers stale pending entries, then
// blocks for a new consumer-group entry. Malformed entries are ACKed and
// skipped so one poison record cannot permanently stall the stream.
func (q *ValkeyQueue) Receive(ctx context.Context) (QueueMessage, error) {
	if err := q.ensureGroup(ctx); err != nil {
		return QueueMessage{}, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return QueueMessage{}, err
		}
		if messages := q.takePending(); len(messages) > 0 {
			if item, ok, err := q.firstUsable(ctx, messages); err != nil || ok {
				return item, err
			}
		}
		if err := q.promoteDue(ctx); err != nil {
			return QueueMessage{}, err
		}
		if q.claimIdle > 0 {
			messages, err := q.claimStale(ctx)
			if err != nil {
				return QueueMessage{}, err
			}
			if item, ok, err := q.firstUsable(ctx, messages); err != nil || ok {
				return item, err
			}
		}

		streams, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    q.group,
			Consumer: q.consumer,
			Streams:  []string{q.stream, ">"},
			Count:    q.batchSize,
			Block:    q.block,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return QueueMessage{}, fmt.Errorf("read delivery stream: %w", err)
		}
		messages := flattenStreamMessages(streams)
		if item, ok, err := q.firstUsable(ctx, messages); err != nil || ok {
			return item, err
		}
	}
}

func (q *ValkeyQueue) claimStale(ctx context.Context) ([]redis.XMessage, error) {
	messages, _, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   q.stream,
		Group:    q.group,
		Consumer: q.consumer,
		MinIdle:  q.claimIdle,
		Start:    "0-0",
		Count:    q.batchSize,
	}).Result()
	if err == nil {
		return messages, nil
	}
	if !isUnsupportedAutoClaimError(err) {
		return nil, fmt.Errorf("claim stale delivery attempts: %w", err)
	}
	// XAUTOCLAIM was introduced after consumer groups. Keep a legacy path for
	// older Redis/Valkey nodes rather than silently leaving abandoned attempts.
	pending, pendingErr := q.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: q.stream,
		Group:  q.group,
		Start:  "-",
		End:    "+",
		Count:  q.batchSize,
	}).Result()
	if pendingErr != nil {
		return nil, fmt.Errorf("list pending delivery attempts: %w", pendingErr)
	}
	ids := make([]string, 0, len(pending))
	for _, entry := range pending {
		if entry.Idle >= q.claimIdle {
			ids = append(ids, entry.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	claimed, claimErr := q.client.XClaim(ctx, &redis.XClaimArgs{
		Stream:   q.stream,
		Group:    q.group,
		Consumer: q.consumer,
		MinIdle:  q.claimIdle,
		Messages: ids,
	}).Result()
	if claimErr != nil {
		return nil, fmt.Errorf("claim legacy pending delivery attempts: %w", claimErr)
	}
	return claimed, nil
}

func (q *ValkeyQueue) firstUsable(ctx context.Context, messages []redis.XMessage) (QueueMessage, bool, error) {
	for index, message := range messages {
		if strings.TrimSpace(message.ID) == "" {
			continue
		}
		attemptID := redisValueString(message.Values[valkeyAttemptIDField])
		idempotencyKey := redisValueString(message.Values[valkeyIdempotencyField])
		if strings.TrimSpace(attemptID) == "" || strings.TrimSpace(idempotencyKey) == "" {
			// Poison entries cannot be processed by the worker. ACKing them is
			// deliberate: retrying a record without a stable identity would make
			// the delivery side effect impossible to deduplicate.
			if err := q.Ack(ctx, message.ID); err != nil {
				return QueueMessage{}, false, err
			}
			continue
		}
		q.pushPending(messages[index+1:])
		return QueueMessage{AccountID: "", AttemptID: attemptID, IdempotencyKey: idempotencyKey, Receipt: message.ID}, true, nil
	}
	return QueueMessage{}, false, nil
}

func (q *ValkeyQueue) takePending() []redis.XMessage {
	q.pendingMu.Lock()
	defer q.pendingMu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	messages := q.pending
	q.pending = nil
	return messages
}

func (q *ValkeyQueue) pushPending(messages []redis.XMessage) {
	if len(messages) == 0 {
		return
	}
	q.pendingMu.Lock()
	q.pending = append(q.pending, messages...)
	q.pendingMu.Unlock()
}

func (q *ValkeyQueue) promoteDue(ctx context.Context) error {
	cmd := q.client.Do(ctx, "EVAL", promoteDueScript, 3, q.delayStream, q.delayKey, q.stream, time.Now().UTC().UnixMilli(), q.batchSize)
	if _, err := cmd.Int(); err != nil {
		return fmt.Errorf("promote delayed delivery attempts: %w", err)
	}
	return nil
}

// Ack is idempotent: XACK returns zero when a receipt was already settled.
func (q *ValkeyQueue) Ack(ctx context.Context, receipt string) error {
	if err := q.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(receipt) == "" {
		return ErrValkeyQueueInvalidMessage
	}
	if _, err := q.client.XAck(ctx, q.stream, q.group, receipt).Result(); err != nil {
		return fmt.Errorf("ack delivery receipt %q: %w", receipt, err)
	}
	return nil
}

// Nack atomically removes a receipt from the consumer group's PEL and records
// it in a delayed stream plus ZSET. The ZSET score is the requested Unix
// millisecond availability time; Receive promotes due entries. A crash cannot
// leave the attempt acknowledged without a durable delayed copy because all
// steps run inside one Redis script.
func (q *ValkeyQueue) Nack(ctx context.Context, receipt string, availableAt time.Time) error {
	if err := q.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(receipt) == "" {
		return ErrValkeyQueueInvalidMessage
	}
	if availableAt.IsZero() {
		availableAt = time.Now().UTC()
	}
	cmd := q.client.Do(ctx, "EVAL", nackScript, 3, q.stream, q.delayStream, q.delayKey, q.group, receipt, availableAt.UTC().UnixMilli())
	if _, err := cmd.Int(); err != nil {
		return fmt.Errorf("nack delivery receipt %q: %w", receipt, err)
	}
	return nil
}

func flattenStreamMessages(streams []redis.XStream) []redis.XMessage {
	var result []redis.XMessage
	for _, stream := range streams {
		result = append(result, stream.Messages...)
	}
	return result
}

func redisValueString(value interface{}) string {
	switch value := value.(type) {
	case nil:
		return ""
	case string:
		return value
	case []byte:
		return string(value)
	case fmt.Stringer:
		return value.String()
	default:
		return ""
	}
}

func isBusyGroupError(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "BUSYGROUP")
}

func isUnsupportedAutoClaimError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unknown command") || strings.Contains(s, "unknown subcommand") || strings.Contains(s, "unsupported")
}

const nackScript = `
local entries = redis.call('XRANGE', KEYS[1], ARGV[2], ARGV[2])
if #entries == 0 then
  -- The stream entry may have been trimmed after it entered the PEL. ACK still
  -- removes the orphaned pending ID; otherwise every recovery scan sees it
  -- forever even though there is no payload left to retry.
  redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
  return 0
end
local fields = entries[1][2]
local attempt = false
local idempotency = false
for i = 1, #fields, 2 do
  if fields[i] == 'attempt_id' and fields[i + 1] ~= nil and fields[i + 1] ~= '' then
    attempt = true
  elseif fields[i] == 'idempotency_key' and fields[i + 1] ~= nil and fields[i + 1] ~= '' then
    idempotency = true
  end
end
if not attempt or not idempotency then
  redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
  redis.call('XDEL', KEYS[1], ARGV[2])
  return 0
end
local delayed_id = redis.call('XADD', KEYS[2], '*', unpack(fields))
redis.call('ZADD', KEYS[3], ARGV[3], delayed_id)
redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
redis.call('XDEL', KEYS[1], ARGV[2])
return 1
`

const promoteDueScript = `
local ids = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[1], 'LIMIT', 0, ARGV[2])
local promoted = 0
for _, delayed_id in ipairs(ids) do
  local entries = redis.call('XRANGE', KEYS[1], delayed_id, delayed_id)
  if #entries > 0 then
    local fields = entries[1][2]
    redis.call('XADD', KEYS[3], '*', unpack(fields))
    redis.call('ZREM', KEYS[2], delayed_id)
    redis.call('XDEL', KEYS[1], delayed_id)
    promoted = promoted + 1
  else
    redis.call('ZREM', KEYS[2], delayed_id)
  end
end
return promoted
`
