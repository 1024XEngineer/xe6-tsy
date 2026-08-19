-- Devices are concrete hardware instances. product_id is descriptive only;
-- every authentication decision is tied to the instance public key and binding.
CREATE TABLE lingow_devices (
    device_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    public_key BYTEA NOT NULL,
    account_id TEXT REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'active',
    bound_at TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMPTZ,
    CONSTRAINT lingow_devices_id_not_empty CHECK (device_id <> ''),
    CONSTRAINT lingow_devices_product_id_not_empty CHECK (product_id <> ''),
    CONSTRAINT lingow_devices_public_key_ed25519 CHECK (octet_length(public_key) = 32),
    CONSTRAINT lingow_devices_status_valid CHECK (status IN ('active', 'revoked')),
    CONSTRAINT lingow_devices_binding_valid CHECK (
        (account_id IS NULL AND bound_at IS NULL) OR (account_id IS NOT NULL AND bound_at IS NOT NULL)
    ),
    CONSTRAINT lingow_devices_revocation_valid CHECK (
        (status = 'active' AND revoked_at IS NULL) OR (status = 'revoked' AND revoked_at IS NOT NULL)
    )
);

CREATE INDEX lingow_devices_account_idx ON lingow_devices (account_id, created_at DESC) WHERE account_id IS NOT NULL;

CREATE TABLE lingow_device_pairing_codes (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    code_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT lingow_device_pairing_codes_hash_length CHECK (octet_length(code_hash) = 32),
    CONSTRAINT lingow_device_pairing_codes_expiry_valid CHECK (expires_at > created_at)
);

CREATE INDEX lingow_device_pairing_codes_active_lookup_idx
    ON lingow_device_pairing_codes (account_id, expires_at DESC)
    WHERE used_at IS NULL;

CREATE TABLE lingow_device_auth_challenges (
    id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES lingow_devices (device_id) ON DELETE RESTRICT,
    nonce TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT lingow_device_auth_challenges_nonce_not_empty CHECK (nonce <> ''),
    CONSTRAINT lingow_device_auth_challenges_expiry_valid CHECK (expires_at > created_at)
);

CREATE INDEX lingow_device_auth_challenges_device_active_idx
    ON lingow_device_auth_challenges (device_id, expires_at DESC)
    WHERE used_at IS NULL;

-- A device credential can operate only sessions it explicitly created.
CREATE TABLE lingow_device_voice_sessions (
    device_id TEXT NOT NULL REFERENCES lingow_devices (device_id) ON DELETE RESTRICT,
    session_id TEXT NOT NULL REFERENCES voice_sessions (id) ON DELETE RESTRICT,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (device_id, session_id),
    CONSTRAINT lingow_device_voice_sessions_account_match
        FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id)
        ON DELETE RESTRICT
);

CREATE INDEX lingow_device_voice_sessions_session_idx ON lingow_device_voice_sessions (session_id, device_id);
