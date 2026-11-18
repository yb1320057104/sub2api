-- Proxy credentials keep their first Kilo owner across transport changes,
-- row replacement and explicit route release. Public-IP ownership remains a
-- separate, releasable admission constraint.
CREATE FUNCTION pk_kilo_proxy_credential_fingerprint(proxy_host TEXT, proxy_port INTEGER, proxy_username TEXT, proxy_password TEXT)
RETURNS TEXT LANGUAGE SQL STABLE PARALLEL SAFE AS $$
    SELECT encode(sha256(convert_to(
        encode(convert_to(lower(btrim(proxy_host, ' []')), 'UTF8'), 'hex') || ':' ||
        proxy_port::text || ':' ||
        encode(convert_to(CASE WHEN COALESCE(proxy_username, '')<>'' AND COALESCE(proxy_password, '')<>'' THEN proxy_username ELSE '' END, 'UTF8'), 'hex') || ':' ||
        encode(convert_to(CASE WHEN COALESCE(proxy_username, '')<>'' AND COALESCE(proxy_password, '')<>'' THEN proxy_password ELSE '' END, 'UTF8'), 'hex'), 'UTF8')), 'hex')
$$;

CREATE TABLE pk_kilo_proxy_owners (
    credential_fingerprint TEXT PRIMARY KEY CHECK (credential_fingerprint ~ '^[0-9a-f]{64}$'),
    account_id BIGINT REFERENCES accounts(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX pk_kilo_proxy_owners_account_idx ON pk_kilo_proxy_owners(account_id);
COMMENT ON COLUMN pk_kilo_proxy_owners.account_id IS
    'First stable Kilo account ID; NULL fences an ambiguous pre-upgrade alias shared by multiple accounts. Never cleared by route release.';

-- Recover successful use only; an unverified binding has not consumed a credential.
-- Ambiguous old aliases are blocked instead of assigning an arbitrary winner.
WITH existing AS (
    SELECT r.account_id,r.proxy_id FROM pk_kilo_egress_routes r
    JOIN accounts a ON a.id=r.account_id
    WHERE a.extra->'kilo_managed'='true'::jsonb AND r.egress_ip IS NOT NULL
), identities AS (
    SELECT pk_kilo_proxy_credential_fingerprint(p.host,p.port,p.username,p.password) AS fingerprint,e.account_id
    FROM existing e JOIN proxies p ON p.id=e.proxy_id
)
INSERT INTO pk_kilo_proxy_owners(credential_fingerprint,account_id)
SELECT fingerprint,CASE WHEN COUNT(DISTINCT account_id)=1 THEN MIN(account_id) ELSE NULL END
FROM identities GROUP BY fingerprint;

DO $kilo_proxy_privileges$
DECLARE role_name TEXT;
BEGIN
    FOR role_name IN SELECT rolname FROM pg_roles WHERE rolname IN ('sub2api_app','sub2api_owner') LOOP
        EXECUTE format('GRANT SELECT,INSERT ON TABLE public.pk_kilo_proxy_owners TO %I',role_name);
        EXECUTE format('GRANT EXECUTE ON FUNCTION public.pk_kilo_proxy_credential_fingerprint(TEXT,INTEGER,TEXT,TEXT) TO %I',role_name);
    END LOOP;
    IF to_regclass('public.cluster_node_credentials') IS NULL THEN RETURN; END IF;
    FOR role_name IN
        SELECT DISTINCT c.database_role FROM public.cluster_node_credentials c
        JOIN public.cluster_nodes n USING(cluster_id,node_id)
        JOIN pg_roles r ON r.rolname=c.database_role
        WHERE c.status='active' AND n.state<>'retired' AND r.rolcanlogin
    LOOP
        EXECUTE format('GRANT SELECT,INSERT ON TABLE public.pk_kilo_proxy_owners TO %I',role_name);
        EXECUTE format('GRANT EXECUTE ON FUNCTION public.pk_kilo_proxy_credential_fingerprint(TEXT,INTEGER,TEXT,TEXT) TO %I',role_name);
    END LOOP;
END;
$kilo_proxy_privileges$;
