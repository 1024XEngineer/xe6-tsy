package outbox

import (
	"context"
	"sync"
)

// MemoryOutbox is an offline durable-writer fake with idempotent acceptance.
type MemoryOutbox struct {
	mu       sync.Mutex
	entries  []Entry
	byKey    map[string]Entry
	failNext error
}

// NewMemoryOutbox creates an empty fake ready for use as pipeline.DurableOutbox.
func NewMemoryOutbox() *MemoryOutbox {
	return &MemoryOutbox{byKey: make(map[string]Entry)}
}

// Append exposes the same boundary used by production sinks.
func (m *MemoryOutbox) Append(ctx context.Context, topic, idempotencyKey string, payload any) error {
	return NewAdapter(m).Append(ctx, topic, idempotencyKey, payload)
}

// Accept stores one canonical entry or returns an idempotent replay/conflict.
func (m *MemoryOutbox) Accept(ctx context.Context, entry Entry) (Ack, error) {
	if err := ctx.Err(); err != nil {
		return Ack{}, err
	}
	if m == nil {
		return Ack{}, ErrWriterRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext != nil {
		err := m.failNext
		m.failNext = nil
		return Ack{}, err
	}
	key := entry.Topic + "\x00" + entry.IdempotencyKey
	if existing, ok := m.byKey[key]; ok {
		if existing.PayloadHash != entry.PayloadHash {
			return Ack{}, ErrConflict
		}
		return Ack{Accepted: true}, nil
	}
	copy := cloneEntry(entry)
	m.byKey[key] = copy
	m.entries = append(m.entries, copy)
	return Ack{Accepted: true}, nil
}

// Entries returns immutable copies in acceptance order.
func (m *MemoryOutbox) Entries() []Entry {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := make([]Entry, len(m.entries))
	for i, entry := range m.entries {
		entries[i] = cloneEntry(entry)
	}
	return entries
}

// FailNext injects one writer failure for recovery tests.
func (m *MemoryOutbox) FailNext(err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.failNext = err
	m.mu.Unlock()
}

func cloneEntry(entry Entry) Entry {
	entry.Payload = append([]byte(nil), entry.Payload...)
	return entry
}

var _ Writer = (*MemoryOutbox)(nil)
