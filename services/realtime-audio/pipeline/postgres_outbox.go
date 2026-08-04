package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrPostgresFinalTurnOutboxConflict indicates reuse of an event ID with another payload.
	ErrPostgresFinalTurnOutboxConflict = errors.New("postgres final turn outbox payload conflict")
	// ErrPostgresFinalTurnOutboxPayload indicates an unsupported topic, key, or payload value.
	ErrPostgresFinalTurnOutboxPayload = errors.New("invalid postgres final turn outbox payload")
)

// PostgresFinalTurnOutbox publishes FinalTurnEvent values into the records service's durable
// input table. It deliberately accepts only the final-turn topic; usage facts use their own
// delivery boundary and must not be mixed into records persistence.
type PostgresFinalTurnOutbox struct {
	pool *pgxpool.Pool
}

func NewPostgresFinalTurnOutbox(pool *pgxpool.Pool) *PostgresFinalTurnOutbox {
	return &PostgresFinalTurnOutbox{pool: pool}
}

// Append implements DurableOutbox for the final-turn topic. Replays with the same event ID and
// payload are accepted, while a changed payload is rejected without overwriting the first event.
func (o *PostgresFinalTurnOutbox) Append(ctx context.Context, topic, idempotencyKey string, payload any) error {
	if o == nil || o.pool == nil {
		return ErrOutboxRequired
	}
	if topic != recordsv1.FinalTurnTopic || idempotencyKey == "" {
		return ErrPostgresFinalTurnOutboxPayload
	}
	event, ok := payload.(recordsv1.FinalTurnEvent)
	if !ok || event.EventID != idempotencyKey {
		return ErrPostgresFinalTurnOutboxPayload
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
	result, err := o.pool.Exec(ctx, postgresFinalTurnOutboxInsertQuery,
		event.EventID,
		event.TurnID,
		event.SessionID,
		event.SequenceNo,
		payloadHash[:],
		payloadJSON,
	)
	if err != nil {
		return fmt.Errorf("append final turn outbox: %w", err)
	}
	if result.RowsAffected() != 0 {
		return nil
	}

	var storedHash []byte
	if err := o.pool.QueryRow(ctx, postgresFinalTurnOutboxHashQuery, event.EventID).Scan(&storedHash); err != nil {
		return fmt.Errorf("read final turn outbox replay: %w", err)
	}
	matches, err := recordsv1.FinalTurnEventPayloadHashMatches(event, storedHash)
	if err != nil {
		return fmt.Errorf("hash final turn outbox replay: %w", err)
	}
	if !matches {
		return fmt.Errorf("%w: event ID %q", ErrPostgresFinalTurnOutboxConflict, event.EventID)
	}
	return nil
}

// NewPostgresFinalTurnSink wires the realtime final-turn port to the shared durable records table.
func NewPostgresFinalTurnSink(pool *pgxpool.Pool) *OutboxFinalTurnSink {
	return NewOutboxFinalTurnSink(NewPostgresFinalTurnOutbox(pool))
}

const postgresFinalTurnOutboxInsertQuery = `
INSERT INTO final_turn_outbox (
    event_id, turn_id, session_id, sequence_no, payload_hash, payload
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (event_id) DO NOTHING`

const postgresFinalTurnOutboxHashQuery = `
SELECT payload_hash
FROM final_turn_outbox
WHERE event_id = $1`

var _ DurableOutbox = (*PostgresFinalTurnOutbox)(nil)
