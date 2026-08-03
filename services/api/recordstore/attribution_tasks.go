package recordstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

const (
	attributionTaskPending    = "pending"
	attributionTaskProcessing = "processing"
	attributionTaskCompleted  = "completed"
	attributionTaskFailed     = "failed"

	attributionTaskPollInterval  = 100 * time.Millisecond
	attributionTaskSettleTimeout = 5 * time.Second
	attributionTaskBackoff       = 1 * time.Second
	attributionTaskMaxBackoff    = 2 * time.Minute
)

var (
	ErrAttributionTaskRequired = errors.New("attribution task store is required")
)

// AttributionTaskStore leases and settles attribution tasks in PostgreSQL. Enqueue happens inside
// the final-turn transaction (see TurnWriter) so task creation and record storage are atomic.
type AttributionTaskStore struct {
	pool *pgxpool.Pool
}

func NewAttributionTaskStore(pool *pgxpool.Pool) *AttributionTaskStore {
	return &AttributionTaskStore{pool: pool}
}

// Enqueue inserts one task per turn inside the caller's transaction. A duplicate turn is a no-op
// because attribution is re-resolved from the persisted record, not from a frozen payload.
func (s *AttributionTaskStore) Enqueue(ctx context.Context, tx pgx.Tx, turnID, sessionID string) error {
	if s == nil || s.pool == nil {
		return ErrAttributionTaskRequired
	}
	_, err := tx.Exec(ctx, insertAttributionTaskQuery, attributionTaskID(turnID), turnID, sessionID)
	if err != nil {
		return fmt.Errorf("enqueue attribution task: %w", MapError(err))
	}
	return nil
}

// Receive waits for one due task and leases it. An expired lease is eligible again so a task
// survives a worker process that exits before settlement.
func (s *AttributionTaskStore) Receive(ctx context.Context) (turns.AttributionTaskDelivery, error) {
	if s == nil || s.pool == nil {
		return nil, ErrAttributionTaskRequired
	}
	for {
		delivery, found, err := s.receiveOnce(ctx)
		if err != nil {
			return nil, err
		}
		if found {
			return delivery, nil
		}
		timer := time.NewTimer(attributionTaskPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *AttributionTaskStore) receiveOnce(ctx context.Context) (turns.AttributionTaskDelivery, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin attribution task claim: %w", err)
	}
	defer tx.Rollback(ctx)

	receipt := ulid.Make().String()
	task := turns.AttributionTask{}
	err = tx.QueryRow(ctx, claimAttributionTaskQuery, receipt).Scan(
		&task.TaskID, &task.TurnID, &task.SessionID, &task.AccountID, &task.TaskType, &task.Attempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("claim attribution task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit attribution task claim: %w", err)
	}
	return &attributionTaskDelivery{store: s, task: task, receipt: receipt}, true, nil
}

type attributionTaskDelivery struct {
	store   *AttributionTaskStore
	task    turns.AttributionTask
	receipt string
}

func (d *attributionTaskDelivery) Task() turns.AttributionTask { return d.task }

func (d *attributionTaskDelivery) Ack() error {
	return d.store.settle(d.task.TaskID, d.receipt, attributionTaskCompleted, nil, nil)
}

func (d *attributionTaskDelivery) Retry(lastError string) error {
	availableAt := time.Now().Add(retryBackoff(d.task.Attempts))
	return d.store.settle(d.task.TaskID, d.receipt, attributionTaskPending, &lastError, &availableAt)
}

// retryBackoff grows exponentially with attempts and is capped so a poison task never busy-loops
// the queue. The caller already bounds the retry count via the worker's max-attempt policy.
func retryBackoff(attempts int) time.Duration {
	delay := attributionTaskBackoff * time.Duration(1<<(attempts-1))
	if delay > attributionTaskMaxBackoff {
		return attributionTaskMaxBackoff
	}
	return delay
}

func (d *attributionTaskDelivery) Fail(lastError string) error {
	return d.store.settle(d.task.TaskID, d.receipt, attributionTaskFailed, &lastError, nil)
}

func (s *AttributionTaskStore) settle(taskID, receipt, status string, lastError *string, availableAt *time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), attributionTaskSettleTimeout)
	defer cancel()

	rowsAffected, err := settleAttributionTask(ctx, s.pool, taskID, receipt, status, lastError, availableAt)
	if err != nil {
		return fmt.Errorf("settle attribution task: %w", err)
	}
	if rowsAffected != 1 {
		var currentStatus string
		if err := s.pool.QueryRow(ctx, `SELECT status FROM attribution_tasks WHERE task_id = $1`, taskID).Scan(&currentStatus); err != nil {
			return fmt.Errorf("settle attribution task: receipt is no longer active: %w", MapError(err))
		}
		return nil
	}
	return nil
}

var _ turns.AttributionTaskSource = (*AttributionTaskStore)(nil)

const insertAttributionTaskQuery = `
INSERT INTO attribution_tasks (task_id, turn_id, session_id, account_id, task_type)
SELECT $1, $2, $3, COALESCE(owner.merged_into, owner.id), 'turn_attribution'
FROM voice_sessions AS sessions
JOIN lingow_accounts AS owner ON owner.id = sessions.account_id
WHERE sessions.id = $3
ON CONFLICT (turn_id) DO NOTHING`

const claimAttributionTaskQuery = `
WITH candidate AS (
    SELECT task_id
    FROM attribution_tasks
    WHERE (status = 'pending' AND available_at <= CURRENT_TIMESTAMP)
       OR (status = 'processing' AND locked_until <= CURRENT_TIMESTAMP)
    ORDER BY available_at ASC, created_at ASC, task_id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE attribution_tasks AS task
SET status = 'processing',
    receipt = $1,
    locked_until = CURRENT_TIMESTAMP + INTERVAL '1 minute',
    attempts = task.attempts + 1
FROM candidate
WHERE task.task_id = candidate.task_id
RETURNING task.task_id, task.turn_id, task.session_id, task.account_id, task.task_type, task.attempts`

func attributionTaskID(turnID string) string {
	return "attr_" + turnID
}

func settleAttributionTask(
	ctx context.Context,
	pool *pgxpool.Pool,
	taskID, receipt, status string,
	lastError *string,
	availableAt *time.Time,
) (int64, error) {
	result, err := pool.Exec(ctx, settleAttributionTaskQuery,
		status, lastError, availableAt, taskID, receipt)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

const settleAttributionTaskQuery = `
UPDATE attribution_tasks
SET status = $1,
    last_error = $2,
    available_at = COALESCE($3, available_at),
    receipt = NULL,
    locked_until = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE task_id = $4 AND receipt = $5 AND status = 'processing'`
