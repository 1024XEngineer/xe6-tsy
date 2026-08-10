ALTER TABLE realtime_fallback_playback_operations
    DROP CONSTRAINT realtime_fallback_playback_status_valid,
    DROP CONSTRAINT realtime_fallback_playback_state_valid,
    ADD CONSTRAINT realtime_fallback_playback_status_valid
        CHECK (status IN ('processing', 'reclaimable', 'accepted')),
    ADD CONSTRAINT realtime_fallback_playback_state_valid
        CHECK (
            (status = 'processing' AND accepted_at IS NULL AND processing_started_at IS NOT NULL
                AND processing_token IS NOT NULL AND processing_token <> '')
            OR (status = 'reclaimable' AND accepted_at IS NULL AND processing_started_at IS NULL
                AND processing_token IS NULL)
            OR (status = 'accepted' AND accepted_at IS NOT NULL AND processing_started_at IS NULL
                AND processing_token IS NULL)
        );
