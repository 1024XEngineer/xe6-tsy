//go:build integration

package outbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/redis/go-redis/v9"
)

func TestValkeyWriterRedisClusterRoutingAndIdempotency(t *testing.T) {
	clusterURL := os.Getenv("REDIS_CLUSTER_URL")
	if clusterURL == "" {
		t.Skip("REDIS_CLUSTER_URL is required for the Redis Cluster integration test")
	}

	rawTag := make([]byte, 8)
	if _, err := rand.Read(rawTag); err != nil {
		t.Fatalf("generate isolated hash tag: %v", err)
	}
	tag := "lingow-outbox-it-" + hex.EncodeToString(rawTag)
	firstStream := "{" + tag + "}:mode:v1"
	secondStream := "{" + tag + "}:mode:v2"

	client, err := openRedisClient(clusterURL, RedisModeCluster)
	if err != nil {
		t.Fatalf("open Redis Cluster client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	cluster, ok := client.(*redis.ClusterClient)
	if !ok {
		t.Fatalf("client type = %T, want *redis.ClusterClient", client)
	}

	firstWriter, err := NewValkeyWriter(cluster, "", firstStream)
	if err != nil {
		t.Fatalf("create first writer: %v", err)
	}
	secondWriter, err := NewValkeyWriter(cluster, "", secondStream)
	if err != nil {
		t.Fatalf("create second writer: %v", err)
	}
	entry := clusterIntegrationEntry("event-1", []byte(`{"event_id":"event-1"}`))
	conflict := clusterIntegrationEntry("event-1", []byte(`{"event_id":"event-1","changed":true}`))
	dedupKeys := []string{
		firstWriter.dedupKey(firstStream, entry),
		secondWriter.dedupKey(secondStream, entry),
	}
	t.Cleanup(func() {
		for _, key := range append([]string{firstStream, secondStream}, dedupKeys...) {
			_ = cluster.Del(context.Background(), key).Err()
		}
	})

	for _, writer := range []*ValkeyWriter{firstWriter, secondWriter} {
		ack, err := writer.Accept(t.Context(), entry)
		if err != nil || !ack.Accepted {
			t.Fatalf("first cluster append = (%+v, %v)", ack, err)
		}
		ack, err = writer.Accept(t.Context(), entry)
		if err != nil || !ack.Accepted {
			t.Fatalf("idempotent cluster replay = (%+v, %v)", ack, err)
		}
	}
	if _, err := firstWriter.Accept(t.Context(), conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting cluster replay error = %v, want %v", err, ErrConflict)
	}
	for _, stream := range []string{firstStream, secondStream} {
		if length, err := cluster.XLen(t.Context(), stream).Result(); err != nil || length != 1 {
			t.Fatalf("XLEN %q = (%d, %v), want (1, nil)", stream, length, err)
		}
	}
}

func clusterIntegrationEntry(idempotencyKey string, payload []byte) Entry {
	return Entry{
		Topic:          realtimev1.ModeChangedTopic,
		IdempotencyKey: idempotencyKey,
		Payload:        payload,
		PayloadHash:    sha256.Sum256(payload),
	}
}
