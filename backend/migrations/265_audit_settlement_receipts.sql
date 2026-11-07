-- Negotiated fork-to-fork audit settlement. Original usage and penalty ledgers stay immutable.
CREATE TABLE audit_settlement_receipts (
    id UUID PRIMARY KEY,
    api_key_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','ready','unavailable')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 days'
);
CREATE INDEX idx_audit_settlement_receipts_owner ON audit_settlement_receipts(api_key_id, created_at DESC);
CREATE INDEX idx_audit_settlement_receipts_expiry ON audit_settlement_receipts(expires_at);

CREATE TABLE audit_settlement_receipt_items (
    receipt_id UUID NOT NULL REFERENCES audit_settlement_receipts(id) ON DELETE CASCADE,
    usage_id BIGINT NOT NULL,
    usage_created_at TIMESTAMPTZ NOT NULL,
    amount NUMERIC(30,8) NOT NULL CHECK (amount >= 0),
    PRIMARY KEY (receipt_id, usage_id, usage_created_at),
    -- One committed debit can be exported by one receipt only. Repeated HTTP
    -- requests can resolve to the same idempotent usage row.
    UNIQUE (usage_id, usage_created_at)
);

COMMENT ON TABLE audit_settlement_receipt_items IS
    'Immutable actual deducted automatic audit amounts only; no multipliers, synthetic tokens, or manual penalties.';
