-- Durable work queue for asynchronous speaker attribution.
--
-- When a final turn is stored with pending or provisional attribution, the records writer
-- enqueues one task per turn in the same transaction. The attribution worker leases a task,
-- resolves a target participant, applies the correction through the records services, and
-- settles the task. Lease and retry state allow recovery after a worker process exits.
CREATE TABLE attribution_tasks (
    task_id TEXT PRIMARY KEY,
    turn_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    task_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    receipt TEXT,
    locked_until TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT attribution_tasks_task_id_not_empty CHECK (task_id <> ''),
    CONSTRAINT attribution_tasks_turn_id_not_empty CHECK (turn_id <> ''),
    CONSTRAINT attribution_tasks_session_id_not_empty CHECK (session_id <> ''),
    CONSTRAINT attribution_tasks_account_id_not_empty CHECK (account_id <> ''),
    CONSTRAINT attribution_tasks_task_type_valid CHECK (task_type IN ('participant_mapping', 'turn_attribution')),
    CONSTRAINT attribution_tasks_status_valid CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    CONSTRAINT attribution_tasks_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT attribution_tasks_available_at_valid CHECK (available_at >= created_at),
    CONSTRAINT attribution_tasks_receipt_state_valid CHECK (
        (status = 'processing' AND receipt IS NOT NULL AND receipt <> '' AND locked_until IS NOT NULL)
        OR (status IN ('pending', 'completed', 'failed') AND receipt IS NULL AND locked_until IS NULL)
    ),
    CONSTRAINT attribution_tasks_turn_id_key UNIQUE (turn_id)
);

CREATE INDEX attribution_tasks_available_idx
    ON attribution_tasks (available_at ASC, created_at ASC, task_id ASC)
    WHERE status = 'pending';

CREATE INDEX attribution_tasks_lease_idx
    ON attribution_tasks (locked_until ASC, created_at ASC, task_id ASC)
    WHERE status = 'processing';
