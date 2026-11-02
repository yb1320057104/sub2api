-- PK-022/025: allow each Binance recharge channel to use its own dedicated
-- outbound proxy. The value is encrypted by the application; this column
-- remains opaque to SQL readers and API responses.
ALTER TABLE binance_recharge_accounts
    ADD COLUMN IF NOT EXISTS proxy_url_encrypted TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS proxy_preflight BOOLEAN NOT NULL DEFAULT FALSE;
