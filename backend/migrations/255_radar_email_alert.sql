-- PK radar email alerts: independent configuration and latest-run state only.
CREATE TABLE IF NOT EXISTS pk_radar_email_alert (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    interval_minutes INTEGER NOT NULL DEFAULT 30 CHECK (interval_minutes BETWEEN 1 AND 10080),
    minimum_count INTEGER NOT NULL DEFAULT 1 CHECK (minimum_count BETWEEN 1 AND 1000000),
    conditions JSONB NOT NULL DEFAULT '{"quota_below_100":true,"qualified":true,"not_401":true}' CHECK (jsonb_typeof(conditions) = 'object'),
    recipients JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(recipients) = 'array' AND jsonb_array_length(recipients) <= 20),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    next_check_at TIMESTAMPTZ,
    last_checked_at TIMESTAMPTZ,
    last_count INTEGER CHECK (last_count >= 0),
    total_accounts INTEGER CHECK (total_accounts >= 0),
    unknown_quota_count INTEGER CHECK (unknown_quota_count >= 0),
    last_status TEXT NOT NULL DEFAULT 'idle' CHECK (last_status IN ('idle','checking','healthy','sent','partial','send_failed','check_failed','superseded','interrupted')),
    last_error TEXT NOT NULL DEFAULT '',
    last_sent_at TIMESTAMPTZ,
    sent_count INTEGER NOT NULL DEFAULT 0 CHECK (sent_count >= 0),
    failed_count INTEGER NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    active_run_token TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO pk_radar_email_alert(id) VALUES (1) ON CONFLICT (id) DO NOTHING;
