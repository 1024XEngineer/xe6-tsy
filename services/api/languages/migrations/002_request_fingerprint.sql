-- Persist idempotency request fingerprints so replay compares the full body
-- (language pairs + expected_version), not only session_id + collapsed pairs.

ALTER TABLE voice_session_language_configs
    ADD COLUMN IF NOT EXISTS request_fingerprint VARCHAR(128);
