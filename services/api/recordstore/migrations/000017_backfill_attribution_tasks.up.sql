-- Backfill durable attribution tasks for final turns that predate the async attribution worker.
--
-- 1. Every pending or provisional turn that has no task yet gets exactly one turn_attribution
--    task, account-scoped from the owning session, idempotently (ON CONFLICT (turn_id) DO NOTHING).
-- 2. Turns without a provider speaker key cannot be resolved deterministically; they are recorded
--    as failed with a stable error code so unresolved records remain auditable instead of silently
--    staying invisible to the worker.
-- 3. Tasks previously acked as completed while their turn is still unresolved are repaired to
--    pending so a real resolver can still consider them.
INSERT INTO attribution_tasks (task_id, turn_id, session_id, account_id, task_type, status, last_error)
SELECT 'attr_' || turn.id,
       turn.id,
       turn.session_id,
       COALESCE(owner.merged_into, owner.id),
       'turn_attribution',
       CASE WHEN turn.provider_speaker_id IS NULL OR btrim(turn.provider_speaker_id) = ''
            THEN 'failed' ELSE 'pending' END,
       CASE WHEN turn.provider_speaker_id IS NULL OR btrim(turn.provider_speaker_id) = ''
            THEN 'no_provider_speaker_id' ELSE NULL END
FROM voice_turns AS turn
JOIN voice_sessions AS sessions ON sessions.id = turn.session_id
JOIN lingow_accounts AS owner ON owner.id = sessions.account_id
WHERE turn.attribution_status IN ('pending', 'provisional')
  AND NOT EXISTS (
      SELECT 1
      FROM attribution_tasks AS existing
      WHERE existing.turn_id = turn.id
  )
ON CONFLICT (turn_id) DO NOTHING;

UPDATE attribution_tasks AS task
SET status = CASE WHEN turn.provider_speaker_id IS NULL OR btrim(turn.provider_speaker_id) = ''
                  THEN 'failed' ELSE 'pending' END,
    last_error = CASE WHEN turn.provider_speaker_id IS NULL OR btrim(turn.provider_speaker_id) = ''
                      THEN 'no_provider_speaker_id' ELSE NULL END,
    receipt = NULL,
    locked_until = NULL,
    updated_at = CURRENT_TIMESTAMP
FROM voice_turns AS turn
WHERE task.turn_id = turn.id
  AND task.status = 'completed'
  AND turn.attribution_status IN ('pending', 'provisional');
