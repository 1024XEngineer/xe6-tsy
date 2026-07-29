-- Version 2 allowed providers to omit either pricing field independently.
-- Normalize those historical rows to unknown pricing before requiring the pair.
DROP TRIGGER lingow_usage_records_reject_updates ON lingow_usage_records;

UPDATE lingow_usage_records
SET cost_amount = NULL,
    currency = NULL
WHERE (cost_amount IS NULL) <> (currency IS NULL);

ALTER TABLE lingow_usage_records
    ADD CONSTRAINT lingow_usage_records_pricing_pair_valid
        CHECK ((cost_amount IS NULL) = (currency IS NULL));

CREATE TRIGGER lingow_usage_records_reject_updates
    BEFORE UPDATE ON lingow_usage_records
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_usage_record_updates();
