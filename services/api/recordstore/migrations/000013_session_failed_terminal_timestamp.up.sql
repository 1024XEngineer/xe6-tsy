UPDATE voice_sessions
SET ended_at = started_at
WHERE status = 'failed' AND ended_at IS NULL;

ALTER TABLE voice_sessions
    DROP CONSTRAINT IF EXISTS voice_sessions_timestamps_valid;

ALTER TABLE voice_sessions
    ADD CONSTRAINT voice_sessions_timestamps_valid CHECK (
        (status = 'created' AND started_at IS NULL AND ended_at IS NULL AND failure_error_code IS NULL)
        OR (status = 'active' AND started_at IS NOT NULL AND ended_at IS NULL AND failure_error_code IS NULL)
        OR (
            status = 'ended'
            AND ended_at IS NOT NULL
            AND failure_error_code IS NULL
            AND (
                (started_at IS NULL AND ended_at >= created_at)
                OR (started_at IS NOT NULL AND ended_at >= started_at)
            )
        )
        OR (
            status = 'failed'
            AND started_at IS NOT NULL
            AND ended_at IS NOT NULL
            AND ended_at >= started_at
            AND failure_error_code IS NOT NULL
        )
    );
