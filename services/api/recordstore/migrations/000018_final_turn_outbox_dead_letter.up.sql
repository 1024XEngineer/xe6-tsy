ALTER TABLE final_turn_outbox
    ADD COLUMN last_error TEXT,
    ADD COLUMN rejected_at TIMESTAMPTZ;
