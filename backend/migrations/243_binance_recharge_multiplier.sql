-- PK-022: keep the received USDT amount separate from the system balance credit.
-- Existing orders retain their original 1:1 value; no user balance or historical
-- redemption record is changed by this migration.
ALTER TABLE binance_recharge_accounts
    ADD COLUMN credit_multiplier NUMERIC(10,6) NOT NULL DEFAULT 1
    CONSTRAINT binance_recharge_accounts_multiplier_range
    CHECK (credit_multiplier BETWEEN 0.000001 AND 1000);

ALTER TABLE binance_recharge_orders
    ADD COLUMN credit_multiplier NUMERIC(10,6) NOT NULL DEFAULT 1
    CONSTRAINT binance_recharge_orders_multiplier_range
    CHECK (credit_multiplier BETWEEN 0.000001 AND 1000),
    ADD COLUMN system_amount NUMERIC(20,8)
    GENERATED ALWAYS AS (ROUND(amount_units::numeric * credit_multiplier / 100000, 8)) STORED,
    ADD COLUMN credited_amount NUMERIC(20,8) NOT NULL DEFAULT 0;

UPDATE binance_recharge_orders
SET credited_amount = credited_units::numeric / 100000
WHERE status = 'COMPLETED';

-- All new orders must explicitly provide the account's multiplier snapshot.
-- Old binaries cannot insert a new order that silently defaults to 1:1.
ALTER TABLE binance_recharge_orders
    ALTER COLUMN credit_multiplier DROP DEFAULT,
    ALTER COLUMN system_amount SET NOT NULL,
    ADD CONSTRAINT binance_recharge_orders_system_amount_positive
    CHECK (system_amount > 0),
    ADD CONSTRAINT binance_recharge_orders_credit_consistency
    CHECK (
        (status = 'COMPLETED' AND credited_amount = system_amount AND credited_units = amount_units)
        OR (status <> 'COMPLETED' AND credited_amount = 0)
    );

-- The completion constraint also rejects old binaries that only set
-- credited_units. Their balance/redemption/receipt changes roll back together.
