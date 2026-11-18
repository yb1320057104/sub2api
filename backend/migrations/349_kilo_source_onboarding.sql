-- Extend the existing Kilo queue to cover credential acquisition. A source
-- account remains a read-only quota identity; generation belongs to its gateway.
ALTER TABLE pk_kilo_jobs
    ADD COLUMN job_kind TEXT NOT NULL DEFAULT 'activate_prepared',
    ADD COLUMN login_proxy_id BIGINT REFERENCES proxies(id),
    ADD COLUMN organization_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN verified_model_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN activate_when_ready BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN material_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN quota_task_id BIGINT,
    ADD COLUMN error_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN failed_stage TEXT NOT NULL DEFAULT '',
    ADD COLUMN next_action TEXT NOT NULL DEFAULT '',
    ADD COLUMN credential_ready BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN subscription_connected BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN quota_oauth_ready BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE pk_kilo_jobs ALTER COLUMN prepared_id DROP NOT NULL;
ALTER TABLE pk_kilo_jobs ADD CONSTRAINT pk_kilo_job_kind_check CHECK (
    (job_kind='activate_prepared' AND prepared_id IS NOT NULL) OR
    (job_kind='onboard_source' AND source_account_id IS NOT NULL AND login_proxy_id IS NOT NULL)
);
ALTER TABLE pk_kilo_jobs ADD CONSTRAINT pk_kilo_verified_models_check
    CHECK (jsonb_typeof(verified_model_ids)='array');
CREATE UNIQUE INDEX pk_kilo_onboard_batch_source ON pk_kilo_jobs(batch_id,source_account_id)
    WHERE job_kind='onboard_source';
COMMENT ON COLUMN pk_kilo_jobs.remote_state_encrypted IS
    'Encrypted, versioned executor session/operation receipt. No plaintext password, TOTP or cookie in options.';
COMMENT ON COLUMN pk_kilo_jobs.quota_task_id IS
    'Existing codex re-login task for the source quota identity, independent of the Kilo OAuth client.';

-- Retain the same inference restriction after an old pairing is released.
-- The original OAuth credential remains available to the read-only quota path.
UPDATE accounts SET extra=COALESCE(extra,'{}'::jsonb)||'{"kilo_quota_only":true}'::jsonb
WHERE deleted_at IS NULL AND (
    COALESCE(extra->>'kilo_source_prepared_id','') NOT IN ('','0') OR
    COALESCE(extra->>'kilo_source_gateway_id','') NOT IN ('','0') OR
    id IN (SELECT source_account_id FROM pk_kilo_prepared_accounts WHERE source_account_id IS NOT NULL)
);
