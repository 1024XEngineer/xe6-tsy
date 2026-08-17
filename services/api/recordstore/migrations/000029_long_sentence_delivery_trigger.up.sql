ALTER TABLE automatic_turn_runs
    ADD COLUMN delivery_trigger TEXT NOT NULL DEFAULT 'configured_route';

ALTER TABLE automatic_turn_runs
    ADD CONSTRAINT automatic_turn_runs_delivery_trigger_valid
    CHECK (delivery_trigger IN ('configured_route', 'long_sentence'));
