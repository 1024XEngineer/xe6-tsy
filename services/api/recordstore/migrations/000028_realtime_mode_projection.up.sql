CREATE TABLE realtime_mode_events (
    event_id TEXT PRIMARY KEY,
    payload_hash BYTEA NOT NULL,
    event_version INTEGER NOT NULL,
    trace_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    runtime_instance_id TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    from_mode TEXT NOT NULL,
    to_mode TEXT NOT NULL,
    resulting_generation BIGINT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT realtime_mode_events_event_id_not_empty CHECK (event_id <> ''),
    CONSTRAINT realtime_mode_events_payload_hash_length CHECK (octet_length(payload_hash) = 32),
    CONSTRAINT realtime_mode_events_version_valid CHECK (event_version = 1),
    CONSTRAINT realtime_mode_events_trace_id_not_empty CHECK (trace_id <> ''),
    CONSTRAINT realtime_mode_events_session_id_not_empty CHECK (session_id <> ''),
    CONSTRAINT realtime_mode_events_runtime_id_not_empty CHECK (runtime_instance_id <> ''),
    CONSTRAINT realtime_mode_events_operation_id_not_empty CHECK (operation_id <> ''),
    CONSTRAINT realtime_mode_events_from_mode_valid CHECK (from_mode IN ('assistant', 'interpretation')),
    CONSTRAINT realtime_mode_events_to_mode_valid CHECK (to_mode IN ('assistant', 'interpretation')),
    CONSTRAINT realtime_mode_events_mode_changed CHECK (from_mode <> to_mode),
    CONSTRAINT realtime_mode_events_generation_positive CHECK (resulting_generation >= 2),
    CONSTRAINT realtime_mode_events_session_key FOREIGN KEY (session_id)
        REFERENCES voice_sessions (id) ON DELETE RESTRICT
);

CREATE INDEX realtime_mode_events_session_occurred_idx
    ON realtime_mode_events (session_id, occurred_at ASC, event_id ASC);

CREATE FUNCTION recordstore_reject_realtime_mode_event_mutations()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'realtime mode events are immutable';
END;
$$;

CREATE TRIGGER realtime_mode_events_reject_mutations
    BEFORE UPDATE OR DELETE ON realtime_mode_events
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_realtime_mode_event_mutations();

-- This is a latest-observed audit projection only; realtime remains authoritative. Ordering is
-- generation-based within one runtime and occurred_at-based across runtimes. The repository uses
-- event_id as a deterministic tie-breaker when two runtimes report the same occurred_at value.
CREATE TABLE realtime_mode_projections (
    session_id TEXT PRIMARY KEY,
    runtime_instance_id TEXT NOT NULL,
    active_mode TEXT NOT NULL,
    generation BIGINT NOT NULL,
    last_event_id TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT realtime_mode_projections_session_id_not_empty CHECK (session_id <> ''),
    CONSTRAINT realtime_mode_projections_runtime_id_not_empty CHECK (runtime_instance_id <> ''),
    CONSTRAINT realtime_mode_projections_mode_valid CHECK (active_mode IN ('assistant', 'interpretation')),
    CONSTRAINT realtime_mode_projections_generation_positive CHECK (generation >= 2),
    CONSTRAINT realtime_mode_projections_last_event_key FOREIGN KEY (last_event_id)
        REFERENCES realtime_mode_events (event_id) ON DELETE RESTRICT,
    CONSTRAINT realtime_mode_projections_session_key FOREIGN KEY (session_id)
        REFERENCES voice_sessions (id) ON DELETE RESTRICT
);

CREATE INDEX realtime_mode_projections_runtime_idx
    ON realtime_mode_projections (runtime_instance_id, updated_at DESC);
