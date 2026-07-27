CREATE TABLE voice_session_participants (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    speaker_code TEXT NOT NULL,
    display_name TEXT,
    provider_speaker_id TEXT,
    voice_profile_id TEXT,
    confidence DOUBLE PRECISION,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT voice_session_participants_id_not_empty CHECK (id <> ''),
    CONSTRAINT voice_session_participants_session_id_not_empty CHECK (session_id <> ''),
    CONSTRAINT voice_session_participants_speaker_code_not_empty CHECK (speaker_code <> ''),
    CONSTRAINT voice_session_participants_session_speaker_code_key UNIQUE (session_id, speaker_code),
    CONSTRAINT voice_session_participants_session_id_id_key UNIQUE (session_id, id)
);

CREATE UNIQUE INDEX voice_session_participants_session_provider_speaker_id_key
    ON voice_session_participants (session_id, provider_speaker_id)
    WHERE provider_speaker_id IS NOT NULL;

CREATE INDEX voice_session_participants_session_speaker_order_idx
    ON voice_session_participants (session_id, speaker_code ASC, id ASC);

CREATE TABLE voice_turns (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    session_id TEXT NOT NULL,
    participant_id TEXT,
    speaker_code TEXT NOT NULL,
    display_name TEXT,
    provider_speaker_id TEXT,
    voice_profile_id TEXT,
    sequence_no BIGINT NOT NULL,
    source_language TEXT NOT NULL,
    target_language TEXT NOT NULL,
    language_config_version BIGINT NOT NULL,
    source_text TEXT NOT NULL,
    translated_text TEXT NOT NULL,
    speaker_confidence DOUBLE PRECISION,
    attribution_status TEXT NOT NULL,
    corrected_by TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ NOT NULL,
    corrected_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT voice_turns_id_not_empty CHECK (id <> ''),
    CONSTRAINT voice_turns_event_id_not_empty CHECK (event_id <> ''),
    CONSTRAINT voice_turns_session_id_not_empty CHECK (session_id <> ''),
    CONSTRAINT voice_turns_speaker_code_not_empty CHECK (speaker_code <> ''),
    CONSTRAINT voice_turns_sequence_no_positive CHECK (sequence_no >= 1),
    CONSTRAINT voice_turns_source_language_not_empty CHECK (source_language <> ''),
    CONSTRAINT voice_turns_target_language_not_empty CHECK (target_language <> ''),
    CONSTRAINT voice_turns_language_config_version_positive CHECK (language_config_version >= 1),
    CONSTRAINT voice_turns_source_text_not_empty CHECK (source_text <> ''),
    CONSTRAINT voice_turns_translated_text_not_empty CHECK (translated_text <> ''),
    CONSTRAINT voice_turns_attribution_status_valid CHECK (attribution_status IN ('pending', 'provisional', 'confirmed', 'corrected')),
    CONSTRAINT voice_turns_corrected_by_valid CHECK (corrected_by IS NULL OR corrected_by = 'system'),
    CONSTRAINT voice_turns_time_order_valid CHECK (ended_at >= started_at),
    CONSTRAINT voice_turns_session_sequence_no_key UNIQUE (session_id, sequence_no),
    CONSTRAINT voice_turns_session_participant_foreign_key
        FOREIGN KEY (session_id, participant_id)
        REFERENCES voice_session_participants (session_id, id)
        ON UPDATE RESTRICT
        ON DELETE RESTRICT
);

CREATE INDEX voice_turns_session_sequence_order_idx
    ON voice_turns (session_id, sequence_no ASC, id ASC);

CREATE INDEX voice_turns_history_created_order_idx
    ON voice_turns (created_at DESC, id DESC);

CREATE FUNCTION recordstore_reject_voice_turn_immutable_updates()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.event_id IS DISTINCT FROM OLD.event_id
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.speaker_code IS DISTINCT FROM OLD.speaker_code
        OR NEW.display_name IS DISTINCT FROM OLD.display_name
        OR NEW.provider_speaker_id IS DISTINCT FROM OLD.provider_speaker_id
        OR NEW.voice_profile_id IS DISTINCT FROM OLD.voice_profile_id
        OR NEW.sequence_no IS DISTINCT FROM OLD.sequence_no
        OR NEW.source_language IS DISTINCT FROM OLD.source_language
        OR NEW.target_language IS DISTINCT FROM OLD.target_language
        OR NEW.language_config_version IS DISTINCT FROM OLD.language_config_version
        OR NEW.source_text IS DISTINCT FROM OLD.source_text
        OR NEW.translated_text IS DISTINCT FROM OLD.translated_text
        OR NEW.started_at IS DISTINCT FROM OLD.started_at
        OR NEW.ended_at IS DISTINCT FROM OLD.ended_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'voice turn immutable fields cannot be updated';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER voice_turns_reject_immutable_updates
    BEFORE UPDATE ON voice_turns
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_voice_turn_immutable_updates();
