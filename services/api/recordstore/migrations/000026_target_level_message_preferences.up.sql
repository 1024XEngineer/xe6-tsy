-- A delivery preference belongs to one verified destination, not to an entire
-- channel. Rows without a destination could never schedule a delivery and are
-- intentionally discarded before making the target identity mandatory.
DELETE FROM message_preferences
WHERE destination_ref IS NULL;

ALTER TABLE message_preferences
    DROP CONSTRAINT IF EXISTS message_preferences_pkey;

ALTER TABLE message_preferences
    ALTER COLUMN destination_ref SET NOT NULL;

ALTER TABLE message_preferences
    ADD CONSTRAINT message_preferences_pkey
    PRIMARY KEY (account_id, channel, destination_ref);
