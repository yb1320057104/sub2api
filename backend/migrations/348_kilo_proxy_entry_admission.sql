-- A proxy credential is the admission and concurrency unit. Different proxies
-- may share or change their public IP without affecting their allocations.
-- Historical IP observations and owner rows remain readable for older data.
DO $kilo_proxy_admission_constraints$
DECLARE item RECORD;
BEGIN
    FOR item IN SELECT conname FROM pg_constraint
        WHERE conrelid='public.pk_kilo_egress_routes'::regclass
          AND contype='f' AND confrelid='public.pk_kilo_egress_owners'::regclass
    LOOP
        EXECUTE format('ALTER TABLE public.pk_kilo_egress_routes DROP CONSTRAINT %I',item.conname);
    END LOOP;
    FOR item IN SELECT conname FROM pg_constraint
        WHERE conrelid='public.pk_kilo_egress_routes'::regclass AND contype='c'
          AND pg_get_constraintdef(oid) LIKE '%egress_ip%'
          AND pg_get_constraintdef(oid) LIKE '%state%'
    LOOP
        EXECUTE format('ALTER TABLE public.pk_kilo_egress_routes DROP CONSTRAINT %I',item.conname);
    END LOOP;
END;
$kilo_proxy_admission_constraints$;
ALTER TABLE pk_kilo_egress_routes ADD CONSTRAINT pk_kilo_route_verified_active
    CHECK (state<>'active' OR verified_at IS NOT NULL);
COMMENT ON COLUMN pk_kilo_egress_routes.egress_ip IS
    'Optional historical observation only; public IP does not determine ownership, uniqueness, stability or concurrency.';
COMMENT ON TABLE pk_kilo_egress_owners IS
    'Legacy public-IP ownership metadata; new proxy-entry admission does not read or write it.';
