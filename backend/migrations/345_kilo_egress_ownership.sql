-- Kilo managed accounts retain public-IP ownership until an explicit release.
-- Leases are not reaped by TTL: expiry cancels the keeper's upstream but cannot
-- prove that a response body has closed on a disconnected node.
CREATE TABLE pk_kilo_egress_owners (
    egress_ip INET PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (egress_ip, account_id),
    CHECK ((family(egress_ip)=4 AND masklen(egress_ip)=32)
        OR (family(egress_ip)=6 AND masklen(egress_ip)=128
            AND NOT (egress_ip <<= '::ffff:0:0/96'::inet)))
);
CREATE INDEX pk_kilo_egress_owners_account_idx ON pk_kilo_egress_owners(account_id);

-- Managed Kilo traffic always requires a normal account proxy binding. Direct
-- routes are deliberately not representable, so NULL uniqueness is unnecessary.
CREATE TABLE pk_kilo_egress_routes (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    proxy_id BIGINT NOT NULL REFERENCES proxies(id) ON DELETE RESTRICT,
    egress_ip INET,
    config_hash TEXT NOT NULL CHECK (length(config_hash)=64),
    verified_at TIMESTAMPTZ,
    state TEXT NOT NULL DEFAULT 'unverified' CHECK (state IN ('active','unverified','draining','failed')),
    max_concurrency INTEGER NOT NULL DEFAULT 10 CHECK (max_concurrency BETWEEN 1 AND 10000),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (account_id, proxy_id),
    UNIQUE (id, account_id),
    FOREIGN KEY (egress_ip, account_id) REFERENCES pk_kilo_egress_owners(egress_ip, account_id) ON DELETE RESTRICT,
    CHECK (state <> 'active' OR (egress_ip IS NOT NULL AND verified_at IS NOT NULL))
);
CREATE INDEX pk_kilo_egress_routes_ip_idx ON pk_kilo_egress_routes(egress_ip, account_id);
CREATE INDEX pk_kilo_egress_routes_proxy_idx ON pk_kilo_egress_routes(proxy_id);

CREATE TABLE pk_kilo_egress_leases (
    id UUID PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    route_id BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (route_id, account_id) REFERENCES pk_kilo_egress_routes(id, account_id) ON DELETE RESTRICT
);
CREATE INDEX pk_kilo_egress_leases_route_idx ON pk_kilo_egress_leases(route_id);
CREATE INDEX pk_kilo_egress_leases_expiry_idx ON pk_kilo_egress_leases(expires_at);

-- Match the active-node grants used by the surrounding PK migrations, while
-- retaining installations that use the standalone application/owner roles.
DO $kilo_egress_privileges$
DECLARE role_name TEXT;
BEGIN
    FOR role_name IN SELECT rolname FROM pg_roles WHERE rolname IN ('sub2api_app','sub2api_owner') LOOP
        EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE public.pk_kilo_egress_owners,public.pk_kilo_egress_routes,public.pk_kilo_egress_leases TO %I',role_name);
        EXECUTE format('GRANT USAGE,SELECT ON SEQUENCE public.pk_kilo_egress_routes_id_seq TO %I',role_name);
    END LOOP;
    IF to_regclass('public.cluster_node_credentials') IS NULL THEN RETURN; END IF;
    FOR role_name IN
        SELECT DISTINCT c.database_role FROM public.cluster_node_credentials c
        JOIN public.cluster_nodes n USING(cluster_id,node_id)
        JOIN pg_roles r ON r.rolname=c.database_role
        WHERE c.status='active' AND n.state<>'retired' AND r.rolcanlogin
    LOOP
        EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE public.pk_kilo_egress_owners,public.pk_kilo_egress_routes,public.pk_kilo_egress_leases TO %I',role_name);
        EXECUTE format('GRANT USAGE,SELECT ON SEQUENCE public.pk_kilo_egress_routes_id_seq TO %I',role_name);
    END LOOP;
END;
$kilo_egress_privileges$;
