-- PK-025: optional encrypted backup route. Existing channels retain their
-- original single-route behavior. Query status contains only bounded labels.
ALTER TABLE binance_recharge_accounts
    ADD COLUMN IF NOT EXISTS backup_proxy_url_encrypted TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS backup_proxy_preflight BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS query_status JSONB NOT NULL DEFAULT '{}';
