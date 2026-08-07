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
