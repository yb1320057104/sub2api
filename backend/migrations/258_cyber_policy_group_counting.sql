-- PK-026: independent group selection for ranking auto-disable counts.
-- Historical usage and existing user counters are deliberately not rewritten.
ALTER TABLE cyber_policy_settings
    ADD COLUMN IF NOT EXISTS group_ids BIGINT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS user_allowlist_ids BIGINT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS group_rules JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS settings_revision BIGINT NOT NULL DEFAULT 1;

ALTER TABLE cyber_policy_settings
    ADD CONSTRAINT cyber_policy_group_ids_valid
        CHECK (cardinality(group_ids) <= 1000 AND array_position(group_ids, NULL) IS NULL AND 0 < ALL(group_ids)),
    ADD CONSTRAINT cyber_policy_settings_revision_positive CHECK (settings_revision > 0);
ALTER TABLE cyber_policy_settings
    ADD CONSTRAINT cyber_policy_user_allowlist_valid
        CHECK (cardinality(user_allowlist_ids) <= 1000 AND array_position(user_allowlist_ids, NULL) IS NULL AND 0 < ALL(user_allowlist_ids));

CREATE OR REPLACE FUNCTION valid_cyber_policy_group_rules(value JSONB)
RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    item JSONB;
    seen BIGINT[] := '{}';
    group_id BIGINT;
    multiplier NUMERIC;
BEGIN
    IF jsonb_typeof(value) <> 'array' OR jsonb_array_length(value) > 1000 THEN RETURN FALSE; END IF;
    FOR item IN SELECT * FROM jsonb_array_elements(value) LOOP
        IF jsonb_typeof(item) <> 'object' OR NOT (item ?& ARRAY['group_id','multiplier','auto_deduct_enabled']) OR jsonb_typeof(item->'group_id') <> 'number'
           OR jsonb_typeof(item->'multiplier') <> 'string' OR jsonb_typeof(item->'auto_deduct_enabled') <> 'boolean'
           OR (item->>'group_id') !~ '^[1-9][0-9]*$'
           OR (item->>'multiplier') !~ '^[0-9]+(\.[0-9]{1,6})?$' THEN RETURN FALSE; END IF;
        group_id := (item->>'group_id')::BIGINT;
        multiplier := (item->>'multiplier')::NUMERIC;
        IF group_id = ANY(seen) OR multiplier < 0.000001 OR multiplier > 1000 THEN RETURN FALSE; END IF;
        seen := array_append(seen, group_id);
    END LOOP;
    RETURN TRUE;
EXCEPTION WHEN OTHERS THEN RETURN FALSE;
END;
$$;
ALTER TABLE cyber_policy_settings ADD CONSTRAINT cyber_policy_group_rules_valid CHECK(valid_cyber_policy_group_rules(group_rules));

-- Maintain revisions in the database so a rollback to an older application
-- (whose UPDATE does not mention the new columns) still versions rule changes.
CREATE OR REPLACE FUNCTION version_cyber_policy_settings()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.group_ids IS DISTINCT FROM OLD.group_ids
       OR NEW.user_allowlist_ids IS DISTINCT FROM OLD.user_allowlist_ids
       OR NEW.group_rules IS DISTINCT FROM OLD.group_rules
       OR NEW.auto_disable_enabled IS DISTINCT FROM OLD.auto_disable_enabled
       OR NEW.auto_disable_threshold IS DISTINCT FROM OLD.auto_disable_threshold
       OR NEW.reset_on_manual_enable IS DISTINCT FROM OLD.reset_on_manual_enable THEN
        NEW.settings_revision := OLD.settings_revision + 1;
    ELSE
        NEW.settings_revision := OLD.settings_revision;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cyber_policy_settings_revision
BEFORE UPDATE ON cyber_policy_settings
FOR EACH ROW EXECUTE FUNCTION version_cyber_policy_settings();

-- Only newly persisted request_type=4 records get a sidecar. No FK to usage,
-- groups or users: retention, partition drops and renames must not rewrite
-- immutable count evidence. Missing sidecars mean historical status unknown.
CREATE TABLE IF NOT EXISTS cyber_policy_count_events (
    usage_id BIGINT NOT NULL,
    usage_created_at TIMESTAMPTZ NOT NULL,
    user_id BIGINT NOT NULL,
    group_id BIGINT,
    group_name TEXT NOT NULL DEFAULT '',
    scope_matched BOOLEAN NOT NULL,
    auto_disable_enabled BOOLEAN NOT NULL,
    auto_disable_threshold BIGINT NOT NULL,
    settings_revision BIGINT NOT NULL CHECK (settings_revision > 0),
    count_status TEXT NOT NULL CHECK (count_status IN ('counted', 'skipped')),
    count_reason TEXT NOT NULL,
    auto_deduct_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    audit_multiplier NUMERIC(10,6) NOT NULL DEFAULT 1 CHECK(audit_multiplier BETWEEN 0.000001 AND 1000),
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (usage_id, usage_created_at),
    CHECK ((count_status = 'counted' AND count_reason = '' AND scope_matched AND auto_disable_enabled)
        OR (count_status = 'skipped' AND count_reason IN ('group_not_enabled', 'user_exempt', 'auto_disable_off', 'admin_exempt', 'user_unavailable')))
);

CREATE OR REPLACE FUNCTION apply_cyber_policy_auto_disable()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    rule_enabled BOOLEAN;
    rule_threshold BIGINT;
    rule_groups BIGINT[];
    exempt_users BIGINT[];
    charge_rules JSONB;
    rule_revision BIGINT;
    evaluated_at_snapshot TIMESTAMPTZ := clock_timestamp();
    hit_user BIGINT;
    target_role TEXT;
    target_status TEXT;
    target_deleted_at TIMESTAMPTZ;
    target_exists BOOLEAN;
    inserted_count BIGINT;
    last_at TIMESTAMPTZ;
    accumulated BIGINT;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM inserted_cyber_policy_usage WHERE request_type = 4) THEN
        RETURN NULL;
    END IF;

    -- One committed settings snapshot per actual INSERT statement. This is
    -- admission to the durable counter, not the upstream-response timestamp.
    -- No settings row lock: preserve the existing user/settings lock order.
    SELECT auto_disable_enabled, auto_disable_threshold, group_ids, settings_revision, user_allowlist_ids, group_rules
    INTO rule_enabled, rule_threshold, rule_groups, rule_revision, exempt_users, charge_rules
    FROM cyber_policy_settings WHERE id = 1;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'cyber policy settings missing';
    END IF;

    FOR hit_user IN
        SELECT DISTINCT user_id FROM inserted_cyber_policy_usage
        WHERE request_type = 4 ORDER BY user_id
    LOOP
        -- Compatible with the usage FK KEY SHARE lock. Serialize counter,
        -- manual re-enable and administrator-role changes for this user.
        SELECT role, status, deleted_at
        INTO target_role, target_status, target_deleted_at
        FROM users WHERE id = hit_user FOR NO KEY UPDATE;
        target_exists := FOUND;

        WITH classified AS (
            SELECT l.id, l.created_at, l.user_id, l.group_id,
                   COALESCE(g.name, '') AS group_name,
                   COALESCE(l.group_id = ANY(rule_groups), FALSE) AS scope_matched,
                   COALESCE((r.value->>'auto_deduct_enabled')::BOOLEAN, FALSE) AS auto_deduct_enabled,
                   COALESCE((r.value->>'multiplier')::NUMERIC, 1) AS audit_multiplier,
                   CASE
                     WHEN NOT COALESCE(l.group_id = ANY(rule_groups), FALSE) THEN 'group_not_enabled'
                     WHEN l.user_id = ANY(exempt_users) THEN 'user_exempt'
                     WHEN NOT rule_enabled THEN 'auto_disable_off'
                     WHEN NOT target_exists OR target_deleted_at IS NOT NULL THEN 'user_unavailable'
                     WHEN target_role = 'admin' THEN 'admin_exempt'
                     ELSE ''
                   END AS reason
            FROM inserted_cyber_policy_usage l
            LEFT JOIN groups g ON g.id = l.group_id
            LEFT JOIN LATERAL (SELECT value FROM jsonb_array_elements(charge_rules) WHERE (value->>'group_id')::BIGINT=l.group_id) r ON TRUE
            WHERE l.request_type = 4 AND l.user_id = hit_user
        ), recorded AS (
            INSERT INTO cyber_policy_count_events
                (usage_id, usage_created_at, user_id, group_id, group_name,
                 scope_matched, auto_disable_enabled, auto_disable_threshold,
                 settings_revision, count_status, count_reason, evaluated_at, auto_deduct_enabled, audit_multiplier)
            SELECT id, created_at, user_id, group_id, group_name, scope_matched,
                   rule_enabled, rule_threshold, rule_revision,
                   CASE WHEN reason = '' THEN 'counted' ELSE 'skipped' END,
                   reason, evaluated_at_snapshot, auto_deduct_enabled, audit_multiplier
            FROM classified
            ON CONFLICT (usage_id, usage_created_at) DO NOTHING
            RETURNING count_status, usage_created_at
        )
        SELECT COUNT(*), MAX(usage_created_at) INTO inserted_count, last_at
        FROM recorded WHERE count_status = 'counted';

        IF inserted_count = 0 THEN
            CONTINUE;
        END IF;
        INSERT INTO cyber_policy_user_counters AS counter
            (user_id, trigger_count, last_triggered_at)
        VALUES (hit_user, inserted_count, last_at)
        ON CONFLICT (user_id) DO UPDATE
        SET trigger_count = counter.trigger_count + EXCLUDED.trigger_count,
            last_triggered_at = GREATEST(counter.last_triggered_at, EXCLUDED.last_triggered_at)
        RETURNING trigger_count INTO accumulated;

        IF accumulated >= rule_threshold AND target_status = 'active' THEN
            UPDATE users SET status = 'disabled', updated_at = NOW() WHERE id = hit_user;
            UPDATE cyber_policy_user_counters SET auto_disabled_at = NOW() WHERE user_id = hit_user;
        END IF;
    END LOOP;
    RETURN NULL;
END;
$$;

-- Keep migration 245's parent-table statement trigger and manual-reset trigger.
-- Ordinary, batched and best-effort application writes all target usage_logs.
-- Direct leaf-partition writes remain outside this supported insertion path.
COMMENT ON TABLE cyber_policy_count_events IS
    'Immutable auto-disable count decisions for newly inserted cyber usage, evaluated using settings at insertion time; no historical backfill and no effect on manual deductions.';

-- Actual pre-forward audit blocks have no upstream account or usage bill. Keep
-- them independently so ranking can show them without fabricating usage_logs.
-- event_key is generated by the server for the request/turn, never copied from
-- an untrusted client request ID. Multiple audit layers share that identity.
CREATE TABLE IF NOT EXISTS cyber_policy_audit_blocks (
    id BIGSERIAL PRIMARY KEY,
    event_key VARCHAR(128) UNIQUE NOT NULL,
    request_id VARCHAR(128) NOT NULL DEFAULT '',
    user_id BIGINT NOT NULL,
    group_id BIGINT,
    group_name TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL CHECK (source IN ('content_audit', 'prompt_audit', 'combined_audit')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    scope_matched BOOLEAN NOT NULL,
    auto_disable_enabled BOOLEAN NOT NULL,
    auto_disable_threshold BIGINT NOT NULL,
    settings_revision BIGINT NOT NULL CHECK (settings_revision > 0),
    count_status TEXT NOT NULL CHECK (count_status IN ('counted', 'skipped')),
    count_reason TEXT NOT NULL,
    auto_deduct_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    audit_multiplier NUMERIC(10,6) NOT NULL DEFAULT 1 CHECK(audit_multiplier BETWEEN 0.000001 AND 1000),
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK ((count_status = 'counted' AND count_reason = '' AND scope_matched AND auto_disable_enabled)
        OR (count_status = 'skipped' AND count_reason IN ('group_not_enabled', 'user_exempt', 'auto_disable_off', 'admin_exempt', 'user_unavailable')))
);
CREATE INDEX IF NOT EXISTS idx_cyber_policy_audit_blocks_user_created
    ON cyber_policy_audit_blocks(user_id, created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION classify_cyber_policy_audit_block()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    rule_groups BIGINT[];
    exempt_users BIGINT[];
    charge_rules JSONB;
    charge_rule JSONB;
    target_role TEXT;
    target_deleted_at TIMESTAMPTZ;
    target_exists BOOLEAN;
BEGIN
    SELECT auto_disable_enabled, auto_disable_threshold, group_ids, settings_revision, user_allowlist_ids, group_rules
    INTO NEW.auto_disable_enabled, NEW.auto_disable_threshold, rule_groups, NEW.settings_revision, exempt_users, charge_rules
    FROM cyber_policy_settings WHERE id = 1;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'cyber policy settings missing';
    END IF;
    NEW.evaluated_at := clock_timestamp();
    NEW.scope_matched := COALESCE(NEW.group_id = ANY(rule_groups), FALSE);
    SELECT value INTO charge_rule FROM jsonb_array_elements(charge_rules) WHERE (value->>'group_id')::BIGINT=NEW.group_id;
    NEW.auto_deduct_enabled := COALESCE((charge_rule->>'auto_deduct_enabled')::BOOLEAN, FALSE);
    NEW.audit_multiplier := COALESCE((charge_rule->>'multiplier')::NUMERIC, 1);
    SELECT COALESCE((SELECT name FROM groups WHERE id = NEW.group_id), '') INTO NEW.group_name;

    -- Production persists one server request/turn at a time. Hold the same
    -- user lock as usage counting/manual re-enable through the AFTER trigger.
    SELECT role, deleted_at INTO target_role, target_deleted_at
    FROM users WHERE id = NEW.user_id FOR NO KEY UPDATE;
    target_exists := FOUND;
    NEW.count_reason := CASE
        WHEN NOT NEW.scope_matched THEN 'group_not_enabled'
        WHEN NEW.user_id = ANY(exempt_users) THEN 'user_exempt'
        WHEN NOT NEW.auto_disable_enabled THEN 'auto_disable_off'
        WHEN NOT target_exists OR target_deleted_at IS NOT NULL THEN 'user_unavailable'
        WHEN target_role = 'admin' THEN 'admin_exempt'
        ELSE ''
    END;
    NEW.count_status := CASE WHEN NEW.count_reason = '' THEN 'counted' ELSE 'skipped' END;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION count_cyber_policy_audit_block()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    accumulated BIGINT;
    target_status TEXT;
BEGIN
    -- BEFORE classification must have no counting side effects: PostgreSQL
    -- also executes BEFORE INSERT when ON CONFLICT later discards the row.
    IF NEW.count_status <> 'counted' THEN
        RETURN NULL;
    END IF;
    INSERT INTO cyber_policy_user_counters AS counter(user_id, trigger_count, last_triggered_at)
    VALUES(NEW.user_id, 1, NEW.created_at)
    ON CONFLICT(user_id) DO UPDATE
    SET trigger_count = counter.trigger_count + 1,
        last_triggered_at = GREATEST(counter.last_triggered_at, EXCLUDED.last_triggered_at)
    RETURNING trigger_count INTO accumulated;
    SELECT status INTO target_status FROM users WHERE id = NEW.user_id;
    IF accumulated >= NEW.auto_disable_threshold AND target_status = 'active' THEN
        UPDATE users SET status = 'disabled', updated_at = NOW() WHERE id = NEW.user_id;
        UPDATE cyber_policy_user_counters SET auto_disabled_at = NOW() WHERE user_id = NEW.user_id;
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER cyber_policy_audit_block_classify
BEFORE INSERT ON cyber_policy_audit_blocks
FOR EACH ROW EXECUTE FUNCTION classify_cyber_policy_audit_block();
CREATE TRIGGER cyber_policy_audit_block_count
AFTER INSERT ON cyber_policy_audit_blocks
FOR EACH ROW EXECUTE FUNCTION count_cyber_policy_audit_block();

COMMENT ON TABLE cyber_policy_audit_blocks IS
    'Server-idempotent actual pre-forward audit block evidence; independent of usage bills and manual deductions, classified at insertion time with no historical backfill.';

-- Separate exact monetary evidence. The original manual preview amount and
-- multiplier remain untouched; only successful positive auto-charges occupy
-- the existing shared usage identity to prevent subsequent manual double debit.
CREATE TABLE cyber_policy_auto_charges (
    id BIGSERIAL PRIMARY KEY,
    usage_id BIGINT,
    usage_created_at TIMESTAMPTZ,
    audit_id BIGINT UNIQUE,
    user_id BIGINT NOT NULL,
    group_id BIGINT,
    actual_cost NUMERIC(20,10) NOT NULL,
    multiplier NUMERIC(10,6) NOT NULL,
    amount NUMERIC(30,8) NOT NULL CHECK(amount >= 0),
    status TEXT NOT NULL CHECK(status IN ('deducted','zero_cost','insufficient_balance','skipped','previously_charged','failed')),
    reason TEXT NOT NULL DEFAULT '',
    balance_before NUMERIC(20,8),
    balance_after NUMERIC(20,8),
    settings_revision BIGINT NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(usage_id,usage_created_at),
    CHECK ((usage_id IS NOT NULL AND usage_created_at IS NOT NULL AND audit_id IS NULL)
        OR (usage_id IS NULL AND usage_created_at IS NULL AND audit_id IS NOT NULL))
);
CREATE INDEX idx_cyber_policy_auto_charges_user ON cyber_policy_auto_charges(user_id,evaluated_at DESC,id DESC);
ALTER TABLE cyber_policy_charges
    ALTER COLUMN deduction_id DROP NOT NULL,
    ADD COLUMN auto_charge_id BIGINT UNIQUE REFERENCES cyber_policy_auto_charges(id),
    ADD CONSTRAINT cyber_policy_charge_origin CHECK ((deduction_id IS NOT NULL) <> (auto_charge_id IS NOT NULL));

CREATE TABLE cyber_policy_auto_charge_outbox (
    user_id BIGINT PRIMARY KEY,
    revision BIGINT NOT NULL DEFAULT 1,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    stage SMALLINT NOT NULL DEFAULT 0 CHECK(stage IN (0,1)),
    claimed_at TIMESTAMPTZ,
    claimed_by TEXT,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT
);
CREATE INDEX idx_cyber_policy_auto_charge_outbox_available ON cyber_policy_auto_charge_outbox(available_at,user_id);

CREATE OR REPLACE FUNCTION apply_cyber_policy_auto_charge(
    event_usage_id BIGINT, event_usage_at TIMESTAMPTZ, event_audit_id BIGINT,
    event_user BIGINT, event_group BIGINT, event_cost NUMERIC,
    event_enabled BOOLEAN, event_multiplier NUMERIC, event_scope BOOLEAN,
    event_count_reason TEXT, event_revision BIGINT, event_evaluated_at TIMESTAMPTZ)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE
    target_role TEXT;
    target_deleted_at TIMESTAMPTZ;
    before_balance NUMERIC;
    after_balance NUMERIC;
    target_exists BOOLEAN;
    charge_amount NUMERIC;
    charge_status TEXT := 'skipped';
    charge_reason TEXT;
    charge_id BIGINT;
    claimed_usage BIGINT;
BEGIN
    charge_amount := ROUND(GREATEST(COALESCE(event_cost,0),0) * event_multiplier,8);
    SELECT role,deleted_at,balance INTO target_role,target_deleted_at,before_balance
    FROM users WHERE id=event_user FOR NO KEY UPDATE;
    target_exists := FOUND;
    charge_reason := CASE
        WHEN NOT event_scope THEN 'group_not_enabled'
        WHEN event_count_reason='user_exempt' THEN 'user_exempt'
        WHEN NOT target_exists OR target_deleted_at IS NOT NULL THEN 'user_unavailable'
        WHEN target_role='admin' THEN 'admin_exempt'
        WHEN NOT event_enabled THEN 'auto_deduct_off'
        WHEN event_cost < 0 THEN 'invalid_cost'
        ELSE '' END;
    IF charge_reason='' THEN
        IF charge_amount=0 THEN charge_status := 'zero_cost';
        ELSIF before_balance < charge_amount THEN charge_status := 'insufficient_balance';
        ELSE charge_status := 'deducted'; END IF;
    END IF;
    after_balance := before_balance;
    INSERT INTO cyber_policy_auto_charges(usage_id,usage_created_at,audit_id,user_id,group_id,
        actual_cost,multiplier,amount,status,reason,balance_before,balance_after,settings_revision,evaluated_at)
    VALUES(event_usage_id,event_usage_at,event_audit_id,event_user,event_group,COALESCE(event_cost,0),event_multiplier,
        charge_amount,charge_status,charge_reason,before_balance,after_balance,event_revision,event_evaluated_at)
    ON CONFLICT DO NOTHING RETURNING id INTO charge_id;
    IF charge_id IS NULL OR charge_status <> 'deducted' THEN RETURN; END IF;

    -- Automatic penalties must not discard the original usage bill. This
    -- subtransaction commits all monetary effects together, or preserves a
    -- failed decision with none of them. No background debit retry is queued.
    BEGIN
        -- Pre-forward audit events have actual cost zero, hence never enter here.
        IF event_usage_id IS NULL THEN RAISE EXCEPTION 'audit block cannot have an upstream charge'; END IF;
        INSERT INTO cyber_policy_charges(usage_id,usage_created_at,user_id,auto_charge_id)
        VALUES(event_usage_id,event_usage_at,event_user,charge_id)
        ON CONFLICT(usage_id,usage_created_at) DO NOTHING RETURNING usage_id INTO claimed_usage;
        IF claimed_usage IS NULL THEN
            UPDATE cyber_policy_auto_charges SET status='previously_charged' WHERE id=charge_id;
            RETURN;
        END IF;
        UPDATE users SET balance=balance-charge_amount,updated_at=NOW()
        WHERE id=event_user AND deleted_at IS NULL AND balance>=charge_amount RETURNING balance INTO after_balance;
        IF NOT FOUND THEN RAISE EXCEPTION 'automatic charge balance changed under user lock'; END IF;
        UPDATE cyber_policy_auto_charges SET balance_after=after_balance WHERE id=charge_id;
        INSERT INTO redeem_codes(code,type,value,status,used_by,used_at,notes)
        VALUES('CA-' || SUBSTR(md5(charge_id::TEXT || ':' || event_usage_id::TEXT || ':' || event_usage_at::TEXT),1,29),'admin_balance',-charge_amount,'used',event_user,NOW(),
            '自动安全审计扣款 / 用量#' || event_usage_id::TEXT || ' / 实际费用 ' || event_cost::TEXT || ' × ' || event_multiplier::TEXT || ' = ' || charge_amount::TEXT);
        INSERT INTO cyber_policy_auto_charge_outbox AS pending(user_id)
        VALUES(event_user) ON CONFLICT(user_id) DO UPDATE
        SET revision=pending.revision+1,available_at=NOW(),stage=0,claimed_at=NULL,claimed_by=NULL;
        INSERT INTO auth_cache_invalidation_outbox(cache_key)
        SELECT encode(sha256(convert_to(key,'UTF8')),'hex') FROM api_keys
        WHERE user_id=event_user AND deleted_at IS NULL AND key<>'';
    EXCEPTION WHEN OTHERS THEN
        UPDATE cyber_policy_auto_charges
        SET status='failed',reason='charge_failed',balance_after=before_balance WHERE id=charge_id;
    END;
EXCEPTION WHEN OTHERS THEN
    -- Even unavailable penalty evidence must not abort a business usage write.
    -- This outer subtransaction rolls back every charge effect before returning.
    -- Do not include SQLERRM: it can contain database values or statement text.
    RAISE WARNING 'cyber_policy_auto_charge_evidence_unavailable';
END;
$$;

CREATE OR REPLACE FUNCTION charge_cyber_policy_usage_event()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE actual_cost NUMERIC;
BEGIN
    SELECT l.actual_cost INTO actual_cost FROM usage_logs l
    WHERE l.id=NEW.usage_id AND l.created_at=NEW.usage_created_at;
    IF NOT FOUND THEN
        RAISE WARNING 'cyber_policy_auto_charge_usage_unavailable';
        RETURN NULL;
    END IF;
    PERFORM apply_cyber_policy_auto_charge(NEW.usage_id,NEW.usage_created_at,NULL,
        NEW.user_id,NEW.group_id,actual_cost,NEW.auto_deduct_enabled,NEW.audit_multiplier,
        NEW.scope_matched,NEW.count_reason,NEW.settings_revision,NEW.evaluated_at);
    RETURN NULL;
EXCEPTION WHEN OTHERS THEN
    RAISE WARNING 'cyber_policy_auto_charge_usage_unavailable';
    RETURN NULL;
END;
$$;
CREATE TRIGGER cyber_policy_usage_auto_charge AFTER INSERT ON cyber_policy_count_events
FOR EACH ROW EXECUTE FUNCTION charge_cyber_policy_usage_event();

CREATE OR REPLACE FUNCTION charge_cyber_policy_audit_event()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM apply_cyber_policy_auto_charge(NULL,NULL,NEW.id,NEW.user_id,NEW.group_id,0,
        NEW.auto_deduct_enabled,NEW.audit_multiplier,NEW.scope_matched,NEW.count_reason,
        NEW.settings_revision,NEW.evaluated_at);
    RETURN NULL;
END;
$$;
CREATE TRIGGER cyber_policy_audit_auto_charge AFTER INSERT ON cyber_policy_audit_blocks
FOR EACH ROW EXECUTE FUNCTION charge_cyber_policy_audit_event();
