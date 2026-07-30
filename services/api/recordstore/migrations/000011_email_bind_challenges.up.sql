-- Short-lived email ownership challenges for message-target bind verification.
CREATE TABLE email_bind_challenges (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    destination_ref TEXT NOT NULL,
    email TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT email_bind_challenges_id_not_empty CHECK (id <> ''),
    CONSTRAINT email_bind_challenges_account_not_empty CHECK (account_id <> ''),
    CONSTRAINT email_bind_challenges_ref_not_empty CHECK (destination_ref <> ''),
    CONSTRAINT email_bind_challenges_email_not_empty CHECK (email <> ''),
    CONSTRAINT email_bind_challenges_token_hash_not_empty CHECK (token_hash <> ''),
    CONSTRAINT email_bind_challenges_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT email_bind_challenges_used_at_valid CHECK (used_at IS NULL OR used_at >= created_at)
);

CREATE UNIQUE INDEX email_bind_challenges_token_hash_key ON email_bind_challenges (token_hash);

CREATE INDEX email_bind_challenges_account_active_idx
    ON email_bind_challenges (account_id, created_at DESC)
    WHERE used_at IS NULL;
