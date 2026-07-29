package recordstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

const (
	finalTurnOutboxPollInterval  = 100 * time.Millisecond
	finalTurnOutboxSettleTimeout = 5 * time.Second
)

var (
	ErrFinalTurnOutboxRequired = errors.New("final turn outbox is required")
	ErrFinalTurnOutboxConflict = errors.New("final turn outbox payload conflict")
	ErrFinalTurnOutboxPayload  = errors.New("invalid final turn outbox payload")
)

// FinalTurnOutbox stores final events and exposes receipt-based delivery to the records worker.
// Payload fields are immutable after Append; only the delivery state and lease are mutable.
type FinalTurnOutbox struct {
	pool *pgxpool.Pool
}

func NewFinalTurnOutbox(pool *pgxpool.Pool) *FinalTurnOutbox {
	return &FinalTurnOutbox{pool: pool}
}

// Append implements the durable publisher used by the realtime final-turn sink. The event ID is
// the outbox identity, and an existing ID is accepted only when its complete payload is identical.
func (o *FinalTurnOutbox) Append(ctx context.Context, topic, idempotencyKey string, payload any) error {
	if o == nil || o.pool == nil {
		return ErrFinalTurnOutboxRequired
	}
	if topic != recordsv1.FinalTurnTopic || idempotencyKey == "" {
		return fmt.Errorf("%w: topic or idempotency key", ErrFinalTurnOutboxPayload)
	}
	event, ok := payload.(recordsv1.FinalTurnEvent)
	if !ok || event.EventID != idempotencyKey {
		return fmt.Errorf("%w: payload must be FinalTurnEvent with matching event ID", ErrFinalTurnOutboxPayload)
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payloadJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal final turn outbox payload: %w", err)
	}
	payloadHash, err := recordsv1.FinalTurnEventPayloadHash(event)
	if err != nil {
		return err
	}

	result, err := o.pool.Exec(ctx, insertFinalTurnOutboxQuery,
		event.EventID,
		event.TurnID,
		event.SessionID,
		event.SequenceNo,
		payloadHash[:],
		payloadJSON,
	)
	if err != nil {
		return fmt.Errorf("append final turn outbox: %w", MapError(err))
	}
	if result.RowsAffected() != 0 {
		return nil
	}

	var storedHash []byte
	if err := o.pool.QueryRow(ctx, finalTurnOutboxHashQuery, event.EventID).Scan(&storedHash); err != nil {
		return fmt.Errorf("read final turn outbox replay: %w", MapError(err))
	}
	if !bytes.Equal(storedHash, payloadHash[:]) {
		return fmt.Errorf("%w: %w", ErrFinalTurnOutboxConflict, domain.ErrConflict)
	}
	return nil
}

// Receive waits for one available event and leases it to the caller. An expired processing lease
// is eligible again, which recovers events after a worker process exits before settlement.
func (o *FinalTurnOutbox) Receive(ctx context.Context) (turns.FinalTurnDelivery, error) {
	if o == nil || o.pool == nil {
		return nil, ErrFinalTurnOutboxRequired
	}
	for {
		delivery, found, err := o.receiveOnce(ctx)
		if err != nil {
			return nil, err
		}
		if found {
			return delivery, nil
		}

		timer := time.NewTimer(finalTurnOutboxPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (o *FinalTurnOutbox) receiveOnce(ctx context.Context) (turns.FinalTurnDelivery, bool, error) {
	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin final turn outbox receive: %w", err)
	}
	defer tx.Rollback(ctx)

	receipt := ulid.Make().String()
	var (
		eventID string
		payload []byte
	)
	err = tx.QueryRow(ctx, claimFinalTurnOutboxQuery, receipt).Scan(&eventID, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("claim final turn outbox event: %w", err)
	}

	var event recordsv1.FinalTurnEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, false, fmt.Errorf("decode final turn outbox event: %w", err)
	}
	if err := event.Validate(); err != nil {
		return nil, false, fmt.Errorf("validate final turn outbox event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit final turn outbox claim: %w", err)
	}
	return &finalTurnDelivery{
		outbox:  o,
		ctx:     context.WithoutCancel(ctx),
		event:   event,
		eventID: eventID,
		receipt: receipt,
	}, true, nil
}

type finalTurnDelivery struct {
	outbox  *FinalTurnOutbox
	ctx     context.Context
	event   recordsv1.FinalTurnEvent
	eventID string
	receipt string
}

func (d *finalTurnDelivery) Event() recordsv1.FinalTurnEvent { return d.event }

func (d *finalTurnDelivery) Ack() error {
	return d.outbox.settle(d.ctx, d.eventID, d.receipt, "acked")
}

func (d *finalTurnDelivery) Nack() error {
	return d.outbox.settle(d.ctx, d.eventID, d.receipt, "pending")
}

func (d *finalTurnDelivery) Reject() error {
	return d.outbox.settle(d.ctx, d.eventID, d.receipt, "rejected")
}

func (o *FinalTurnOutbox) settle(parent context.Context, eventID, receipt, status string) error {
	ctx, cancel := context.WithTimeout(parent, finalTurnOutboxSettleTimeout)
	defer cancel()

	var rowsAffected int64
	if status == "pending" {
		result, err := o.pool.Exec(ctx, nackFinalTurnOutboxQuery, eventID, receipt)
		if err != nil {
			return fmt.Errorf("settle final turn outbox event: %w", err)
		}
		rowsAffected = result.RowsAffected()
	} else {
		result, err := o.pool.Exec(ctx, settleFinalTurnOutboxQuery, status, eventID, receipt)
		if err != nil {
			return fmt.Errorf("settle final turn outbox event: %w", err)
		}
		rowsAffected = result.RowsAffected()
	}
	if rowsAffected != 1 {
		return fmt.Errorf("settle final turn outbox event: receipt is no longer active")
	}
	return nil
}

const insertFinalTurnOutboxQuery = `
INSERT INTO final_turn_outbox (
    event_id, turn_id, session_id, sequence_no, payload_hash, payload
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (event_id) DO NOTHING`

const finalTurnOutboxHashQuery = `
SELECT payload_hash
FROM final_turn_outbox
WHERE event_id = $1`

const claimFinalTurnOutboxQuery = `
WITH candidate AS (
    SELECT event_id
    FROM final_turn_outbox
    WHERE (status = 'pending' AND available_at <= CURRENT_TIMESTAMP)
       OR (status = 'processing' AND locked_until <= CURRENT_TIMESTAMP)
    ORDER BY available_at ASC, created_at ASC, event_id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE final_turn_outbox AS outbox
SET status = 'processing',
    receipt = $1,
    locked_until = CURRENT_TIMESTAMP + INTERVAL '1 minute',
    attempts = outbox.attempts + 1
FROM candidate
WHERE outbox.event_id = candidate.event_id
RETURNING outbox.event_id, outbox.payload`

const settleFinalTurnOutboxQuery = `
UPDATE final_turn_outbox
SET status = $1,
    receipt = NULL,
    locked_until = NULL
WHERE event_id = $2 AND receipt = $3 AND status = 'processing'`

const nackFinalTurnOutboxQuery = `
UPDATE final_turn_outbox
SET status = 'pending',
    available_at = CURRENT_TIMESTAMP + INTERVAL '1 second',
    receipt = NULL,
    locked_until = NULL
WHERE event_id = $1 AND receipt = $2 AND status = 'processing'`

var _ turns.FinalTurnDeliverySource = (*FinalTurnOutbox)(nil)
