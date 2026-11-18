-- Native Kilo onboarding keeps prepared credentials separate from schedulable
-- gateway accounts. A batch reserves its exact prepared/source pairs before work.
CREATE TABLE pk_kilo_prepared_accounts (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 256),
    email TEXT NOT NULL DEFAULT '',
    credential_encrypted TEXT NOT NULL,
    credential_fingerprint TEXT NOT NULL UNIQUE CHECK (length(credential_fingerprint)=64),
    auth_method TEXT NOT NULL CHECK (auth_method IN ('api_key','device')),
    organization_id TEXT NOT NULL DEFAULT '',
    machine_id UUID NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'ready' CHECK (status IN ('ready','binding','bound','disabled')),
    account_id BIGINT UNIQUE REFERENCES accounts(id) ON DELETE RESTRICT,
    source_account_id BIGINT UNIQUE REFERENCES accounts(id) ON DELETE RESTRICT,
    binding_job_id BIGINT,
    remote_user_id TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX pk_kilo_prepared_remote_identity ON pk_kilo_prepared_accounts(remote_user_id,organization_id) WHERE remote_user_id<>'';

CREATE TABLE pk_kilo_import_requests (
    request_id UUID PRIMARY KEY,
    payload_hash TEXT NOT NULL CHECK (length(payload_hash)=64),
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE pk_kilo_batches (
    id UUID PRIMARY KEY,
    request_id UUID NOT NULL UNIQUE,
    payload_hash TEXT NOT NULL CHECK (length(payload_hash)=64),
    options JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE pk_kilo_jobs (
    id BIGSERIAL PRIMARY KEY,
    batch_id UUID NOT NULL REFERENCES pk_kilo_batches(id),
    prepared_id BIGINT NOT NULL REFERENCES pk_kilo_prepared_accounts(id),
    source_account_id BIGINT REFERENCES accounts(id),
    source_identity TEXT NOT NULL DEFAULT '',
    account_id BIGINT REFERENCES accounts(id),
    stage TEXT NOT NULL DEFAULT 'queued',
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','awaiting_authorization','succeeded','failed','cancelled')),
    options JSONB NOT NULL,
    lease_token UUID,
    lease_until TIMESTAMPTZ,
    revision BIGINT NOT NULL DEFAULT 1,
    attempt INTEGER NOT NULL DEFAULT 0,
    verification_url TEXT NOT NULL DEFAULT '',
    user_code TEXT NOT NULL DEFAULT '',
    remote_state_encrypted TEXT NOT NULL DEFAULT '',
    remote_outcome TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    UNIQUE(batch_id,prepared_id),
    CHECK ((lease_token IS NULL)=(lease_until IS NULL))
);
CREATE UNIQUE INDEX pk_kilo_jobs_one_active_prepared ON pk_kilo_jobs(prepared_id)
    WHERE status IN ('queued','running','awaiting_authorization');
CREATE UNIQUE INDEX pk_kilo_jobs_one_active_source ON pk_kilo_jobs(source_account_id)
    WHERE source_account_id IS NOT NULL AND status IN ('queued','running','awaiting_authorization');
CREATE INDEX pk_kilo_jobs_due ON pk_kilo_jobs(next_attempt_at,id)
    WHERE status IN ('queued','running','awaiting_authorization');
ALTER TABLE pk_kilo_prepared_accounts ADD CONSTRAINT pk_kilo_prepared_job_fk
    FOREIGN KEY(binding_job_id) REFERENCES pk_kilo_jobs(id);

-- Device codes and issued bearer credentials never travel in browser polling DTOs.
CREATE TABLE pk_kilo_device_sessions (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    proxy_id BIGINT NOT NULL REFERENCES proxies(id),
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    code_encrypted TEXT NOT NULL,
    token_encrypted TEXT NOT NULL DEFAULT '',
    verification_url TEXT NOT NULL,
    user_code TEXT NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','denied','expired','cancelled','saved')),
    prepared_id BIGINT REFERENCES pk_kilo_prepared_accounts(id),
    next_poll_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX pk_kilo_device_expiry ON pk_kilo_device_sessions(expires_at)
    WHERE status IN ('pending','approved');

COMMENT ON TABLE pk_kilo_prepared_accounts IS 'Administrator-owned Kilo credentials waiting for explicit bulk pairing; not a second gateway scheduler.';
COMMENT ON COLUMN pk_kilo_jobs.remote_outcome IS 'Records ambiguous external mutations before retry; a worker restart never silently repeats a possibly committed binding.';

-- Only registered, non-retired application identities inherit control-data access.
DO $$
DECLARE node_role RECORD; table_name TEXT; sequence_row RECORD;
    added_tables TEXT[] := ARRAY['pk_kilo_prepared_accounts','pk_kilo_import_requests','pk_kilo_batches','pk_kilo_jobs','pk_kilo_device_sessions'];
BEGIN
    IF to_regclass('public.cluster_node_credentials') IS NULL THEN RETURN; END IF;
    FOR node_role IN
        SELECT DISTINCT c.database_role FROM public.cluster_node_credentials c
        JOIN public.cluster_nodes n USING(cluster_id,node_id)
        JOIN pg_roles r ON r.rolname=c.database_role
        WHERE c.status='active' AND n.state<>'retired' AND r.rolcanlogin
    LOOP
        FOREACH table_name IN ARRAY added_tables LOOP
            EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE public.%I TO %I',table_name,node_role.database_role);
        END LOOP;
        FOR sequence_row IN
            SELECT ns.nspname AS schema_name,s.relname AS sequence_name
            FROM pg_class s JOIN pg_namespace ns ON ns.oid=s.relnamespace
            JOIN pg_depend d ON d.objid=s.oid AND d.classid='pg_class'::regclass
                AND d.refclassid='pg_class'::regclass AND d.deptype IN ('a','i')
            JOIN pg_class t ON t.oid=d.refobjid JOIN pg_namespace tn ON tn.oid=t.relnamespace
            WHERE s.relkind='S' AND tn.nspname='public' AND t.relname=ANY(added_tables)
        LOOP
            EXECUTE format('GRANT USAGE,SELECT ON SEQUENCE %I.%I TO %I',sequence_row.schema_name,sequence_row.sequence_name,node_role.database_role);
        END LOOP;
    END LOOP;
END $$;
