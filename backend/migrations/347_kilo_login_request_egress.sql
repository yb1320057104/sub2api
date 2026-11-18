-- Login identity keeps one explicit proxy; ordinary bindings contain the model
-- request pool. An empty prepared account remains unconfigured until selected.
ALTER TABLE pk_kilo_prepared_accounts
    ADD COLUMN login_proxy_id BIGINT REFERENCES proxies(id) ON DELETE RESTRICT,
    ADD COLUMN routing_options JSONB NOT NULL DEFAULT '{}'::jsonb;

WITH saved AS (
    SELECT DISTINCT ON (prepared_id) prepared_id,proxy_id
    FROM pk_kilo_device_sessions
    WHERE status='saved' AND prepared_id IS NOT NULL
    ORDER BY prepared_id,updated_at DESC,created_at DESC,id DESC
), selected AS (
    SELECT prepared.id,COALESCE(saved.proxy_id,first_binding.proxy_id) AS proxy_id
    FROM pk_kilo_prepared_accounts prepared
    LEFT JOIN saved ON saved.prepared_id=prepared.id
    LEFT JOIN LATERAL (
        SELECT b.proxy_id FROM pk_effective_account_proxy_bindings b
        WHERE b.account_id=prepared.account_id ORDER BY b.position,b.proxy_id LIMIT 1
    ) first_binding ON TRUE
)
UPDATE pk_kilo_prepared_accounts prepared SET login_proxy_id=selected.proxy_id
FROM selected WHERE selected.id=prepared.id AND selected.proxy_id IS NOT NULL;

UPDATE accounts a
SET extra=jsonb_set(COALESCE(a.extra,'{}'::jsonb),'{kilo_login_proxy_id}',to_jsonb(prepared.login_proxy_id),TRUE)
FROM pk_kilo_prepared_accounts prepared
WHERE prepared.account_id=a.id AND prepared.login_proxy_id IS NOT NULL;

-- Existing bound gateways do not rerun the planner during upgrade. Remove the
-- login credential (including transport aliases) from their request collection
-- without releasing its route or any response lease. Updating the legacy
-- primary fires 263's full-set reset, so restore the saved independent bindings.
DO $kilo_split_login_bindings$
DECLARE account_row RECORD; remaining JSONB; next_proxy BIGINT;
BEGIN
    PERFORM pg_advisory_xact_lock(70440001);
    PERFORM pg_advisory_xact_lock(70440079);
    FOR account_row IN
        SELECT a.id,a.proxy_id,pk_kilo_proxy_credential_fingerprint(login.host,login.port,login.username,login.password) AS fingerprint
        FROM accounts a JOIN proxies login ON a.extra->'kilo_login_proxy_id'=to_jsonb(login.id)
        WHERE a.parent_account_id IS NULL AND a.extra->'kilo_managed'='true'::jsonb
        AND (EXISTS(SELECT 1 FROM pk_account_proxy_bindings b JOIN proxies p ON p.id=b.proxy_id
                    WHERE b.account_id=a.id AND pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)=pk_kilo_proxy_credential_fingerprint(login.host,login.port,login.username,login.password))
             OR EXISTS(SELECT 1 FROM proxies p WHERE p.id=a.proxy_id
                       AND pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)=pk_kilo_proxy_credential_fingerprint(login.host,login.port,login.username,login.password)))
        ORDER BY a.id
    LOOP
        SELECT COALESCE(jsonb_agg(jsonb_build_object('proxy_id',b.proxy_id,'position',b.position) ORDER BY b.position),'[]'::jsonb)
        INTO remaining FROM pk_account_proxy_bindings b JOIN proxies p ON p.id=b.proxy_id
        WHERE b.account_id=account_row.id AND pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)<>account_row.fingerprint;
        IF EXISTS(SELECT 1 FROM proxies p WHERE p.id=account_row.proxy_id
                  AND pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)=account_row.fingerprint) THEN
            SELECT proxy_id INTO next_proxy FROM jsonb_to_recordset(remaining) AS b(proxy_id BIGINT,position INTEGER) ORDER BY position LIMIT 1;
            UPDATE accounts SET proxy_id=next_proxy,proxy_fallback_origin_id=NULL,updated_at=NOW() WHERE id=account_row.id;
            INSERT INTO pk_account_proxy_bindings(account_id,proxy_id,position)
            SELECT account_row.id,b.proxy_id,b.position FROM jsonb_to_recordset(remaining) AS b(proxy_id BIGINT,position INTEGER)
            ON CONFLICT DO NOTHING;
        END IF;
        DELETE FROM pk_account_proxy_bindings b USING proxies p
        WHERE b.account_id=account_row.id AND p.id=b.proxy_id
        AND pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)=account_row.fingerprint;
        DELETE FROM pk_account_proxy_fallback_sets WHERE account_id=account_row.id;
        UPDATE accounts child SET proxy_id=parent.proxy_id,proxy_fallback_origin_id=NULL,updated_at=NOW()
        FROM accounts parent WHERE parent.id=account_row.id AND child.parent_account_id=parent.id
        AND child.proxy_id IS DISTINCT FROM parent.proxy_id;
    END LOOP;
END;
$kilo_split_login_bindings$;

COMMENT ON COLUMN pk_kilo_prepared_accounts.login_proxy_id IS
    'Fixed Kilo login identity proxy; separate from model request bindings and never silently rotated.';
COMMENT ON COLUMN pk_kilo_prepared_accounts.routing_options IS
    'Per-account request routing options. Missing auto_release_statuses defaults to [401,402,403]; explicit [] disables automatic release, including structured restriction codes.';

-- Keep the credential seen at route creation, so editing a proxy row cannot
-- change which original credential remains fenced by an outstanding stream.
ALTER TABLE pk_kilo_egress_routes ADD COLUMN credential_fingerprint TEXT
    CHECK (credential_fingerprint ~ '^[0-9a-f]{64}$');
CREATE INDEX pk_kilo_egress_routes_credential_idx ON pk_kilo_egress_routes(credential_fingerprint,account_id);

-- 346 did not retain credential snapshots. Only a matching URL hash proves the
-- current row is the configuration that this historical route actually used.
-- Unmatched legacy rows keep NULL and conservatively fence their account's
-- existing claims until explicitly drained; never invent historical identity.
CREATE FUNCTION pk_kilo_proxy_config_hash(proxy_protocol TEXT,proxy_host TEXT,proxy_port INTEGER,proxy_username TEXT,proxy_password TEXT)
RETURNS TEXT LANGUAGE SQL IMMUTABLE PARALLEL SAFE AS $$
    SELECT encode(sha256(convert_to(proxy_protocol || '://' ||
        CASE WHEN COALESCE(proxy_username,'')<>'' AND COALESCE(proxy_password,'')<>'' THEN
            (SELECT string_agg(CASE WHEN chr(byte) ~ '^[A-Za-z0-9._~$&+,;=-]$' THEN chr(byte) ELSE '%' || upper(lpad(to_hex(byte),2,'0')) END,'' ORDER BY n)
             FROM generate_series(0,octet_length(convert_to(proxy_username,'UTF8'))-1) n
             CROSS JOIN LATERAL (SELECT get_byte(convert_to(proxy_username,'UTF8'),n) AS byte) b)
            || ':' ||
            (SELECT string_agg(CASE WHEN chr(byte) ~ '^[A-Za-z0-9._~$&+,;=-]$' THEN chr(byte) ELSE '%' || upper(lpad(to_hex(byte),2,'0')) END,'' ORDER BY n)
             FROM generate_series(0,octet_length(convert_to(proxy_password,'UTF8'))-1) n
             CROSS JOIN LATERAL (SELECT get_byte(convert_to(proxy_password,'UTF8'),n) AS byte) b)
            || '@' ELSE '' END ||
        CASE WHEN strpos(proxy_host,':')>0 THEN '[' || proxy_host || ']' ELSE proxy_host END || ':' || proxy_port::text,'UTF8')),'hex')
$$;
UPDATE pk_kilo_egress_routes r
SET credential_fingerprint=pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)
FROM proxies p WHERE p.id=r.proxy_id
AND r.config_hash=pk_kilo_proxy_config_hash(p.protocol,p.host,p.port,p.username,p.password);
DROP FUNCTION pk_kilo_proxy_config_hash(TEXT,TEXT,INTEGER,TEXT,TEXT);

-- A historical claim without any surviving route has no outstanding lease
-- (the lease FK restricts route deletion), so it is safe to return to the pool.
DELETE FROM pk_kilo_proxy_owners o
WHERE NOT EXISTS (
    SELECT 1 FROM pk_kilo_egress_routes r
    WHERE r.credential_fingerprint=o.credential_fingerprint
       OR (r.credential_fingerprint IS NULL AND (o.account_id IS NULL OR r.account_id=o.account_id))
);
COMMENT ON TABLE pk_kilo_proxy_owners IS
    'Current verified credential occupancy, retained until the final route and all response leases are explicitly released.';
COMMENT ON COLUMN pk_kilo_proxy_owners.account_id IS
    'Current Kilo account occupying this credential. NULL fences ambiguous legacy aliases while a route survives. Login configuration reserves its proxy independently.';
COMMENT ON TABLE pk_kilo_egress_routes IS
    'Verified or pending Kilo proxy routes admitted through an ordinary request binding or the fixed login proxy.';

-- Publish a new login pin in the same transaction that reserves its credential.
-- Ordinary account mutations already take the binding lock before egress; use
-- that order here too. An unchanged pin never reclaims or steals an old owner.
CREATE FUNCTION pk_kilo_login_proxy_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE candidate BIGINT; fingerprint TEXT;
BEGIN
    IF NEW.extra->'kilo_managed' IS DISTINCT FROM 'true'::jsonb THEN RETURN NEW; END IF;
    IF TG_OP='UPDATE' AND NEW.extra->'kilo_login_proxy_id' IS NOT DISTINCT FROM OLD.extra->'kilo_login_proxy_id'
        AND OLD.extra->'kilo_managed'='true'::jsonb THEN RETURN NEW; END IF;
    IF NEW.extra->'kilo_login_proxy_id' IS NULL THEN RETURN NEW; END IF;
    IF jsonb_typeof(NEW.extra->'kilo_login_proxy_id')<>'number'
        OR NEW.extra->>'kilo_login_proxy_id' !~ '^[1-9][0-9]{0,18}$'
        OR (NEW.extra->>'kilo_login_proxy_id')::numeric>9223372036854775807 THEN
        RAISE EXCEPTION 'Kilo login proxy must be a positive integer' USING ERRCODE='23514';
    END IF;
    candidate := (NEW.extra->>'kilo_login_proxy_id')::bigint;
    PERFORM pg_advisory_xact_lock(70440001);
    PERFORM pg_advisory_xact_lock(70440079);
    SELECT pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)
    INTO fingerprint FROM proxies p WHERE p.id=candidate;
    IF fingerprint IS NULL THEN RAISE EXCEPTION 'Kilo login proxy is unavailable' USING ERRCODE='23503'; END IF;
    IF EXISTS (
        SELECT 1 FROM pk_kilo_proxy_owners o WHERE o.credential_fingerprint=fingerprint
        AND o.account_id IS DISTINCT FROM NEW.id AND EXISTS (
            SELECT 1 FROM pk_kilo_egress_routes r WHERE r.credential_fingerprint=o.credential_fingerprint
            OR (r.credential_fingerprint IS NULL AND (o.account_id IS NULL OR r.account_id=o.account_id)))
    ) OR EXISTS (
        SELECT 1 FROM pk_kilo_egress_routes r JOIN proxies p ON p.id=r.proxy_id
        WHERE r.account_id<>NEW.id
        AND COALESCE(r.credential_fingerprint,pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password))=fingerprint
    ) OR EXISTS (
        SELECT 1 FROM accounts a JOIN proxies p ON a.extra->'kilo_login_proxy_id'=to_jsonb(p.id)
        WHERE a.id<>NEW.id AND a.extra->'kilo_managed'='true'::jsonb
        AND pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password)=fingerprint
    ) THEN
        RAISE EXCEPTION 'Kilo proxy credentials are currently occupied by another account' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER pk_kilo_login_proxy_capacity BEFORE INSERT OR UPDATE OF extra ON accounts
FOR EACH ROW EXECUTE FUNCTION pk_kilo_login_proxy_guard();

DO $kilo_recyclable_proxy_privileges$
DECLARE role_name TEXT;
BEGIN
    FOR role_name IN SELECT rolname FROM pg_roles WHERE rolname IN ('sub2api_app','sub2api_owner') LOOP
        EXECUTE format('GRANT SELECT,INSERT,DELETE ON TABLE public.pk_kilo_proxy_owners TO %I',role_name);
    END LOOP;
    IF to_regclass('public.cluster_node_credentials') IS NULL THEN RETURN; END IF;
    FOR role_name IN
        SELECT DISTINCT c.database_role FROM public.cluster_node_credentials c
        JOIN public.cluster_nodes n USING(cluster_id,node_id)
        JOIN pg_roles r ON r.rolname=c.database_role
        WHERE c.status='active' AND n.state<>'retired' AND r.rolcanlogin
    LOOP
        EXECUTE format('GRANT SELECT,INSERT,DELETE ON TABLE public.pk_kilo_proxy_owners TO %I',role_name);
    END LOOP;
END;
$kilo_recyclable_proxy_privileges$;
