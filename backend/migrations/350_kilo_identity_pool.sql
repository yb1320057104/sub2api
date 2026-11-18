-- A Kilo identity and its fixed login exit outlive replaceable GPT subscriptions.
ALTER TABLE pk_kilo_prepared_accounts
 ADD COLUMN identity_status TEXT NOT NULL DEFAULT 'active' CHECK(identity_status IN ('active','unknown','banned')),
 ADD COLUMN binding_state TEXT NOT NULL DEFAULT 'empty' CHECK(binding_state IN ('empty','bound','recovering','unbinding','binding','blocked')),
 ADD COLUMN binding_revision BIGINT NOT NULL DEFAULT 1 CHECK(binding_revision>0),
 ADD COLUMN recovery_task_id BIGINT,
 ADD COLUMN binding_error TEXT NOT NULL DEFAULT '',
 ADD COLUMN binding_resume BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN identity_session_encrypted TEXT NOT NULL DEFAULT '',
 ADD COLUMN identity_credential_encrypted TEXT NOT NULL DEFAULT '';
UPDATE pk_kilo_prepared_accounts SET
 identity_status=CASE WHEN last_error ~ '^kilo_(release|released|block):http_403$' THEN 'banned' ELSE 'active' END,
 binding_state=CASE
 WHEN last_error ~ '^kilo_(release|released|block):http_403$' THEN 'blocked'
 WHEN status='binding' THEN 'binding'
 WHEN source_account_id IS NOT NULL AND last_error LIKE 'kilo_source:%' THEN 'recovering'
 WHEN source_account_id IS NOT NULL THEN 'bound' ELSE 'empty' END,
 binding_resume=(status='bound' OR last_error LIKE 'kilo_source:%');
-- Old snapshots need the same generation fence as newly created identities.
UPDATE accounts g SET extra=COALESCE(g.extra,'{}'::jsonb)||jsonb_build_object('kilo_binding_revision',p.binding_revision)
FROM pk_kilo_prepared_accounts p WHERE p.account_id=g.id AND g.extra->>'kilo_prepared_id'=p.id::text;
CREATE INDEX pk_kilo_pool_idle ON pk_kilo_prepared_accounts(organization_id,id)
 WHERE identity_status='active' AND binding_state='empty' AND source_account_id IS NULL;
ALTER TABLE pk_kilo_jobs
 ADD COLUMN binding_revision BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN reused_identity BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE pk_kilo_jobs DROP CONSTRAINT pk_kilo_job_kind_check;
ALTER TABLE pk_kilo_jobs ADD CONSTRAINT pk_kilo_job_kind_check CHECK(
 (job_kind='activate_prepared' AND prepared_id IS NOT NULL) OR
 (job_kind='onboard_source' AND source_account_id IS NOT NULL AND login_proxy_id IS NOT NULL) OR
 (job_kind='unbind_source' AND prepared_id IS NOT NULL AND source_account_id IS NOT NULL AND login_proxy_id IS NOT NULL));
COMMENT ON COLUMN pk_kilo_prepared_accounts.identity_status IS 'Kilo identity health; only a Kilo identity/gateway 403 marks banned. GPT rejection never does.';
COMMENT ON COLUMN pk_kilo_prepared_accounts.binding_state IS 'GPT slot ownership. Empty is published only after remote disconnect confirmation; login proxy remains pinned.';
COMMENT ON COLUMN pk_kilo_jobs.binding_revision IS 'Fences remote results against a later GPT binding on the same Kilo Key.';
