-- SHA-256 alone is unsafe for short OTPs and deterministic phone identifiers.
-- Keep historical account hashes only for lazy lookup of existing registered
-- accounts. New challenge rows hold an encrypted compatibility value and all
-- new durable account identifiers use the application-held HMAC pepper.
ALTER TABLE lingow_accounts
    ADD COLUMN phone_hash_v2 TEXT;

ALTER TABLE lingow_accounts
    DROP CONSTRAINT accounts_identity_valid;

ALTER TABLE lingow_accounts
    ADD CONSTRAINT accounts_identity_valid CHECK (
        (kind = 'anonymous' AND phone_hash IS NULL AND phone_hash_v2 IS NULL)
        OR (kind = 'registered' AND (phone_hash IS NOT NULL OR phone_hash_v2 IS NOT NULL) AND merged_into IS NULL)
    );

CREATE UNIQUE INDEX lingow_accounts_phone_hash_v2_key
    ON lingow_accounts (phone_hash_v2)
    WHERE phone_hash_v2 IS NOT NULL;

ALTER TABLE lingow_phone_challenges
    ADD COLUMN legacy_phone_hash TEXT,
    ADD COLUMN digest_version SMALLINT NOT NULL DEFAULT 1,
    ADD CONSTRAINT lingow_phone_challenges_digest_version_valid CHECK (digest_version IN (1, 2));

UPDATE lingow_phone_challenges
SET legacy_phone_hash = phone_hash
WHERE legacy_phone_hash IS NULL;

-- v1 challenges used the pre-v2 code digest and cannot be verified by the new
-- verifier. Expire them during the pre-production migration so clients obtain
-- a fresh v2 challenge instead of receiving an unexplained verification error.
UPDATE lingow_phone_challenges
SET expires_at = created_at + INTERVAL '1 second'
WHERE digest_version = 1;

ALTER TABLE lingow_phone_challenges
    ALTER COLUMN legacy_phone_hash SET NOT NULL;

CREATE INDEX lingow_phone_challenges_legacy_phone_created_idx
    ON lingow_phone_challenges (legacy_phone_hash, created_at DESC);
