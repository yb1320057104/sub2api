-- PK-044: additive account egress bindings. The legacy primary stays usable by
-- older binaries; rollback preserves bindings and never rewrites business data.
CREATE TABLE IF NOT EXISTS pk_proxy_limits (
    proxy_id BIGINT PRIMARY KEY REFERENCES proxies(id),
    max_concurrency INTEGER NOT NULL DEFAULT 0 CHECK (max_concurrency BETWEEN 0 AND 10000),
    max_accounts INTEGER NOT NULL DEFAULT 0 CHECK (max_accounts BETWEEN 0 AND 10000),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS pk_account_proxy_bindings (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    proxy_id BIGINT NOT NULL REFERENCES proxies(id),
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 99),
    PRIMARY KEY (account_id, proxy_id),
    UNIQUE (account_id, position)
);
CREATE INDEX IF NOT EXISTS pk_account_proxy_bindings_proxy_idx ON pk_account_proxy_bindings(proxy_id);

CREATE TABLE IF NOT EXISTS pk_account_proxy_fallback_sets (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    proxy_ids BIGINT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Shadows inherit their parent's complete routing set. No backfill is needed.
CREATE OR REPLACE VIEW pk_effective_account_proxy_bindings AS
SELECT a.id AS account_id, b.proxy_id, b.position
FROM accounts a JOIN accounts owner ON owner.id=COALESCE(a.parent_account_id,a.id) AND owner.deleted_at IS NULL
JOIN pk_account_proxy_bindings b ON b.account_id=owner.id WHERE a.deleted_at IS NULL
UNION ALL
SELECT a.id AS account_id, owner.proxy_id, 0 AS position
FROM accounts a JOIN accounts owner ON owner.id=COALESCE(a.parent_account_id,a.id) AND owner.deleted_at IS NULL
WHERE a.deleted_at IS NULL AND owner.proxy_id IS NOT NULL
AND NOT EXISTS (SELECT 1 FROM pk_account_proxy_bindings b WHERE b.account_id=owner.id);

-- Serializes old import/edit paths with the new allocator, including old
-- binaries after rollback. A legacy primary change intentionally resets the set.
CREATE OR REPLACE FUNCTION pk_account_proxy_capacity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed INTEGER; used INTEGER; candidate BIGINT;
BEGIN
    IF NEW.parent_account_id IS NOT NULL OR NEW.deleted_at IS NOT NULL THEN RETURN NEW; END IF;
    IF TG_OP='UPDATE' AND NEW.proxy_id IS NOT DISTINCT FROM OLD.proxy_id AND OLD.deleted_at IS NULL THEN RETURN NEW; END IF;
    PERFORM pg_advisory_xact_lock(70440001);
    IF TG_OP='UPDATE' AND NEW.proxy_id IS DISTINCT FROM OLD.proxy_id THEN
        DELETE FROM pk_account_proxy_bindings WHERE account_id=NEW.id;
        DELETE FROM pk_account_proxy_fallback_sets WHERE account_id=NEW.id;
    END IF;
    FOR candidate IN
        SELECT proxy_id FROM pk_account_proxy_bindings WHERE account_id=NEW.id
        UNION ALL SELECT NEW.proxy_id WHERE NEW.proxy_id IS NOT NULL
        AND NOT EXISTS(SELECT 1 FROM pk_account_proxy_bindings WHERE account_id=NEW.id)
    LOOP
        SELECT max_accounts INTO allowed FROM pk_proxy_limits WHERE proxy_id=candidate;
        IF COALESCE(allowed,0)>0 THEN
            SELECT COUNT(*) INTO used FROM pk_effective_account_proxy_bindings b
            JOIN accounts a ON a.id=b.account_id AND a.parent_account_id IS NULL
            WHERE b.proxy_id=candidate AND a.id<>NEW.id;
            IF used>=allowed THEN RAISE EXCEPTION '代理已达到绑定账号上限' USING ERRCODE='23514'; END IF;
        END IF;
    END LOOP;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS pk_account_proxy_capacity ON accounts;
CREATE TRIGGER pk_account_proxy_capacity BEFORE INSERT OR UPDATE OF proxy_id,deleted_at ON accounts
FOR EACH ROW EXECUTE FUNCTION pk_account_proxy_capacity_guard();

CREATE OR REPLACE FUNCTION pk_proxy_bindings_delete_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.deleted_at IS NOT NULL AND OLD.deleted_at IS NULL THEN
        PERFORM pg_advisory_xact_lock(70440001);
        IF EXISTS (SELECT 1 FROM pk_effective_account_proxy_bindings WHERE proxy_id=OLD.id) THEN
            RAISE EXCEPTION '代理已绑定账号，不能删除' USING ERRCODE='23514';
        END IF;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS pk_proxy_bindings_delete ON proxies;
CREATE TRIGGER pk_proxy_bindings_delete BEFORE UPDATE OF deleted_at ON proxies
FOR EACH ROW EXECUTE FUNCTION pk_proxy_bindings_delete_guard();
