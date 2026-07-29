-- Persist OTP failure state so invalid-code attempts cannot be retried forever.
ALTER TABLE lingow_phone_challenges
    ADD COLUMN attempts SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN max_attempts SMALLINT NOT NULL DEFAULT 5,
    ADD COLUMN last_attempt_at TIMESTAMPTZ;

ALTER TABLE lingow_phone_challenges
    ADD CONSTRAINT lingow_phone_challenges_attempts_valid
        CHECK (attempts >= 0 AND attempts <= max_attempts),
    ADD CONSTRAINT lingow_phone_challenges_max_attempts_valid
        CHECK (max_attempts BETWEEN 1 AND 10),
    ADD CONSTRAINT lingow_phone_challenges_last_attempt_valid
        CHECK (last_attempt_at IS NULL OR last_attempt_at >= created_at);

CREATE INDEX lingow_phone_challenges_phone_created_idx
    ON lingow_phone_challenges (phone_hash, created_at DESC);
