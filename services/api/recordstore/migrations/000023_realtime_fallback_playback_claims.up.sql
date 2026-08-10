ALTER TABLE realtime_fallback_playback_operations
    ADD COLUMN status TEXT NOT NULL DEFAULT 'accepted',
    ADD COLUMN processing_started_at TIMESTAMPTZ,
    ALTER COLUMN accepted_at DROP NOT NULL;

ALTER TABLE realtime_fallback_playback_operations
    ADD CONSTRAINT realtime_fallback_playback_status_valid
        CHECK (status IN ('processing', 'accepted')),
    ADD CONSTRAINT realtime_fallback_playback_state_valid
        CHECK (
            (status = 'processing' AND accepted_at IS NULL AND processing_started_at IS NOT NULL)
            OR (status = 'accepted' AND accepted_at IS NOT NULL AND processing_started_at IS NULL)
        );

CREATE INDEX realtime_fallback_playback_processing_idx
    ON realtime_fallback_playback_operations (processing_started_at ASC)
    WHERE status = 'processing';
