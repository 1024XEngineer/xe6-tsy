ALTER TABLE voice_session_end_intents
    ADD COLUMN trace_id TEXT,
    ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_error TEXT,
    ADD COLUMN next_attempt_at TIMESTAMPTZ,
    ADD COLUMN recovery_owner TEXT,
    ADD COLUMN recovery_lease_expires_at TIMESTAMPTZ;

UPDATE voice_session_end_intents
SET trace_id = 'end-recovery-' || idempotency_key,
    next_attempt_at = LEAST(requested_at, clock_timestamp());

ALTER TABLE voice_session_end_intents
    ALTER COLUMN trace_id SET NOT NULL,
    ALTER COLUMN next_attempt_at SET NOT NULL,
    ADD CONSTRAINT voice_session_end_intents_trace_not_empty CHECK (trace_id <> ''),
    ADD CONSTRAINT voice_session_end_intents_retry_count_valid CHECK (retry_count >= 0),
    ADD CONSTRAINT voice_session_end_intents_recovery_lease_valid CHECK (
        (recovery_owner IS NULL AND recovery_lease_expires_at IS NULL)
        OR (
            recovery_owner IS NOT NULL
            AND recovery_owner <> ''
            AND recovery_lease_expires_at IS NOT NULL
        )
    );

CREATE INDEX voice_session_end_intents_recovery_due_idx
    ON voice_session_end_intents (next_attempt_at, requested_at, session_id)
    WHERE completed_at IS NULL;
