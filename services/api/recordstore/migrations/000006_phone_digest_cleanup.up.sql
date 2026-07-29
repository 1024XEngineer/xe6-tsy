-- Accounts already upgraded to the peppered digest no longer need the legacy
-- SHA-256 identifier. Legacy-only rows remain available for lazy upgrade on
-- their next successful phone verification.
UPDATE lingow_accounts
SET phone_hash = NULL
WHERE kind = 'registered'
  AND phone_hash_v2 IS NOT NULL
  AND phone_hash IS NOT NULL;
