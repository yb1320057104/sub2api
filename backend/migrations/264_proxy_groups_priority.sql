-- PK-045: explicit static proxy groups and opt-in strict account priority.
-- Existing accounts, bindings, limits and group policies remain unchanged.
CREATE TABLE IF NOT EXISTS pk_proxy_groups (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL CHECK (char_length(trim(name)) > 0),
    description VARCHAR(1000) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS pk_proxy_groups_name_unique ON pk_proxy_groups(lower(name));
CREATE TABLE IF NOT EXISTS pk_proxy_group_members (
    proxy_id BIGINT PRIMARY KEY REFERENCES proxies(id),
    proxy_group_id BIGINT NOT NULL REFERENCES pk_proxy_groups(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS pk_proxy_group_members_group_idx ON pk_proxy_group_members(proxy_group_id,proxy_id);
CREATE TABLE IF NOT EXISTS pk_group_proxy_priority (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    strict_priority BOOLEAN NOT NULL DEFAULT FALSE,
    proxy_group_id BIGINT REFERENCES pk_proxy_groups(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (NOT strict_priority OR proxy_group_id IS NOT NULL)
);

-- Member edits and legacy proxy deletion share the allocator lock. Existing
-- accounts retain their binding snapshot when an unused IP group is removed.
CREATE OR REPLACE FUNCTION pk_proxy_group_strict_member_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_group BIGINT; new_group BIGINT;
BEGIN
    PERFORM pg_advisory_xact_lock(70440001);
    IF TG_OP <> 'INSERT' THEN old_group := OLD.proxy_group_id; END IF;
    IF TG_OP <> 'DELETE' THEN new_group := NEW.proxy_group_id; END IF;
    IF TG_OP='UPDATE' AND old_group=new_group AND OLD.proxy_id=NEW.proxy_id THEN RETURN NEW; END IF;
    IF EXISTS(SELECT 1 FROM pk_group_proxy_priority p JOIN groups g ON g.id=p.group_id AND g.deleted_at IS NULL
              WHERE p.strict_priority AND (p.proxy_group_id=old_group OR p.proxy_group_id=new_group)) THEN
        RAISE EXCEPTION 'IP分组正被严格优先级策略使用，请先关闭相关开关再调整成员' USING ERRCODE='23514';
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS pk_proxy_group_strict_member ON pk_proxy_group_members;
CREATE TRIGGER pk_proxy_group_strict_member BEFORE INSERT OR UPDATE OR DELETE ON pk_proxy_group_members
FOR EACH ROW EXECUTE FUNCTION pk_proxy_group_strict_member_guard();

CREATE OR REPLACE FUNCTION pk_proxy_group_strict_delete_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.deleted_at IS NOT NULL AND OLD.deleted_at IS NULL THEN
        PERFORM pg_advisory_xact_lock(70440001);
        IF EXISTS(SELECT 1 FROM pk_proxy_group_members m JOIN pk_group_proxy_priority p ON p.proxy_group_id=m.proxy_group_id
                  JOIN groups g ON g.id=p.group_id AND g.deleted_at IS NULL WHERE m.proxy_id=OLD.id AND p.strict_priority) THEN
            RAISE EXCEPTION '代理所属IP分组正被严格优先级策略使用，请先关闭相关开关再删除' USING ERRCODE='23514';
        END IF;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS pk_proxy_group_strict_delete ON proxies;
CREATE TRIGGER pk_proxy_group_strict_delete BEFORE UPDATE OF deleted_at ON proxies
FOR EACH ROW EXECUTE FUNCTION pk_proxy_group_strict_delete_guard();
