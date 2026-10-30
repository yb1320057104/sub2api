-- PK-036: isolated storage; no changes to existing account data.
CREATE TABLE IF NOT EXISTS pk_authorization_config (
 id SMALLINT PRIMARY KEY CHECK (id=1),
 inspection_enabled BOOLEAN NOT NULL DEFAULT FALSE,
 reauth_enabled BOOLEAN NOT NULL DEFAULT FALSE,
 inspection_concurrency INTEGER NOT NULL DEFAULT 10 CHECK (inspection_concurrency BETWEEN 1 AND 50),
 authorization_concurrency INTEGER NOT NULL DEFAULT 3 CHECK (authorization_concurrency BETWEEN 1 AND 50),
 account_concurrency INTEGER NOT NULL DEFAULT 3 CHECK (account_concurrency BETWEEN 1 AND 1000),
 account_priority INTEGER NOT NULL DEFAULT 50 CHECK (account_priority BETWEEN 0 AND 1000),
 group_ids JSONB NOT NULL DEFAULT '[]', reauth_status_codes JSONB NOT NULL DEFAULT '[401]', revision BIGINT NOT NULL DEFAULT 1,
 next_inspection_at TIMESTAMPTZ, next_reauth_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO pk_authorization_config(id) VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS pk_authorization_history (
 id BIGSERIAL PRIMARY KEY, email TEXT NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
 secret_encrypted TEXT NOT NULL, has_twofa BOOLEAN NOT NULL, source TEXT NOT NULL DEFAULT 'submission',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_pk_authorization_history_email ON pk_authorization_history(email,id DESC);
CREATE TABLE IF NOT EXISTS pk_authorization_proxies (
 id BIGSERIAL PRIMARY KEY, endpoint TEXT NOT NULL, secret_encrypted TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT TRUE, last_checked_at TIMESTAMPTZ,
 last_status TEXT NOT NULL DEFAULT 'unknown', last_message TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS pk_authorization_jobs (
 id BIGSERIAL PRIMARY KEY, batch_id TEXT NOT NULL,
 history_id BIGINT NOT NULL REFERENCES pk_authorization_history(id),
 email TEXT NOT NULL, account_id BIGINT,
 source TEXT NOT NULL CHECK (source IN ('manual','reauthorization')),
 status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','completed','failed','cancelled','needs_input','banned')),
 stage TEXT NOT NULL DEFAULT 'queued', message TEXT NOT NULL DEFAULT '', settings JSONB NOT NULL, owner_token TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_pk_authorization_jobs_active_email ON pk_authorization_jobs(email) WHERE status IN ('queued','running');
CREATE INDEX IF NOT EXISTS idx_pk_authorization_jobs_created ON pk_authorization_jobs(id DESC);
CREATE INDEX IF NOT EXISTS idx_pk_authorization_jobs_email ON pk_authorization_jobs(email,id DESC);
CREATE TABLE IF NOT EXISTS pk_authorization_state (
 account_id BIGINT PRIMARY KEY, credential_version TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'unknown', http_status INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL DEFAULT '', checked_at TIMESTAMPTZ, authorized_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
