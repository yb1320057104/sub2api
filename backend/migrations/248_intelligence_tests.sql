-- PK-032: server-owned intelligence checks; no prompts or response traces in history.
CREATE TABLE IF NOT EXISTS pk_intelligence_test_keys (
    group_id BIGINT PRIMARY KEY,
    api_key_id BIGINT NOT NULL UNIQUE REFERENCES api_keys(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS pk_intelligence_test_state (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    mode VARCHAR(8) NOT NULL CHECK (mode IN ('svg', 'candy')),
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    active_result_id BIGINT,
    interval_minutes INTEGER NOT NULL DEFAULT 5 CHECK (interval_minutes BETWEEN 1 AND 1440),
    PRIMARY KEY (group_id, mode)
);

CREATE TABLE IF NOT EXISTS pk_intelligence_test_results (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    mode VARCHAR(8) NOT NULL CHECK (mode IN ('svg', 'candy')),
    status VARCHAR(16) NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    passed BOOLEAN,
    artifact TEXT NOT NULL DEFAULT '' CHECK (octet_length(artifact) <= 262144),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT,
    CHECK ((status = 'running' AND finished_at IS NULL) OR
           (status IN ('completed', 'failed') AND finished_at IS NOT NULL)),
    CHECK (status = 'completed' OR (passed IS NULL AND artifact = '')),
    CHECK (mode = 'svg' OR artifact = '')
);
CREATE INDEX IF NOT EXISTS idx_pk_intelligence_results_recent
    ON pk_intelligence_test_results(group_id, mode, id DESC);
