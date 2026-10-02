-- PK-026: security-policy ranking, exact manual deductions and durable audit.
CREATE TABLE IF NOT EXISTS cyber_policy_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    multiplier NUMERIC(10,6) NOT NULL DEFAULT 6.7 CHECK (multiplier BETWEEN 0.000001 AND 1000),
    auto_disable_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    auto_disable_threshold BIGINT NOT NULL DEFAULT 10 CHECK (auto_disable_threshold BETWEEN 1 AND 1000000000),
    reset_on_manual_enable BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO cyber_policy_settings(id) VALUES(1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS cyber_policy_deductions (
    id UUID PRIMARY KEY,
    actor_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    email TEXT NOT NULL,
    username TEXT NOT NULL,
    start_date VARCHAR(10) NOT NULL,
    end_date VARCHAR(10) NOT NULL,
    timezone TEXT NOT NULL,
    multiplier NUMERIC(10,6) NOT NULL CHECK (multiplier BETWEEN 0.000001 AND 1000),
    trigger_count BIGINT NOT NULL DEFAULT 0 CHECK (trigger_count >= 0),
    amount NUMERIC(20,8) GENERATED ALWAYS AS (trigger_count * multiplier) STORED,
    balance NUMERIC(20,8) NOT NULL,
    balance_after NUMERIC(20,8),
    status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '10 minutes',
    completed_at TIMESTAMPTZ,
    CHECK ((status = 'pending' AND completed_at IS NULL AND balance_after IS NULL) OR
           (status = 'completed' AND completed_at IS NOT NULL AND balance_after IS NOT NULL AND trigger_count > 0))
);
CREATE INDEX IF NOT EXISTS idx_cyber_policy_deductions_user ON cyber_policy_deductions(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cyber_policy_deductions_expired ON cyber_policy_deductions(expires_at) WHERE status = 'pending';

-- Snapshot identities deliberately have no FK to usage_logs: cleanup/partition
-- retention must neither erase payment evidence nor make a trigger chargeable again.
CREATE TABLE IF NOT EXISTS cyber_policy_preview_items (
    deduction_id UUID NOT NULL REFERENCES cyber_policy_deductions(id) ON DELETE CASCADE,
    usage_id BIGINT NOT NULL,
    usage_created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (deduction_id, usage_id, usage_created_at)
);
CREATE TABLE IF NOT EXISTS cyber_policy_charges (
    usage_id BIGINT NOT NULL,
    usage_created_at TIMESTAMPTZ NOT NULL,
    deduction_id UUID NOT NULL REFERENCES cyber_policy_deductions(id),
    user_id BIGINT NOT NULL,
    PRIMARY KEY (usage_id, usage_created_at)
);
CREATE INDEX IF NOT EXISTS idx_cyber_policy_charges_user ON cyber_policy_charges(user_id, usage_created_at);
CREATE INDEX IF NOT EXISTS idx_cyber_policy_charges_deduction ON cyber_policy_charges(deduction_id);
CREATE INDEX IF NOT EXISTS idx_usage_logs_cyber_policy_ranking ON usage_logs(created_at, user_id, id) WHERE request_type = 4;
