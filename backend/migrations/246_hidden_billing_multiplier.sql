-- PK-029: append-only snapshots; never reprice or debit historical usage.
-- Explicit SQL repositories own these extension columns (like first_response_ms).
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS hidden_rate_multiplier NUMERIC(10,6)
    CHECK (hidden_rate_multiplier >= 0 AND hidden_rate_multiplier <= 1000);

-- Batch jobs freeze pricing before a balance hold and may settle days later.
ALTER TABLE batch_image_jobs
    ADD COLUMN IF NOT EXISTS hidden_rate_multiplier NUMERIC(10,6)
    CHECK (hidden_rate_multiplier >= 0 AND hidden_rate_multiplier <= 1000);
