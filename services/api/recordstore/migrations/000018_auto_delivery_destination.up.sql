-- Persist the single automatic destination selected for each channel.
ALTER TABLE message_preferences
    ADD COLUMN IF NOT EXISTS destination_ref TEXT;

UPDATE message_preferences p
SET destination_ref = (
    SELECT d.destination_ref
    FROM account_destinations d
    WHERE d.account_id = p.account_id
      AND d.channel = p.channel
      AND d.verified_at IS NOT NULL
      AND d.revoked_at IS NULL
    ORDER BY d.verified_at DESC, d.destination_ref ASC
    LIMIT 1
)
WHERE p.destination_ref IS NULL;

ALTER TABLE message_preferences
    ADD CONSTRAINT message_preferences_destination_ref_valid
    CHECK (destination_ref IS NULL OR destination_ref <> '');
