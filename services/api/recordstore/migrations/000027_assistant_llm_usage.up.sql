ALTER TABLE lingow_usage_records
    DROP CONSTRAINT lingow_usage_records_service_type_valid,
    ADD CONSTRAINT lingow_usage_records_service_type_valid
        CHECK (service_type IN ('asr', 'translation', 'assistant_llm', 'tts', 'diarization'));
