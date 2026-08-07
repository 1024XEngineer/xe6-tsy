CREATE TABLE automatic_turn_runs (
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    turn_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    trace_id TEXT NOT NULL,
    target_language TEXT NOT NULL,
    translated_text TEXT NOT NULL,
    language_config_version BIGINT NOT NULL,
    status TEXT NOT NULL,
    target_count INTEGER NOT NULL,
    settled_count INTEGER NOT NULL DEFAULT 0,
    succeeded_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    fallback_operation_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (account_id, turn_id),
    CONSTRAINT automatic_turn_runs_turn_not_empty CHECK (turn_id <> ''),
    CONSTRAINT automatic_turn_runs_session_not_empty CHECK (session_id <> ''),
    CONSTRAINT automatic_turn_runs_trace_not_empty CHECK (trace_id <> ''),
    CONSTRAINT automatic_turn_runs_language_not_empty CHECK (target_language <> ''),
    CONSTRAINT automatic_turn_runs_translation_not_empty CHECK (translated_text <> ''),
    CONSTRAINT automatic_turn_runs_config_version_positive CHECK (language_config_version >= 1),
    CONSTRAINT automatic_turn_runs_status_valid CHECK (status IN ('pending', 'succeeded', 'failed', 'fallback_pending', 'fallback_played', 'restored')),
    CONSTRAINT automatic_turn_runs_counts_valid CHECK (
        target_count >= 0
        AND settled_count >= 0
        AND succeeded_count >= 0
        AND failed_count >= 0
        AND settled_count = succeeded_count + failed_count
        AND settled_count <= target_count
    ),
    CONSTRAINT automatic_turn_runs_fallback_not_empty CHECK (fallback_operation_id <> ''),
    CONSTRAINT automatic_turn_runs_updated_at_valid CHECK (updated_at >= created_at)
);

CREATE INDEX automatic_turn_runs_session_order_idx
    ON automatic_turn_runs (session_id, created_at ASC, turn_id ASC);

CREATE TABLE automatic_turn_settlements (
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    turn_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    target_language TEXT NOT NULL,
    channel TEXT NOT NULL,
    destination_ref TEXT NOT NULL,
    status TEXT NOT NULL,
    message_id TEXT REFERENCES outbound_messages (id) ON DELETE RESTRICT,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT automatic_turn_settlements_run_fk FOREIGN KEY (account_id, turn_id)
        REFERENCES automatic_turn_runs (account_id, turn_id) ON DELETE RESTRICT,
    CONSTRAINT automatic_turn_settlements_identity_key UNIQUE (turn_id, channel, destination_ref),
    CONSTRAINT automatic_turn_settlements_account_not_empty CHECK (account_id <> ''),
    CONSTRAINT automatic_turn_settlements_turn_not_empty CHECK (turn_id <> ''),
    CONSTRAINT automatic_turn_settlements_session_not_empty CHECK (session_id <> ''),
    CONSTRAINT automatic_turn_settlements_language_not_empty CHECK (target_language <> ''),
    CONSTRAINT automatic_turn_settlements_channel_valid CHECK (channel IN ('email', 'wechat')),
    CONSTRAINT automatic_turn_settlements_destination_not_empty CHECK (destination_ref <> ''),
    CONSTRAINT automatic_turn_settlements_status_valid CHECK (status IN ('queued', 'succeeded', 'failed')),
    CONSTRAINT automatic_turn_settlements_error_not_empty CHECK (error_code IS NULL OR error_code <> ''),
    CONSTRAINT automatic_turn_settlements_updated_at_valid CHECK (updated_at >= created_at)
);

CREATE INDEX automatic_turn_settlements_account_turn_idx
    ON automatic_turn_settlements (account_id, turn_id, channel, destination_ref);
