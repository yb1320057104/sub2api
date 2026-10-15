-- PK-030: original upstream token snapshots are independent of customer billing.
-- NULL means unknown historical usage; never infer it from converted counters.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS original_usage JSONB;

ALTER TABLE usage_dashboard_hourly
    ADD COLUMN IF NOT EXISTS original_usage JSONB,
    ADD COLUMN IF NOT EXISTS original_usage_requests BIGINT NOT NULL DEFAULT 0;

ALTER TABLE usage_dashboard_daily
    ADD COLUMN IF NOT EXISTS original_usage JSONB,
    ADD COLUMN IF NOT EXISTS original_usage_requests BIGINT NOT NULL DEFAULT 0;
