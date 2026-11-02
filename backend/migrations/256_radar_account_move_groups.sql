-- PK-034/035: preserve every actual destination of a successful group move.
-- Historical rows remain NULL and readers fall back to moved_to_group_id.
ALTER TABLE pk_radar_account_runs
    ADD COLUMN IF NOT EXISTS moved_to_group_ids JSONB
        CHECK (moved_to_group_ids IS NULL OR jsonb_typeof(moved_to_group_ids) = 'array');
