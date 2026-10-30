-- PK-035: independent radar storage. Legacy settings and test history stay intact.
CREATE TABLE IF NOT EXISTS pk_radar_account_config (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    interval_minutes INTEGER NOT NULL CHECK (interval_minutes BETWEEN 1 AND 1440),
    target_group_id BIGINT,
    generation TEXT NOT NULL,
    initialized_from_legacy BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS pk_radar_account_models (
    id BIGSERIAL PRIMARY KEY,
    config_id SMALLINT NOT NULL REFERENCES pk_radar_account_config(id),
    model TEXT NOT NULL CHECK (length(model) BETWEEN 1 AND 200),
    effort TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL CHECK (sort_order BETWEEN 0 AND 7),
    UNIQUE(config_id, model),
    UNIQUE(config_id, sort_order)
);
CREATE TABLE IF NOT EXISTS pk_radar_account_runs (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL,
    source VARCHAR(16) NOT NULL CHECK (source IN ('manual','scheduled')),
    generation TEXT NOT NULL,
    models JSONB NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'running' CHECK (status IN ('running','completed','partial_failed','failed','cancelled')),
    verdict VARCHAR(16) NOT NULL DEFAULT 'unknown' CHECK (verdict IN ('unknown','pass','fail')),
    original_group_ids JSONB NOT NULL DEFAULT '[]',
    target_group_id BIGINT,
    moved_to_group_id BIGINT,
    error_message TEXT NOT NULL DEFAULT '',
    move_error TEXT NOT NULL DEFAULT '',
    egress_ip INET,
    egress_mode VARCHAR(16) NOT NULL CHECK (egress_mode IN ('proxy','direct')),
    egress_checked_at TIMESTAMPTZ,
    egress_error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT
);
CREATE INDEX IF NOT EXISTS idx_pk_radar_runs_account ON pk_radar_account_runs(account_id,id DESC);
CREATE TABLE IF NOT EXISTS pk_radar_account_model_results (
    id BIGSERIAL PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES pk_radar_account_runs(id) ON DELETE CASCADE,
    sort_order INTEGER NOT NULL,
    model TEXT NOT NULL,
    mapped_model TEXT NOT NULL DEFAULT '',
    effort TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed','cancelled')),
    passed BOOLEAN,
    answer INTEGER,
    response_text TEXT NOT NULL DEFAULT '' CHECK (octet_length(response_text) <= 262144),
    error_message TEXT NOT NULL DEFAULT '',
    egress_ip INET,
    egress_checked_at TIMESTAMPTZ,
    egress_error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT,
    UNIQUE(run_id,sort_order),
    CHECK (status = 'completed' OR passed IS NULL)
);
CREATE TABLE IF NOT EXISTS pk_radar_account_state (
    account_id BIGINT PRIMARY KEY,
    active_run_id BIGINT,
    lease_token TEXT,
    lease_until TIMESTAMPTZ,
    latest_run_id BIGINT,
    verdict VARCHAR(16) NOT NULL DEFAULT 'unknown' CHECK (verdict IN ('unknown','pass','fail')),
    generation TEXT NOT NULL DEFAULT '',
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_pk_radar_state_due ON pk_radar_account_state(next_run_at,lease_until);
CREATE INDEX IF NOT EXISTS idx_pk_radar_state_verdict ON pk_radar_account_state(generation,verdict,account_id);
