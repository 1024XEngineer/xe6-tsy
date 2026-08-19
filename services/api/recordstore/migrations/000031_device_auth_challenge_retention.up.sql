-- Retain only one usable challenge per device. The application returns that
-- challenge to retries, while this migration removes historical rows so a
-- public authentication endpoint cannot grow the table without bound.
DELETE FROM lingow_device_auth_challenges
WHERE used_at IS NOT NULL OR expires_at <= CURRENT_TIMESTAMP;

DELETE FROM lingow_device_auth_challenges AS older
USING lingow_device_auth_challenges AS newer
WHERE older.device_id = newer.device_id
  AND older.used_at IS NULL
  AND newer.used_at IS NULL
  AND (older.created_at, older.id) < (newer.created_at, newer.id);

CREATE UNIQUE INDEX lingow_device_auth_challenges_one_active_per_device
    ON lingow_device_auth_challenges (device_id)
    WHERE used_at IS NULL;
