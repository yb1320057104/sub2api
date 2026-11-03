-- Additive audit policy evidence. Published rows and migration checksums stay
-- unchanged. NULL means the historical event did not record a scored policy.
ALTER TABLE content_moderation_logs
    ADD COLUMN risk_score NUMERIC CHECK (risk_score BETWEEN 0 AND 100),
    ADD COLUMN risk_level TEXT CHECK (risk_level IN ('none','low','medium','high','critical')),
    ADD COLUMN policy_action TEXT CHECK (policy_action IN ('allow','block','charge_allow'));
ALTER TABLE prompt_audit_events
    ADD COLUMN risk_score NUMERIC CHECK (risk_score BETWEEN 0 AND 100),
    ADD COLUMN policy_action TEXT CHECK (policy_action IN ('allow','block','charge_allow'));
ALTER TABLE batch_image_jobs
    ADD COLUMN audit_snapshot JSONB CHECK (jsonb_typeof(audit_snapshot)='object');

-- Keep the existing table name and block defaults so older binaries can be
-- restored. A charge_allow row references a real completed ordinary bill;
-- it never masquerades as request_type=4 or becomes manually deductible.
ALTER TABLE cyber_policy_audit_blocks
    ADD COLUMN policy_action TEXT NOT NULL DEFAULT 'block'
        CHECK (policy_action IN ('block','charge_allow')),
    ADD COLUMN risk_score NUMERIC CHECK (risk_score BETWEEN 0 AND 100),
    ADD COLUMN risk_level TEXT CHECK (risk_level IN ('none','low','medium','high','critical')),
    ADD COLUMN usage_id BIGINT,
    ADD COLUMN usage_created_at TIMESTAMPTZ,
    ADD CONSTRAINT cyber_audit_action_usage CHECK (
        (policy_action='block' AND usage_id IS NULL AND usage_created_at IS NULL) OR
        (policy_action='charge_allow' AND usage_id IS NOT NULL AND usage_created_at IS NOT NULL)),
    ADD CONSTRAINT cyber_audit_action_usage_unique UNIQUE(usage_id,usage_created_at);

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
    -- Do not count an event for another user/group, an absent bill, or an
    -- upstream policy refusal already handled by migration 258's usage path.
    IF NEW.policy_action='charge_allow' THEN
        PERFORM 1 FROM usage_logs l WHERE l.id=NEW.usage_id AND l.created_at=NEW.usage_created_at
            AND l.user_id=NEW.user_id AND l.group_id IS NOT DISTINCT FROM NEW.group_id
            AND l.request_type<>4;
        IF NOT FOUND THEN RAISE EXCEPTION 'audit usage identity unavailable'; END IF;
    END IF;
    SELECT auto_disable_enabled,auto_disable_threshold,group_ids,settings_revision,user_allowlist_ids,group_rules
    INTO NEW.auto_disable_enabled,NEW.auto_disable_threshold,rule_groups,NEW.settings_revision,exempt_users,charge_rules
    FROM cyber_policy_settings WHERE id=1;
    IF NOT FOUND THEN RAISE EXCEPTION 'cyber policy settings missing'; END IF;
    NEW.evaluated_at := clock_timestamp();
    NEW.scope_matched := COALESCE(NEW.group_id=ANY(rule_groups),FALSE);
    SELECT value INTO charge_rule FROM jsonb_array_elements(charge_rules)
        WHERE (value->>'group_id')::BIGINT=NEW.group_id;
    NEW.auto_deduct_enabled := COALESCE((charge_rule->>'auto_deduct_enabled')::BOOLEAN,FALSE);
    NEW.audit_multiplier := COALESCE((charge_rule->>'multiplier')::NUMERIC,1);
    SELECT COALESCE((SELECT name FROM groups WHERE id=NEW.group_id),'') INTO NEW.group_name;
    SELECT role,deleted_at INTO target_role,target_deleted_at
        FROM users WHERE id=NEW.user_id FOR NO KEY UPDATE;
    target_exists := FOUND;
    NEW.count_reason := CASE
        WHEN NOT NEW.scope_matched THEN 'group_not_enabled'
        WHEN NEW.user_id=ANY(exempt_users) THEN 'user_exempt'
        WHEN NOT NEW.auto_disable_enabled THEN 'auto_disable_off'
        WHEN NOT target_exists OR target_deleted_at IS NOT NULL THEN 'user_unavailable'
        WHEN target_role='admin' THEN 'admin_exempt'
        ELSE '' END;
    NEW.count_status := CASE WHEN NEW.count_reason='' THEN 'counted' ELSE 'skipped' END;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION charge_cyber_policy_audit_event()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE actual_cost NUMERIC;
BEGIN
    IF NEW.policy_action='charge_allow' THEN
        SELECT l.actual_cost INTO actual_cost FROM usage_logs l
        WHERE l.id=NEW.usage_id AND l.created_at=NEW.usage_created_at AND l.user_id=NEW.user_id
            AND l.group_id IS NOT DISTINCT FROM NEW.group_id AND l.request_type<>4;
        IF NOT FOUND THEN
            RAISE WARNING 'cyber_policy_audit_usage_unavailable';
            RETURN NULL;
        END IF;
        -- Reuse exact NUMERIC rounding, the balance lock, shared usage claim,
        -- ledger and cache outboxes. Technical failure rolls back only penalty
        -- side effects and never retries or invalidates the ordinary bill.
        PERFORM apply_cyber_policy_auto_charge(NEW.usage_id,NEW.usage_created_at,NULL,
            NEW.user_id,NEW.group_id,actual_cost,NEW.auto_deduct_enabled,NEW.audit_multiplier,
            NEW.scope_matched,NEW.count_reason,NEW.settings_revision,NEW.evaluated_at);
    ELSE
        PERFORM apply_cyber_policy_auto_charge(NULL,NULL,NEW.id,NEW.user_id,NEW.group_id,0,
            NEW.auto_deduct_enabled,NEW.audit_multiplier,NEW.scope_matched,NEW.count_reason,
            NEW.settings_revision,NEW.evaluated_at);
    END IF;
    RETURN NULL;
EXCEPTION WHEN OTHERS THEN
    RAISE WARNING 'cyber_policy_audit_auto_charge_unavailable';
    RETURN NULL;
END;
$$;

COMMENT ON TABLE cyber_policy_audit_blocks IS
    'One server request/turn audit action: pre-forward block or completed charge_allow linked to real ordinary usage. Ranking and financial settings snapshot at event insertion; no historical backfill.';
