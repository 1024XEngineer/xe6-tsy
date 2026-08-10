CREATE TABLE realtime_fallback_playback_operations (
    session_id TEXT NOT NULL REFERENCES voice_sessions (id) ON DELETE RESTRICT,
    operation_id TEXT NOT NULL,
    payload_hash TEXT NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, operation_id),
    CONSTRAINT realtime_fallback_playback_operation_not_empty CHECK (operation_id <> ''),
    CONSTRAINT realtime_fallback_playback_hash_not_empty CHECK (payload_hash <> '')
);
