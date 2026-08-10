ALTER TABLE realtime_fallback_playback_operations
    ADD COLUMN processing_token TEXT;

UPDATE realtime_fallback_playback_operations
SET processing_token = md5(
    session_id || ':' || operation_id || ':' ||
    processing_started_at::TEXT || ':' || random()::TEXT
)
WHERE status = 'processing';

ALTER TABLE realtime_fallback_playback_operations
    DROP CONSTRAINT realtime_fallback_playback_state_valid,
    ADD CONSTRAINT realtime_fallback_playback_state_valid
        CHECK (
            (status = 'processing' AND accepted_at IS NULL AND processing_started_at IS NOT NULL
                AND processing_token IS NOT NULL AND processing_token <> '')
            OR (status = 'accepted' AND accepted_at IS NOT NULL AND processing_started_at IS NULL
                AND processing_token IS NULL)
        );
