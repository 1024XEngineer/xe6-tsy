-- Control-plane metadata for choosing immutable ASR and TTS profiles per
-- unordered language pair. Provider credentials and endpoints remain outside
-- this schema in deployment configuration.

CREATE TABLE IF NOT EXISTS speech_asr_profiles (
    id                   VARCHAR(128) PRIMARY KEY,
    provider_code        VARCHAR(128) NOT NULL,
    model                VARCHAR(256) NOT NULL,
    supports_auto_detect BOOLEAN NOT NULL DEFAULT FALSE,
    supports_streaming   BOOLEAN NOT NULL DEFAULT TRUE,
    input_encoding       VARCHAR(64) NOT NULL,
    input_sample_rate_hz INT NOT NULL,
    input_channels       INT NOT NULL,
    enabled              BOOLEAN NOT NULL DEFAULT TRUE,
    retired_at           TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_speech_asr_profile_input
        CHECK (input_sample_rate_hz > 0 AND input_channels > 0),
    CONSTRAINT chk_speech_asr_profile_retired
        CHECK (retired_at IS NULL OR enabled = FALSE)
);

CREATE TABLE IF NOT EXISTS speech_tts_profiles (
    id                    VARCHAR(128) PRIMARY KEY,
    provider_code         VARCHAR(128) NOT NULL,
    model                 VARCHAR(256) NOT NULL,
    voice_id              VARCHAR(256) NOT NULL,
    supports_streaming    BOOLEAN NOT NULL DEFAULT TRUE,
    output_encoding       VARCHAR(64) NOT NULL,
    output_sample_rate_hz INT NOT NULL,
    output_channels       INT NOT NULL,
    enabled               BOOLEAN NOT NULL DEFAULT TRUE,
    retired_at            TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_speech_tts_profile_audio
        CHECK (output_sample_rate_hz > 0 AND output_channels > 0),
    CONSTRAINT chk_speech_tts_profile_retired
        CHECK (retired_at IS NULL OR enabled = FALSE)
);

CREATE TABLE IF NOT EXISTS speech_asr_profile_languages (
    profile_id    VARCHAR(128) NOT NULL,
    language_code VARCHAR(10) NOT NULL,
    PRIMARY KEY (profile_id, language_code),
    CONSTRAINT fk_speech_asr_profile_language_profile
        FOREIGN KEY (profile_id) REFERENCES speech_asr_profiles(id) ON DELETE RESTRICT,
    CONSTRAINT fk_speech_asr_profile_language_catalog
        FOREIGN KEY (language_code) REFERENCES supported_languages(language_code) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS speech_tts_profile_languages (
    profile_id    VARCHAR(128) NOT NULL,
    language_code VARCHAR(10) NOT NULL,
    PRIMARY KEY (profile_id, language_code),
    CONSTRAINT fk_speech_tts_profile_language_profile
        FOREIGN KEY (profile_id) REFERENCES speech_tts_profiles(id) ON DELETE RESTRICT,
    CONSTRAINT fk_speech_tts_profile_language_catalog
        FOREIGN KEY (language_code) REFERENCES supported_languages(language_code) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS speech_language_pair_routes (
    id             VARCHAR(128) PRIMARY KEY,
    language_a     VARCHAR(10) NOT NULL,
    language_b     VARCHAR(10) NOT NULL,
    asr_profile_id VARCHAR(128) NOT NULL,
    tts_profile_id VARCHAR(128) NOT NULL,
    enabled        BOOLEAN NOT NULL DEFAULT TRUE,
    retired_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_speech_language_pair_route_order
        CHECK (language_a < language_b),
    CONSTRAINT chk_speech_language_pair_route_retired
        CHECK (retired_at IS NULL OR enabled = FALSE),
    CONSTRAINT fk_speech_language_pair_route_language_a
        FOREIGN KEY (language_a) REFERENCES supported_languages(language_code) ON DELETE RESTRICT,
    CONSTRAINT fk_speech_language_pair_route_language_b
        FOREIGN KEY (language_b) REFERENCES supported_languages(language_code) ON DELETE RESTRICT,
    CONSTRAINT fk_speech_language_pair_route_asr_profile
        FOREIGN KEY (asr_profile_id) REFERENCES speech_asr_profiles(id) ON DELETE RESTRICT,
    CONSTRAINT fk_speech_language_pair_route_tts_profile
        FOREIGN KEY (tts_profile_id) REFERENCES speech_tts_profiles(id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS speech_active_language_pair_route_unique
    ON speech_language_pair_routes (language_a, language_b)
    WHERE enabled = TRUE AND retired_at IS NULL;

-- Preserve the existing default only when the legacy bilingual pair remains
-- selectable in the live catalog. No provider credential or endpoint is
-- inferred by this compatibility seed.
INSERT INTO speech_asr_profiles (
    id, provider_code, model, supports_auto_detect, supports_streaming,
    input_encoding, input_sample_rate_hz, input_channels, enabled
)
SELECT 'legacy-default', 'legacy', 'legacy-default', FALSE, TRUE,
       'pcm_s16le', 16000, 1, TRUE
WHERE EXISTS (
    SELECT 1
    FROM supported_languages
    WHERE language_code = 'zh-CN'
      AND is_active = TRUE
      AND supports_as_source = TRUE
      AND supports_as_target = TRUE
)
AND EXISTS (
    SELECT 1
    FROM supported_languages
    WHERE language_code = 'en-US'
      AND is_active = TRUE
      AND supports_as_source = TRUE
      AND supports_as_target = TRUE
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO speech_tts_profiles (
    id, provider_code, model, voice_id, supports_streaming, output_encoding,
    output_sample_rate_hz, output_channels, enabled
)
SELECT 'legacy-default', 'legacy', 'legacy-default', 'legacy-default', TRUE,
       'pcm_s16le', 24000, 1, TRUE
WHERE EXISTS (
    SELECT 1
    FROM supported_languages
    WHERE language_code = 'zh-CN'
      AND is_active = TRUE
      AND supports_as_source = TRUE
      AND supports_as_target = TRUE
)
AND EXISTS (
    SELECT 1
    FROM supported_languages
    WHERE language_code = 'en-US'
      AND is_active = TRUE
      AND supports_as_source = TRUE
      AND supports_as_target = TRUE
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO speech_asr_profile_languages (profile_id, language_code)
SELECT 'legacy-default', language_code
FROM supported_languages
WHERE language_code IN ('zh-CN', 'en-US')
  AND is_active = TRUE
  AND supports_as_source = TRUE
  AND supports_as_target = TRUE
  AND EXISTS (
      SELECT 1 FROM speech_asr_profiles
      WHERE id = 'legacy-default' AND enabled = TRUE AND retired_at IS NULL
  )
ON CONFLICT (profile_id, language_code) DO NOTHING;

INSERT INTO speech_tts_profile_languages (profile_id, language_code)
SELECT 'legacy-default', language_code
FROM supported_languages
WHERE language_code IN ('zh-CN', 'en-US')
  AND is_active = TRUE
  AND supports_as_source = TRUE
  AND supports_as_target = TRUE
  AND EXISTS (
      SELECT 1 FROM speech_tts_profiles
      WHERE id = 'legacy-default' AND enabled = TRUE AND retired_at IS NULL
  )
ON CONFLICT (profile_id, language_code) DO NOTHING;

INSERT INTO speech_language_pair_routes (
    id, language_a, language_b, asr_profile_id, tts_profile_id, enabled
)
SELECT 'legacy-default-en-us-zh-cn', 'en-US', 'zh-CN', 'legacy-default', 'legacy-default', TRUE
WHERE EXISTS (
    SELECT 1 FROM speech_asr_profile_languages
    WHERE profile_id = 'legacy-default' AND language_code = 'en-US'
)
AND EXISTS (
    SELECT 1 FROM speech_asr_profile_languages
    WHERE profile_id = 'legacy-default' AND language_code = 'zh-CN'
)
AND EXISTS (
    SELECT 1 FROM speech_tts_profile_languages
    WHERE profile_id = 'legacy-default' AND language_code = 'en-US'
)
AND EXISTS (
    SELECT 1 FROM speech_tts_profile_languages
    WHERE profile_id = 'legacy-default' AND language_code = 'zh-CN'
)
ON CONFLICT DO NOTHING;
