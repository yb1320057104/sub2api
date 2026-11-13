-- Durable external import receipts commit atomically with the created account.
-- No foreign-key cascade or TTL: a deleted account must not free an operation ID.
CREATE TABLE IF NOT EXISTS zhongcai_import_receipts (
    key_id VARCHAR(128) NOT NULL,
    source_id UUID NOT NULL,
    operation_id UUID NOT NULL,
    request_sha256 VARCHAR(64) NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    identity_sha256 VARCHAR(64) NOT NULL CHECK (identity_sha256 ~ '^[0-9a-f]{64}$'),
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    requires_network BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (key_id, source_id, operation_id)
);
