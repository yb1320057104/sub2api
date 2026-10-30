-- Account probes are independent of group intelligence tests and of user billing.
CREATE TABLE IF NOT EXISTS pk_account_candy_results (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source VARCHAR(16) NOT NULL CHECK (source IN ('manual', 'scheduled')),
    status VARCHAR(16) NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'cancelled')),
    generation TEXT NOT NULL,
    model TEXT NOT NULL,
    effort TEXT NOT NULL,
    prompt TEXT NOT NULL,
    response_text TEXT NOT NULL DEFAULT '' CHECK (octet_length(response_text) <= 262144),
    passed BOOLEAN,
    answer INTEGER,
    original_group_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    target_group_id BIGINT,
    moved_to_group_id BIGINT,
    error_message TEXT NOT NULL DEFAULT '',
    move_error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT
);
CREATE INDEX IF NOT EXISTS idx_pk_account_candy_results_account
    ON pk_account_candy_results(account_id, id DESC);

CREATE TABLE IF NOT EXISTS pk_account_candy_state (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active_result_id BIGINT,
    lease_until TIMESTAMPTZ
);
