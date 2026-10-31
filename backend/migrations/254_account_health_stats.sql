-- PK-037: independent passive statistics, never scan request history on radar reads.
CREATE TABLE IF NOT EXISTS pk_account_health_config (
 id SMALLINT PRIMARY KEY CHECK (id=1), enabled BOOLEAN NOT NULL DEFAULT TRUE,
 interval_minutes INTEGER NOT NULL DEFAULT 30 CHECK (interval_minutes BETWEEN 5 AND 1440),
 window_hours INTEGER NOT NULL DEFAULT 24 CHECK (window_hours BETWEEN 1 AND 168),
 revision BIGINT NOT NULL DEFAULT 1, next_cycle_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO pk_account_health_config(id) VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS pk_account_health_cycles (
 id BIGSERIAL PRIMARY KEY, revision BIGINT NOT NULL, status TEXT NOT NULL,
 started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), finished_at TIMESTAMPTZ,
 window_start TIMESTAMPTZ NOT NULL, window_end TIMESTAMPTZ NOT NULL,
 interval_minutes INTEGER NOT NULL, batch_count INTEGER NOT NULL,
 total_accounts INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_pk_health_active ON pk_account_health_cycles ((1)) WHERE status='running';
CREATE TABLE IF NOT EXISTS pk_account_health_jobs (
 cycle_id BIGINT NOT NULL REFERENCES pk_account_health_cycles(id) ON DELETE CASCADE,
 account_id BIGINT NOT NULL, account_name TEXT NOT NULL,
 batch_no INTEGER NOT NULL, scheduled_at TIMESTAMPTZ NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending', started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ,
 error_message TEXT NOT NULL DEFAULT '', result JSONB,
 PRIMARY KEY(cycle_id,account_id)
);
CREATE INDEX IF NOT EXISTS idx_pk_health_jobs_queue ON pk_account_health_jobs(cycle_id,batch_no,status,account_id);
CREATE TABLE IF NOT EXISTS pk_account_health_snapshots (
 account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 result JSONB NOT NULL, computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
