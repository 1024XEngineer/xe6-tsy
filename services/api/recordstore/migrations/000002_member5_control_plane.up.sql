-- Control-plane storage for accounts, business sessions, usage, and delivery.
--
-- voice_turns and voice_session_language_configs predate voice_sessions and
-- intentionally retain their plain session_id columns. Adding foreign keys to
-- those tables here would reject valid historical/asynchronously-ingested
-- records, so this migration only makes new control-plane data referential.

CREATE TABLE lingow_accounts (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    phone_hash TEXT,
    merged_into TEXT REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT accounts_id_not_empty CHECK (id <> ''),
    CONSTRAINT accounts_kind_valid CHECK (kind IN ('anonymous', 'registered')),
    CONSTRAINT accounts_identity_valid CHECK (
        (kind = 'anonymous' AND phone_hash IS NULL)
        OR (kind = 'registered' AND phone_hash IS NOT NULL AND merged_into IS NULL)
    )
);

CREATE UNIQUE INDEX lingow_accounts_phone_hash_key
    ON lingow_accounts (phone_hash)
    WHERE phone_hash IS NOT NULL;

CREATE TABLE lingow_phone_challenges (
    id TEXT PRIMARY KEY,
    phone_hash TEXT NOT NULL,
    code_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT lingow_phone_challenges_id_not_empty CHECK (id <> ''),
    CONSTRAINT lingow_phone_challenges_phone_hash_not_empty CHECK (phone_hash <> ''),
    CONSTRAINT lingow_phone_challenges_code_hash_not_empty CHECK (code_hash <> ''),
    CONSTRAINT lingow_phone_challenges_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT lingow_phone_challenges_used_at_valid CHECK (used_at IS NULL OR used_at >= created_at)
);

CREATE INDEX lingow_phone_challenges_phone_expiry_idx
    ON lingow_phone_challenges (phone_hash, expires_at DESC);

CREATE TABLE lingow_auth_sessions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    refresh_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT lingow_auth_sessions_id_not_empty CHECK (id <> ''),
    CONSTRAINT lingow_auth_sessions_refresh_hash_not_empty CHECK (refresh_hash <> ''),
    CONSTRAINT lingow_auth_sessions_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT lingow_auth_sessions_revoked_at_valid CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE UNIQUE INDEX lingow_auth_sessions_refresh_hash_key
    ON lingow_auth_sessions (refresh_hash);

CREATE INDEX lingow_auth_sessions_active_refresh_lookup_idx
    ON lingow_auth_sessions (refresh_hash, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE voice_sessions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    status TEXT NOT NULL,
    audio_config JSONB NOT NULL,
    capabilities JSONB NOT NULL,
    failure_error_code TEXT,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT voice_sessions_id_not_empty CHECK (id <> ''),
    CONSTRAINT voice_sessions_status_valid CHECK (status IN ('created', 'active', 'ended', 'failed')),
    CONSTRAINT voice_sessions_config_objects CHECK (
        jsonb_typeof(audio_config) = 'object'
        AND jsonb_typeof(capabilities) = 'object'
    ),
    CONSTRAINT voice_sessions_timestamps_valid CHECK (
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
        OR (status = 'failed' AND started_at IS NOT NULL AND ended_at IS NULL AND failure_error_code IS NOT NULL)
    ),
    CONSTRAINT voice_sessions_failure_error_not_empty CHECK (failure_error_code IS NULL OR failure_error_code <> ''),
    CONSTRAINT voice_sessions_id_account_id_key UNIQUE (id, account_id)
);

CREATE INDEX voice_sessions_account_created_order_idx
    ON voice_sessions (account_id, created_at DESC, id DESC);

CREATE INDEX voice_sessions_account_status_created_order_idx
    ON voice_sessions (account_id, status, created_at DESC, id DESC);

CREATE TABLE voice_session_create_requests (
    account_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    session_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (account_id, idempotency_key),
    CONSTRAINT voice_session_create_requests_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT voice_session_create_requests_hash_not_empty CHECK (request_hash <> ''),
    CONSTRAINT voice_session_create_requests_session_key
        FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id)
        ON DELETE RESTRICT
);

-- A Start operation is durable before crossing the realtime boundary. Its
-- status, rather than an in-process lock, is the cross-instance authority for
-- activation and any compensating Stop call.
CREATE TABLE voice_session_start_operations (
    operation_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status TEXT NOT NULL,
    compensation_claim_id TEXT,
    started_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT voice_session_start_operations_id_not_empty CHECK (operation_id <> ''),
    CONSTRAINT voice_session_start_operations_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT voice_session_start_operations_hash_not_empty CHECK (request_hash <> ''),
    CONSTRAINT voice_session_start_operations_status_valid CHECK (
        status IN ('pending', 'compensating', 'completed', 'compensated', 'compensation_failed')
    ),
    CONSTRAINT voice_session_start_operations_claim_id_not_empty CHECK (
        compensation_claim_id IS NULL OR compensation_claim_id <> ''
    ),
    CONSTRAINT voice_session_start_operations_updated_at_valid CHECK (updated_at >= created_at),
    CONSTRAINT voice_session_start_operations_state_valid CHECK (
        (status = 'pending' AND started_at IS NULL AND compensation_claim_id IS NULL)
        OR (status = 'compensating' AND started_at IS NULL AND compensation_claim_id IS NOT NULL)
        OR (status = 'completed' AND started_at IS NOT NULL AND compensation_claim_id IS NULL)
        OR (
            status IN ('compensated', 'compensation_failed')
            AND started_at IS NULL
            AND compensation_claim_id IS NOT NULL
        )
    ),
    CONSTRAINT voice_session_start_operations_key_unique UNIQUE (account_id, idempotency_key),
    CONSTRAINT voice_session_start_operations_session_key
        FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id)
        ON DELETE RESTRICT
);

-- completed operations are replayable audit history. Every other status keeps
-- ownership unresolved, so exactly one may exist for a session at a time.
CREATE UNIQUE INDEX voice_session_start_operations_one_unfinished_per_session
    ON voice_session_start_operations (session_id)
    WHERE status IN ('pending', 'compensating', 'compensation_failed');

CREATE INDEX voice_session_start_operations_account_session_key_idx
    ON voice_session_start_operations (account_id, session_id, idempotency_key);

CREATE TABLE voice_session_end_intents (
    session_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (session_id, account_id),
    CONSTRAINT voice_session_end_intents_reason_valid CHECK (
        reason IN ('user_requested', 'operator_cancelled', 'client_disconnected')
    ),
    CONSTRAINT voice_session_end_intents_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT voice_session_end_intents_hash_not_empty CHECK (request_hash <> ''),
    CONSTRAINT voice_session_end_intents_completion_valid CHECK (completed_at IS NULL OR completed_at >= requested_at),
    CONSTRAINT voice_session_end_intents_key_unique UNIQUE (account_id, idempotency_key),
    CONSTRAINT voice_session_end_intents_session_key
        FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id)
        ON DELETE RESTRICT
);

CREATE TABLE lingow_usage_records (
    event_version INTEGER NOT NULL,
    event_id TEXT PRIMARY KEY,
    trace_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    account_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    service_type TEXT NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens BIGINT NOT NULL,
    output_tokens BIGINT NOT NULL,
    audio_duration_ms BIGINT NOT NULL,
    -- Providers may omit pricing for an event. NULL preserves that distinction
    -- from a measured zero-cost event while keeping aggregation safe.
    cost_amount NUMERIC(20, 8),
    currency TEXT,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT lingow_usage_records_event_id_not_empty CHECK (event_id <> ''),
    CONSTRAINT lingow_usage_records_event_version_valid CHECK (event_version = 1),
    CONSTRAINT lingow_usage_records_trace_id_not_empty CHECK (trace_id <> ''),
    CONSTRAINT lingow_usage_records_idempotency_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT lingow_usage_records_payload_hash_length CHECK (octet_length(payload_hash) = 32),
    CONSTRAINT lingow_usage_records_turn_id_not_empty CHECK (turn_id <> ''),
    CONSTRAINT lingow_usage_records_service_type_valid CHECK (service_type IN ('asr', 'translation', 'tts', 'diarization')),
    CONSTRAINT lingow_usage_records_provider_not_empty CHECK (provider <> ''),
    CONSTRAINT lingow_usage_records_model_not_empty CHECK (model <> ''),
    CONSTRAINT lingow_usage_records_measurements_nonnegative CHECK (
        input_tokens >= 0 AND output_tokens >= 0 AND audio_duration_ms >= 0
        AND (cost_amount IS NULL OR cost_amount >= 0)
    ),
    CONSTRAINT lingow_usage_records_currency_valid CHECK (
        currency IS NULL OR currency ~ '^[A-Z]{3}$'
    ),
    CONSTRAINT lingow_usage_records_session_key
        FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id)
        ON DELETE RESTRICT
);

CREATE UNIQUE INDEX lingow_usage_records_idempotency_key
    ON lingow_usage_records (idempotency_key);

CREATE INDEX lingow_usage_records_session_service_occurred_idx
    ON lingow_usage_records (session_id, service_type, occurred_at ASC, event_id ASC);

CREATE INDEX lingow_usage_records_account_occurred_idx
    ON lingow_usage_records (account_id, occurred_at ASC, event_id ASC);

CREATE TABLE account_destinations (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    channel TEXT NOT NULL,
    destination_ref TEXT NOT NULL,
    provider_target_ciphertext BYTEA NOT NULL,
    key_version TEXT NOT NULL,
    verified_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT account_destinations_id_not_empty CHECK (id <> ''),
    CONSTRAINT account_destinations_channel_valid CHECK (channel = 'email'),
    CONSTRAINT account_destinations_ref_not_empty CHECK (destination_ref <> ''),
    CONSTRAINT account_destinations_target_not_empty CHECK (octet_length(provider_target_ciphertext) > 0),
    CONSTRAINT account_destinations_key_version_not_empty CHECK (key_version <> ''),
    CONSTRAINT account_destinations_verification_valid CHECK (revoked_at IS NULL OR verified_at IS NOT NULL),
    CONSTRAINT account_destinations_revocation_valid CHECK (revoked_at IS NULL OR revoked_at >= verified_at),
    CONSTRAINT account_destinations_updated_at_valid CHECK (updated_at >= created_at),
    CONSTRAINT account_destinations_account_channel_ref_key UNIQUE (account_id, channel, destination_ref)
);

CREATE INDEX account_destinations_verified_lookup_idx
    ON account_destinations (account_id, channel, destination_ref)
    WHERE verified_at IS NOT NULL AND revoked_at IS NULL;

CREATE TABLE message_preferences (
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    channel TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (account_id, channel),
    CONSTRAINT message_preferences_channel_valid CHECK (channel = 'email')
);

CREATE TABLE outbound_messages (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL,
    channel TEXT NOT NULL,
    destination_ref TEXT NOT NULL,
    snapshot_version INTEGER NOT NULL,
    turns JSONB NOT NULL,
    status TEXT NOT NULL,
    attempts INTEGER NOT NULL,
    last_error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT outbound_messages_id_not_empty CHECK (id <> ''),
    CONSTRAINT outbound_messages_idempotency_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT outbound_messages_channel_valid CHECK (channel = 'email'),
    CONSTRAINT outbound_messages_destination_ref_not_empty CHECK (destination_ref <> ''),
    CONSTRAINT outbound_messages_snapshot_version_positive CHECK (snapshot_version >= 1),
    CONSTRAINT outbound_messages_turns_array CHECK (jsonb_typeof(turns) = 'array' AND jsonb_array_length(turns) > 0),
    CONSTRAINT outbound_messages_status_valid CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'retrying', 'cancelled')),
    CONSTRAINT outbound_messages_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT outbound_messages_last_error_not_empty CHECK (last_error_code IS NULL OR last_error_code <> ''),
    CONSTRAINT outbound_messages_updated_at_valid CHECK (updated_at >= created_at),
    CONSTRAINT outbound_messages_account_idempotency_key UNIQUE (account_id, idempotency_key)
);

CREATE INDEX outbound_messages_account_created_order_idx
    ON outbound_messages (account_id, created_at DESC, id DESC);

CREATE TABLE delivery_attempts (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES outbound_messages (id) ON DELETE RESTRICT,
    attempt_number INTEGER NOT NULL,
    status TEXT NOT NULL,
    error_code TEXT,
    next_attempt_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT delivery_attempts_id_not_empty CHECK (id <> ''),
    CONSTRAINT delivery_attempts_number_positive CHECK (attempt_number >= 1),
    CONSTRAINT delivery_attempts_status_valid CHECK (status IN ('queued', 'sending', 'succeeded', 'failed')),
    CONSTRAINT delivery_attempts_error_not_empty CHECK (error_code IS NULL OR error_code <> ''),
    CONSTRAINT delivery_attempts_timestamps_valid CHECK (
        (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR (started_at IS NOT NULL AND finished_at >= started_at))
        AND (next_attempt_at IS NULL OR next_attempt_at >= created_at)
    ),
    CONSTRAINT delivery_attempts_message_number_key UNIQUE (message_id, attempt_number)
);

CREATE INDEX delivery_attempts_message_order_idx
    ON delivery_attempts (message_id, attempt_number ASC);

CREATE INDEX delivery_attempts_queued_schedule_idx
    ON delivery_attempts (next_attempt_at ASC, created_at ASC, id ASC)
    WHERE status = 'queued';

CREATE TABLE delivery_outbox (
    id TEXT PRIMARY KEY,
    attempt_id TEXT NOT NULL REFERENCES delivery_attempts (id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL,
    topic TEXT NOT NULL,
    event_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT delivery_outbox_id_not_empty CHECK (id <> ''),
    CONSTRAINT delivery_outbox_idempotency_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT delivery_outbox_topic_not_empty CHECK (topic <> ''),
    CONSTRAINT delivery_outbox_event_key_not_empty CHECK (event_key <> ''),
    CONSTRAINT delivery_outbox_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT delivery_outbox_available_at_valid CHECK (available_at >= created_at),
    CONSTRAINT delivery_outbox_published_at_valid CHECK (published_at IS NULL OR published_at >= created_at),
    CONSTRAINT delivery_outbox_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT delivery_outbox_last_error_not_empty CHECK (last_error IS NULL OR last_error <> ''),
    -- attempt_id is the durable delivery identity. The API idempotency key is
    -- scoped to an account on outbound_messages and must not be global here.
    CONSTRAINT delivery_outbox_attempt_key UNIQUE (attempt_id)
);

CREATE INDEX delivery_outbox_unpublished_schedule_idx
    ON delivery_outbox (available_at ASC, created_at ASC, id ASC)
    WHERE published_at IS NULL;

CREATE FUNCTION recordstore_reject_usage_record_updates()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'usage records are immutable';
END;
$$;

CREATE TRIGGER lingow_usage_records_reject_updates
    BEFORE UPDATE ON lingow_usage_records
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_usage_record_updates();

CREATE FUNCTION recordstore_reject_outbound_message_snapshot_updates()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.account_id IS DISTINCT FROM OLD.account_id
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.channel IS DISTINCT FROM OLD.channel
        OR NEW.destination_ref IS DISTINCT FROM OLD.destination_ref
        OR NEW.snapshot_version IS DISTINCT FROM OLD.snapshot_version
        OR NEW.turns IS DISTINCT FROM OLD.turns
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'outbound message snapshot fields cannot be updated';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER outbound_messages_reject_snapshot_updates
    BEFORE UPDATE ON outbound_messages
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_outbound_message_snapshot_updates();
