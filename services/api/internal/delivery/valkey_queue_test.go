package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

var _ ValkeyStreamClient = (*redis.Client)(nil)

type valkeyQueueFake struct {
	groupErr   error
	groupCalls int

	xaddErr   error
	xaddCalls []*redis.XAddArgs

	autoMessages []redis.XMessage
	autoErr      error
	autoCalls    int

	pending    []redis.XPendingExt
	pendingErr error
	claim      []redis.XMessage
	claimErr   error
	claimCalls int

	readStreams []redis.XStream
	readErr     error

	acked   []string
	doCalls [][]interface{}
	doValue interface{}
	doErr   error
}

func (f *valkeyQueueFake) XAdd(ctx context.Context, args *redis.XAddArgs) *redis.StringCmd {
	f.xaddCalls = append(f.xaddCalls, args)
	cmd := redis.NewStringCmd(ctx)
	if f.xaddErr != nil {
		cmd.SetErr(f.xaddErr)
	} else {
		cmd.SetVal("1-0")
	}
	return cmd
}

func (f *valkeyQueueFake) XGroupCreateMkStream(ctx context.Context, _, _, _ string) *redis.StatusCmd {
	f.groupCalls++
	cmd := redis.NewStatusCmd(ctx)
	if f.groupErr != nil {
		cmd.SetErr(f.groupErr)
	} else {
		cmd.SetVal("OK")
	}
	return cmd
}

func (f *valkeyQueueFake) XReadGroup(ctx context.Context, _ *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	cmd := redis.NewXStreamSliceCmd(ctx)
	if f.readErr != nil {
		cmd.SetErr(f.readErr)
	} else {
		cmd.SetVal(f.readStreams)
	}
	return cmd
}

func (f *valkeyQueueFake) XAutoClaim(ctx context.Context, _ *redis.XAutoClaimArgs) *redis.XAutoClaimCmd {
	f.autoCalls++
	cmd := redis.NewXAutoClaimCmd(ctx)
	if f.autoErr != nil {
		cmd.SetErr(f.autoErr)
	} else {
		cmd.SetVal(f.autoMessages, "0-0")
	}
	return cmd
}

func (f *valkeyQueueFake) XPendingExt(ctx context.Context, _ *redis.XPendingExtArgs) *redis.XPendingExtCmd {
	cmd := redis.NewXPendingExtCmd(ctx)
	if f.pendingErr != nil {
		cmd.SetErr(f.pendingErr)
	} else {
		cmd.SetVal(f.pending)
	}
	return cmd
}

func (f *valkeyQueueFake) XClaim(ctx context.Context, _ *redis.XClaimArgs) *redis.XMessageSliceCmd {
	f.claimCalls++
	cmd := redis.NewXMessageSliceCmd(ctx)
	if f.claimErr != nil {
		cmd.SetErr(f.claimErr)
	} else {
		cmd.SetVal(f.claim)
	}
	return cmd
}

func (f *valkeyQueueFake) XAck(ctx context.Context, _, _ string, ids ...string) *redis.IntCmd {
	f.acked = append(f.acked, ids...)
	cmd := redis.NewIntCmd(ctx)
	cmd.SetVal(int64(len(ids)))
	return cmd
}

func (f *valkeyQueueFake) Do(ctx context.Context, args ...interface{}) *redis.Cmd {
	f.doCalls = append(f.doCalls, args)
	cmd := redis.NewCmd(ctx)
	if f.doErr != nil {
		cmd.SetErr(f.doErr)
	} else if f.doValue != nil {
		cmd.SetVal(f.doValue)
	} else {
		cmd.SetVal(int64(0))
	}
	return cmd
}

func TestValkeyQueueEnqueueUsesStableAttemptIdentity(t *testing.T) {
	fake := &valkeyQueueFake{groupErr: errors.New("BUSYGROUP Consumer Group name already exists")}
	queue := NewValkeyQueue(fake, ValkeyQueueConfig{Stream: "delivery", Group: "workers", Consumer: "worker-1"})

	for range 2 {
		if err := queue.Enqueue(context.Background(), QueueItem{AttemptID: "attempt-1", IdempotencyKey: "request-1"}); err != nil {
			t.Fatalf("Enqueue() error = %v", err)
		}
	}
	if fake.groupCalls != 1 {
		t.Fatalf("group create calls = %d, want 1", fake.groupCalls)
	}
	if len(fake.xaddCalls) != 2 {
		t.Fatalf("XADD calls = %d, want 2 duplicate-safe publishes", len(fake.xaddCalls))
	}
	values, ok := fake.xaddCalls[0].Values.([]string)
	if !ok || len(values) != 4 || values[0] != valkeyAttemptIDField || values[1] != "attempt-1" || values[2] != valkeyIdempotencyField || values[3] != "request-1" {
		t.Fatalf("XADD values = %#v, want stable attempt and idempotency fields", fake.xaddCalls[0].Values)
	}
}

func TestValkeyQueueReceiveClaimsStaleBeforeReadingNew(t *testing.T) {
	fake := &valkeyQueueFake{
		autoMessages: []redis.XMessage{{ID: "7-0", Values: map[string]interface{}{valkeyAttemptIDField: "attempt-7", valkeyIdempotencyField: "key-7"}}},
	}
	queue := NewValkeyQueue(fake, ValkeyQueueConfig{Stream: "delivery", Group: "workers", Consumer: "worker-1", Block: time.Nanosecond})

	got, err := queue.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if got != (QueueMessage{AttemptID: "attempt-7", IdempotencyKey: "key-7", Receipt: "7-0"}) {
		t.Fatalf("Receive() = %#v, want claimed receipt", got)
	}
	if fake.autoCalls != 1 {
		t.Fatalf("XAUTOCLAIM calls = %d, want 1", fake.autoCalls)
	}
}

func TestValkeyQueuePreservesBatchMessagesForNextReceive(t *testing.T) {
	fake := &valkeyQueueFake{
		autoMessages: []redis.XMessage{
			{ID: "12-0", Values: map[string]interface{}{valkeyAttemptIDField: "attempt-12", valkeyIdempotencyField: "key-12"}},
			{ID: "13-0", Values: map[string]interface{}{valkeyAttemptIDField: "attempt-13", valkeyIdempotencyField: "key-13"}},
		},
	}
	queue := NewValkeyQueue(fake, ValkeyQueueConfig{Stream: "delivery", Group: "workers", Consumer: "worker-1", Block: time.Nanosecond})

	first, err := queue.Receive(context.Background())
	if err != nil {
		t.Fatalf("first Receive() error = %v", err)
	}
	second, err := queue.Receive(context.Background())
	if err != nil {
		t.Fatalf("second Receive() error = %v", err)
	}
	if first.AttemptID != "attempt-12" || second.AttemptID != "attempt-13" {
		t.Fatalf("batch results = %#v, %#v", first, second)
	}
	if fake.autoCalls != 1 {
		t.Fatalf("XAUTOCLAIM calls = %d, want 1 while draining local batch", fake.autoCalls)
	}
}

func TestValkeyQueueReceiveFallsBackWhenAutoClaimUnsupported(t *testing.T) {
	fake := &valkeyQueueFake{
		autoErr: errors.New("ERR unknown command 'XAUTOCLAIM'"),
		pending: []redis.XPendingExt{{ID: "8-0", Idle: time.Minute}},
		claim:   []redis.XMessage{{ID: "8-0", Values: map[string]interface{}{valkeyAttemptIDField: "attempt-8", valkeyIdempotencyField: "key-8"}}},
	}
	queue := NewValkeyQueue(fake, ValkeyQueueConfig{Stream: "delivery", Group: "workers", Consumer: "worker-1", ClaimIdle: time.Second, Block: time.Nanosecond})

	got, err := queue.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if got.AttemptID != "attempt-8" || got.Receipt != "8-0" {
		t.Fatalf("Receive() = %#v, want legacy claimed message", got)
	}
	if fake.claimCalls != 1 {
		t.Fatalf("XCLAIM calls = %d, want 1", fake.claimCalls)
	}
}

func TestValkeyQueueReceiveAcksPoisonEntry(t *testing.T) {
	fake := &valkeyQueueFake{
		autoMessages: []redis.XMessage{{ID: "9-0", Values: map[string]interface{}{"wrong": "field"}}},
		readStreams:  []redis.XStream{{Stream: "delivery", Messages: []redis.XMessage{{ID: "10-0", Values: map[string]interface{}{valkeyAttemptIDField: "attempt-10", valkeyIdempotencyField: "key-10"}}}}},
	}
	queue := NewValkeyQueue(fake, ValkeyQueueConfig{Stream: "delivery", Group: "workers", Consumer: "worker-1", Block: time.Nanosecond})

	got, err := queue.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if got.AttemptID != "attempt-10" {
		t.Fatalf("Receive() attempt = %q, want attempt-10", got.AttemptID)
	}
	if len(fake.acked) != 1 || fake.acked[0] != "9-0" {
		t.Fatalf("ACKed receipts = %#v, want poison receipt", fake.acked)
	}
}

func TestValkeyQueueReceiveDrainsClaimedBatchWithoutWaitingForIdle(t *testing.T) {
	fake := &valkeyQueueFake{
		autoMessages: []redis.XMessage{
			{ID: "11-0", Values: map[string]interface{}{valkeyAttemptIDField: "attempt-11", valkeyIdempotencyField: "key-11"}},
			{ID: "12-0", Values: map[string]interface{}{valkeyAttemptIDField: "attempt-12", valkeyIdempotencyField: "key-12"}},
		},
	}
	queue := NewValkeyQueue(fake, ValkeyQueueConfig{Stream: "delivery", Group: "workers", Consumer: "worker-1", Block: time.Nanosecond})

	first, err := queue.Receive(context.Background())
	if err != nil {
		t.Fatalf("first Receive() error = %v", err)
	}
	second, err := queue.Receive(context.Background())
	if err != nil {
		t.Fatalf("second Receive() error = %v", err)
	}
	if first.AttemptID != "attempt-11" || second.AttemptID != "attempt-12" {
		t.Fatalf("received attempts = %q, %q, want claimed batch order", first.AttemptID, second.AttemptID)
	}
	if fake.autoCalls != 1 {
		t.Fatalf("XAUTOCLAIM calls = %d, want buffered second item without another claim", fake.autoCalls)
	}
}

func TestValkeyQueueNackCarriesAvailableAtToAtomicScript(t *testing.T) {
	fake := &valkeyQueueFake{}
	queue := NewValkeyQueue(fake, ValkeyQueueConfig{Stream: "delivery", Group: "workers", Consumer: "worker-1"})
	availableAt := time.Unix(1_800_000_000, 123_000_000).UTC()

	if err := queue.Nack(context.Background(), "11-0", availableAt); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}
	if len(fake.doCalls) != 1 {
		t.Fatalf("EVAL calls = %d, want 1", len(fake.doCalls))
	}
	args := fake.doCalls[0]
	if len(args) != 9 || args[0] != "EVAL" || args[1] != nackScript || args[2] != 3 || args[3] != "delivery" || args[4] != "delivery:delayed" || args[5] != "delivery:delay" || args[6] != "workers" || args[7] != "11-0" || args[8] != availableAt.UnixMilli() {
		t.Fatalf("EVAL args = %#v, want stream/group/receipt/availableAt", args)
	}
	if !strings.Contains(nackScript, "ZADD") || !strings.Contains(nackScript, "XACK") {
		t.Fatal("Nack script does not atomically persist delay and settle receipt")
	}
	if !strings.Contains(nackScript, "if #entries == 0 then\n  -- The stream entry may have been trimmed") || !strings.Contains(nackScript, "redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])\n  return 0") {
		t.Fatal("Nack script does not clear a PEL receipt whose stream entry was trimmed")
	}
}

func TestValkeyQueueRejectsEmptyReceipt(t *testing.T) {
	queue := NewValkeyQueue(&valkeyQueueFake{}, ValkeyQueueConfig{})
	if err := queue.Ack(context.Background(), ""); !errors.Is(err, ErrValkeyQueueInvalidMessage) {
		t.Fatalf("Ack(empty) error = %v, want ErrValkeyQueueInvalidMessage", err)
	}
	if err := queue.Nack(context.Background(), "", time.Now()); !errors.Is(err, ErrValkeyQueueInvalidMessage) {
		t.Fatalf("Nack(empty) error = %v, want ErrValkeyQueueInvalidMessage", err)
	}
}

func TestValkeyQueueRejectsInvalidDelayKeyConfiguration(t *testing.T) {
	queue := NewValkeyQueue(&valkeyQueueFake{}, ValkeyQueueConfig{
		Stream:      "delivery",
		Group:       "workers",
		Consumer:    "worker-1",
		DelayStream: "delivery-delay",
		DelayKey:    "delivery-delay",
	})
	if err := queue.Ack(context.Background(), "1-0"); !errors.Is(err, ErrValkeyQueueInvalidConfig) {
		t.Fatalf("Ack() error = %v, want ErrValkeyQueueInvalidConfig", err)
	}
}

func TestRedisValueStringDoesNotTurnMissingFieldIntoIdentity(t *testing.T) {
	if got := redisValueString(nil); got != "" {
		t.Fatalf("redisValueString(nil) = %q, want empty", got)
	}
	if got := redisValueString(42); got != "" {
		t.Fatalf("redisValueString(number) = %q, want empty", got)
	}
}
