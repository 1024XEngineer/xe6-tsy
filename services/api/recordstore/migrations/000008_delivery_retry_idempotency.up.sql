-- Manual retry keys are distinct from the idempotency key used to create a
-- message. Persist them separately so a create key can never be mistaken for
-- a retry, and scope uniqueness to the requesting account across all messages.
CREATE TABLE delivery_retry_requests (
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL,
    message_id TEXT NOT NULL REFERENCES outbound_messages (id) ON DELETE RESTRICT,
    attempt_id TEXT NOT NULL REFERENCES delivery_attempts (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT delivery_retry_requests_account_key PRIMARY KEY (account_id, idempotency_key),
    CONSTRAINT delivery_retry_requests_attempt_key UNIQUE (attempt_id),
    CONSTRAINT delivery_retry_requests_account_not_empty CHECK (account_id <> ''),
    CONSTRAINT delivery_retry_requests_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT delivery_retry_requests_message_not_empty CHECK (message_id <> ''),
    CONSTRAINT delivery_retry_requests_attempt_not_empty CHECK (attempt_id <> '')
);

CREATE INDEX delivery_retry_requests_message_created_idx
    ON delivery_retry_requests (message_id, created_at DESC);
