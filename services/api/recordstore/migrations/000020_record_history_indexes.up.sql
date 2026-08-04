CREATE INDEX voice_turns_session_history_order_idx
    ON voice_turns (session_id, created_at DESC, id DESC);
