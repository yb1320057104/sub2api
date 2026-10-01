-- PK-022: Binance UID transfers use integer 1/100000 USDT units. Existing
-- payment tables retain their fiat precision and existing payment behavior.
CREATE TABLE IF NOT EXISTS binance_recharge_accounts (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    recipient_uid VARCHAR(32) NOT NULL UNIQUE,
    api_key_encrypted TEXT NOT NULL DEFAULT '',
    api_secret_encrypted TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    order_ttl_minutes INTEGER NOT NULL DEFAULT 30 CHECK (order_ttl_minutes IN (15, 30)),
    last_checked_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    next_poll_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    poll_token VARCHAR(36) NOT NULL DEFAULT '',
    failure_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS binance_recharge_orders (
    id BIGSERIAL PRIMARY KEY,
    order_no VARCHAR(40) NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    account_id BIGINT NOT NULL REFERENCES binance_recharge_accounts(id),
    account_name VARCHAR(100) NOT NULL,
    recipient_uid VARCHAR(32) NOT NULL,
    requested_amount BIGINT NOT NULL CHECK (requested_amount BETWEEN 1 AND 1000000),
    amount_units BIGINT NOT NULL CHECK (amount_units > 0),
    credited_units BIGINT NOT NULL DEFAULT 0 CHECK (credited_units >= 0),
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'COMPLETED', 'EXPIRED', 'REVIEW')),
    transaction_id VARCHAR(160) NOT NULL DEFAULT '',
    review_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    paid_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- An expired/completed amount is never reallocated to a different user.
    UNIQUE (recipient_uid, amount_units),
    CHECK (amount_units > requested_amount * 100000 AND amount_units < (requested_amount + 1) * 100000),
    CHECK (credited_units = 0 OR credited_units = amount_units)
);
CREATE INDEX IF NOT EXISTS idx_binance_recharge_orders_user_created ON binance_recharge_orders(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_binance_recharge_orders_account_status ON binance_recharge_orders(account_id, status);

CREATE TABLE IF NOT EXISTS binance_recharge_receipts (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES binance_recharge_accounts(id),
    recipient_uid VARCHAR(32) NOT NULL,
    transaction_id VARCHAR(160) NOT NULL,
    amount_units BIGINT NOT NULL CHECK (amount_units > 0),
    paid_at TIMESTAMPTZ NOT NULL,
    order_id BIGINT REFERENCES binance_recharge_orders(id),
    status VARCHAR(20) NOT NULL CHECK (status IN ('COMPLETED', 'REVIEW')),
    review_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (recipient_uid, transaction_id)
);
CREATE INDEX IF NOT EXISTS idx_binance_recharge_receipts_amount ON binance_recharge_receipts(recipient_uid, amount_units);
CREATE INDEX IF NOT EXISTS idx_binance_recharge_receipts_review ON binance_recharge_receipts(status, created_at DESC);
