-- Historical databases recorded member5_control_plane before the durable
-- StartOperation table and direct created-to-ended timestamps were finalized.
-- Keep the legacy table for deployment compatibility, but make its deprecation
-- explicit so no new persistence path treats it as a data source.
DO $$
BEGIN
    IF to_regclass('voice_session_start_requests') IS NOT NULL THEN
        COMMENT ON TABLE voice_session_start_requests IS
            'DEPRECATED: legacy Start request table. New Session persistence must use voice_session_start_operations.';
    END IF;
END;
$$;

ALTER TABLE voice_sessions
    DROP CONSTRAINT IF EXISTS voice_sessions_timestamps_valid;

ALTER TABLE voice_sessions
    ADD CONSTRAINT voice_sessions_timestamps_valid CHECK (
        (status = 'created' AND started_at IS NULL AND ended_at IS NULL AND failure_error_code IS NULL)
        OR (status = 'active' AND started_at IS NOT NULL AND ended_at IS NULL AND failure_error_code IS NULL)
        OR (
            status = 'ended'
            AND ended_at IS NOT NULL
            AND failure_error_code IS NULL
            AND (
                (started_at IS NULL AND ended_at >= created_at)
                OR (started_at IS NOT NULL AND ended_at >= started_at)
            )
        )
        OR (status = 'failed' AND started_at IS NOT NULL AND ended_at IS NULL AND failure_error_code IS NOT NULL)
    );

DO $$
DECLARE
    operation_table REGCLASS := to_regclass('voice_session_start_operations');
BEGIN
    IF operation_table IS NULL THEN
        CREATE TABLE voice_session_start_operations (
            operation_id TEXT PRIMARY KEY,
            session_id TEXT NOT NULL,
            account_id TEXT NOT NULL,
            idempotency_key TEXT NOT NULL,
            request_hash TEXT NOT NULL,
            status TEXT NOT NULL,
            compensation_claim_id TEXT,
            started_at TIMESTAMPTZ,
            created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
            updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
            CONSTRAINT voice_session_start_operations_id_not_empty CHECK (operation_id <> ''),
            CONSTRAINT voice_session_start_operations_key_not_empty CHECK (idempotency_key <> ''),
            CONSTRAINT voice_session_start_operations_hash_not_empty CHECK (request_hash <> ''),
            CONSTRAINT voice_session_start_operations_status_valid CHECK (
                status IN ('pending', 'compensating', 'completed', 'compensated', 'compensation_failed')
            ),
            CONSTRAINT voice_session_start_operations_claim_id_not_empty CHECK (
                compensation_claim_id IS NULL OR compensation_claim_id <> ''
            ),
            CONSTRAINT voice_session_start_operations_updated_at_valid CHECK (updated_at >= created_at),
            CONSTRAINT voice_session_start_operations_state_valid CHECK (
                (status = 'pending' AND started_at IS NULL AND compensation_claim_id IS NULL)
                OR (status = 'compensating' AND started_at IS NULL AND compensation_claim_id IS NOT NULL)
                OR (status = 'completed' AND started_at IS NOT NULL AND compensation_claim_id IS NULL)
                OR (
                    status IN ('compensated', 'compensation_failed')
                    AND started_at IS NULL
                    AND compensation_claim_id IS NOT NULL
                )
            ),
            CONSTRAINT voice_session_start_operations_key_unique UNIQUE (account_id, idempotency_key),
            CONSTRAINT voice_session_start_operations_session_key
                FOREIGN KEY (session_id, account_id)
                REFERENCES voice_sessions (id, account_id)
                ON DELETE RESTRICT
        );

        CREATE UNIQUE INDEX voice_session_start_operations_one_unfinished_per_session
            ON voice_session_start_operations (session_id)
            WHERE status IN ('pending', 'compensating', 'compensation_failed');

        CREATE INDEX voice_session_start_operations_account_session_key_idx
            ON voice_session_start_operations (account_id, session_id, idempotency_key);

        operation_table := to_regclass('voice_session_start_operations');
    END IF;

    IF (
        SELECT COUNT(*) <> 6
        FROM pg_attribute
        WHERE attrelid = operation_table
          AND attname IN (
              'operation_id',
              'session_id',
              'account_id',
              'idempotency_key',
              'request_hash',
              'status'
          )
          AND NOT attisdropped
    ) THEN
        RAISE EXCEPTION
            'voice_session_start_operations is missing one or more critical columns';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint AS constraint_record
        JOIN pg_attribute AS column_record
          ON column_record.attrelid = constraint_record.conrelid
         AND column_record.attnum = ANY (constraint_record.conkey)
        WHERE constraint_record.conrelid = operation_table
          AND constraint_record.contype = 'p'
        GROUP BY constraint_record.oid
        HAVING COUNT(*) = 1
           AND BOOL_AND(column_record.attname = 'operation_id')
    ) THEN
        RAISE EXCEPTION
            'voice_session_start_operations is missing the operation_id primary key';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint AS constraint_record
        JOIN pg_attribute AS column_record
          ON column_record.attrelid = constraint_record.conrelid
         AND column_record.attnum = ANY (constraint_record.conkey)
        WHERE constraint_record.conrelid = operation_table
          AND constraint_record.contype = 'u'
        GROUP BY constraint_record.oid
        HAVING COUNT(*) = 2
           AND COUNT(*) FILTER (
               WHERE column_record.attname IN ('account_id', 'idempotency_key')
           ) = 2
    ) THEN
        RAISE EXCEPTION
            'voice_session_start_operations is missing account idempotency uniqueness';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_class AS index_record
        JOIN pg_index AS index_metadata
          ON index_metadata.indexrelid = index_record.oid
        JOIN pg_attribute AS session_column
          ON session_column.attrelid = index_metadata.indrelid
         AND session_column.attname = 'session_id'
        WHERE index_metadata.indrelid = operation_table
          AND index_record.relname =
              'voice_session_start_operations_one_unfinished_per_session'
          AND index_metadata.indisunique
          AND index_metadata.indpred IS NOT NULL
          AND index_metadata.indnkeyatts = 1
          AND session_column.attnum = ANY (index_metadata.indkey)
    ) THEN
        RAISE EXCEPTION
            'voice_session_start_operations is missing unfinished session uniqueness';
    END IF;
END;
$$;
