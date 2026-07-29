-- Durable input transport for final translation events.
--
-- The outbound delivery outbox is tied to delivery_attempts and cannot carry records events. This
-- table keeps the final event payload immutable while allowing receipt state to advance.
CREATE TABLE final_turn_outbox (
    event_id TEXT PRIMARY KEY,
    turn_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    sequence_no BIGINT NOT NULL,
    payload_hash BYTEA NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    receipt TEXT,
    locked_until TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT final_turn_outbox_event_id_not_empty CHECK (event_id <> ''),
    CONSTRAINT final_turn_outbox_turn_id_not_empty CHECK (turn_id <> ''),
    CONSTRAINT final_turn_outbox_session_id_not_empty CHECK (session_id <> ''),
    CONSTRAINT final_turn_outbox_sequence_positive CHECK (sequence_no > 0),
    CONSTRAINT final_turn_outbox_payload_hash_length CHECK (octet_length(payload_hash) = 32),
    CONSTRAINT final_turn_outbox_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT final_turn_outbox_status_valid CHECK (status IN ('pending', 'processing', 'acked', 'rejected')),
    CONSTRAINT final_turn_outbox_available_at_valid CHECK (available_at >= created_at),
    CONSTRAINT final_turn_outbox_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT final_turn_outbox_receipt_state_valid CHECK (
        (status = 'processing' AND receipt IS NOT NULL AND receipt <> '' AND locked_until IS NOT NULL)
        OR (status IN ('pending', 'acked', 'rejected') AND receipt IS NULL AND locked_until IS NULL)
    )
);

CREATE INDEX final_turn_outbox_available_idx
    ON final_turn_outbox (available_at ASC, created_at ASC, event_id ASC)
    WHERE status = 'pending';

CREATE INDEX final_turn_outbox_lease_idx
    ON final_turn_outbox (locked_until ASC, created_at ASC, event_id ASC)
    WHERE status = 'processing';

CREATE FUNCTION recordstore_reject_final_turn_outbox_payload_updates()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.event_id IS DISTINCT FROM OLD.event_id
        OR NEW.turn_id IS DISTINCT FROM OLD.turn_id
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.sequence_no IS DISTINCT FROM OLD.sequence_no
        OR NEW.payload_hash IS DISTINCT FROM OLD.payload_hash
        OR NEW.payload IS DISTINCT FROM OLD.payload
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'final turn outbox payload fields cannot be updated';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER final_turn_outbox_reject_payload_updates
    BEFORE UPDATE ON final_turn_outbox
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_final_turn_outbox_payload_updates();
