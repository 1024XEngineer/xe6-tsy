-- Language configuration schema (issue #88 §5.3).
-- voice_sessions FK is deferred until the session-management module owns that table;
-- session_id is stored as a plain identifier for now.

CREATE TABLE IF NOT EXISTS schema_migrations (
    filename   TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS supported_languages (
    language_code      VARCHAR(10) PRIMARY KEY,
    display_name       VARCHAR(64) NOT NULL,
    display_name_en    VARCHAR(64) NOT NULL DEFAULT '',
    supports_as_source BOOLEAN NOT NULL DEFAULT TRUE,
    supports_as_target BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order         INT NOT NULL DEFAULT 0,
    is_active          BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS voice_session_language_configs (
    id              VARCHAR(26) PRIMARY KEY,
    session_id      VARCHAR(26) NOT NULL,
    version         INT NOT NULL,
    language_pairs  JSONB NOT NULL,
    status          VARCHAR(20) NOT NULL,
    effective_from  TIMESTAMPTZ NOT NULL,
    effective_until TIMESTAMPTZ,
    created_by      VARCHAR(64) NOT NULL,
    idempotency_key VARCHAR(128),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_lang_config_status
        CHECK (status IN ('active', 'superseded', 'expired'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_lang_config_active
    ON voice_session_language_configs (session_id, status)
    WHERE status = 'active';

CREATE UNIQUE INDEX IF NOT EXISTS idx_lang_config_version
    ON voice_session_language_configs (session_id, version);

CREATE UNIQUE INDEX IF NOT EXISTS idx_lang_config_idempotency
    ON voice_session_language_configs (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_lang_config_session_version_desc
    ON voice_session_language_configs (session_id, version DESC);

INSERT INTO supported_languages (
    language_code, display_name, display_name_en,
    supports_as_source, supports_as_target, sort_order, is_active
) VALUES
    ('zh-CN', '中文（简体）', 'Chinese (Simplified)', TRUE, TRUE, 10, TRUE),
    ('en-US', 'English (US)', 'English (US)', TRUE, TRUE, 20, TRUE)
ON CONFLICT (language_code) DO NOTHING;
