-- PK.54: reuse the existing per-event manual amount as the minimum for NEW
-- native upstream type4 automatic deductions. Original costs stay untouched.
-- Local pre-forward blocks/observations/ordinary charge_allow retain their rules.
-- Previously published migrations and financial evidence are never rewritten.
ALTER TABLE usage_logs
    ADD COLUMN audit_settlement_imported_event BOOLEAN NOT NULL DEFAULT FALSE,
    ADD CONSTRAINT usage_audit_imported_type4 CHECK (NOT audit_settlement_imported_event OR request_type=4);
COMMENT ON COLUMN usage_logs.audit_settlement_imported_event IS
    'Server-owned negotiated upstream type4 receipt marker; import its actual charge only, without local count/automatic/manual duplication.';
ALTER TABLE cyber_policy_count_events
    ADD COLUMN upstream_minimum_amount NUMERIC(20,8)
        CHECK (upstream_minimum_amount BETWEEN 0.000001 AND 1000);
ALTER TABLE cyber_policy_auto_charges
    ADD COLUMN charge_source TEXT CHECK (charge_source IN ('upstream_type4','audit_charge_allow','audit_block')),
    ADD COLUMN calculation_rule TEXT CHECK (calculation_rule IN ('max_cost_multiplier_minimum','cost_multiplier')),
    ADD COLUMN minimum_amount NUMERIC(20,8) CHECK (minimum_amount BETWEEN 0 AND 1000);
COMMENT ON COLUMN cyber_policy_auto_charges.minimum_amount IS
    'Insertion-time minimum snapshot; NULL on historical rows. Not an amount actually deducted unless status=deducted.';

-- Older binaries may still build manual previews from every type4 row. Refuse
-- their shared monetary claim too; the existing count mismatch rolls back the
-- whole manual deduction rather than charging an imported event twice.
CREATE FUNCTION guard_imported_cyber_policy_charge()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS(SELECT 1 FROM usage_logs l WHERE l.id=NEW.usage_id
        AND l.created_at=NEW.usage_created_at AND l.audit_settlement_imported_event)
        OR (NEW.deduction_id IS NOT NULL AND EXISTS(SELECT 1 FROM audit_settlement_imports i WHERE i.usage_id=NEW.usage_id
            AND i.usage_created_at=NEW.usage_created_at)) THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER cyber_policy_imported_charge_guard
BEFORE INSERT ON cyber_policy_charges
FOR EACH ROW EXECUTE FUNCTION guard_imported_cyber_policy_charge();

CREATE OR REPLACE FUNCTION version_cyber_policy_settings()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.multiplier IS DISTINCT FROM OLD.multiplier
       OR NEW.group_ids IS DISTINCT FROM OLD.group_ids
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

CREATE OR REPLACE FUNCTION apply_cyber_policy_auto_disable()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    rule_enabled BOOLEAN;
    rule_threshold BIGINT;
    rule_groups BIGINT[];
    exempt_users BIGINT[];
    charge_rules JSONB;
    rule_revision BIGINT;
    upstream_minimum NUMERIC;
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
    IF NOT EXISTS (SELECT 1 FROM inserted_cyber_policy_usage WHERE request_type = 4 AND NOT audit_settlement_imported_event) THEN
        RETURN NULL;
    END IF;

    -- One committed settings snapshot per actual INSERT statement. This is
    -- admission to the durable counter, not the upstream-response timestamp.
    -- No settings row lock: preserve the existing user/settings lock order.
    SELECT auto_disable_enabled, auto_disable_threshold, group_ids, settings_revision, user_allowlist_ids, group_rules, multiplier
    INTO rule_enabled, rule_threshold, rule_groups, rule_revision, exempt_users, charge_rules, upstream_minimum
    FROM cyber_policy_settings WHERE id = 1;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'cyber policy settings missing';
    END IF;

    FOR hit_user IN
        SELECT DISTINCT user_id FROM inserted_cyber_policy_usage
        WHERE request_type = 4 AND NOT audit_settlement_imported_event ORDER BY user_id
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
            WHERE l.request_type = 4 AND NOT l.audit_settlement_imported_event AND l.user_id = hit_user
        ), recorded AS (
            INSERT INTO cyber_policy_count_events
                (usage_id, usage_created_at, user_id, group_id, group_name,
                 scope_matched, auto_disable_enabled, auto_disable_threshold,
                 settings_revision, count_status, count_reason, evaluated_at, auto_deduct_enabled, audit_multiplier, upstream_minimum_amount)
            SELECT id, created_at, user_id, group_id, group_name, scope_matched,
                   rule_enabled, rule_threshold, rule_revision,
                   CASE WHEN reason = '' THEN 'counted' ELSE 'skipped' END,
                   reason, evaluated_at_snapshot, auto_deduct_enabled, audit_multiplier, upstream_minimum
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
    usage_kind SMALLINT;
    imported_event BOOLEAN;
    minimum_snapshot NUMERIC := 0;
    source_snapshot TEXT := 'audit_block';
    rule_snapshot TEXT := 'cost_multiplier';
BEGIN
    -- Resolve only a server-persisted usage identity. A negotiated upstream
    -- event is settled once by the import worker, never charged locally again.
    IF event_usage_id IS NOT NULL THEN
        SELECT l.request_type,l.audit_settlement_imported_event INTO usage_kind,imported_event
        FROM usage_logs l WHERE l.id=event_usage_id AND l.created_at=event_usage_at
            AND l.user_id=event_user AND l.group_id IS NOT DISTINCT FROM event_group;
        source_snapshot := 'audit_charge_allow';
        IF usage_kind=4 THEN
            IF imported_event THEN RETURN; END IF;
            source_snapshot := 'upstream_type4';
            -- The amount comes from the insertion-time settings snapshot, not
            -- current settings. Old events have NULL and are never backfilled.
            SELECT e.upstream_minimum_amount INTO minimum_snapshot
            FROM cyber_policy_count_events e WHERE e.usage_id=event_usage_id
                AND e.usage_created_at=event_usage_at AND e.user_id=event_user
                AND e.group_id IS NOT DISTINCT FROM event_group;
            IF minimum_snapshot IS NOT NULL THEN rule_snapshot := 'max_cost_multiplier_minimum'; END IF;
        END IF;
    END IF;
    charge_amount := ROUND(GREATEST(GREATEST(COALESCE(event_cost,0),0) * event_multiplier,COALESCE(minimum_snapshot,0)),8);
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
        actual_cost,multiplier,amount,status,reason,balance_before,balance_after,settings_revision,evaluated_at,charge_source,calculation_rule,minimum_amount)
    VALUES(event_usage_id,event_usage_at,event_audit_id,event_user,event_group,COALESCE(event_cost,0),event_multiplier,
        charge_amount,charge_status,charge_reason,before_balance,after_balance,event_revision,event_evaluated_at,source_snapshot,rule_snapshot,minimum_snapshot)
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
            '自动安全审计扣款 / 用量#' || event_usage_id::TEXT || ' / ' ||
            CASE WHEN rule_snapshot='max_cost_multiplier_minimum' THEN
                'max(' || COALESCE(event_cost,0)::TEXT || ' × ' || event_multiplier::TEXT || ', 最低额 ' || minimum_snapshot::TEXT || ')'
            ELSE '实际费用 ' || COALESCE(event_cost,0)::TEXT || ' × ' || event_multiplier::TEXT END || ' = ' || charge_amount::TEXT);
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
