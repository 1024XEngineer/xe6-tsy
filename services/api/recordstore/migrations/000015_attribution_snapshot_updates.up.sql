-- Attribution corrections now keep the turn's speaker snapshot (speaker_code,
-- display_name, provider_speaker_id, voice_profile_id) aligned with the target
-- participant. Those fields become mutable; the translation snapshot, language
-- direction, timing, and identity keys remain immutable.
CREATE OR REPLACE FUNCTION recordstore_reject_voice_turn_immutable_updates()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.event_id IS DISTINCT FROM OLD.event_id
        OR NEW.event_payload_hash IS DISTINCT FROM OLD.event_payload_hash
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
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
