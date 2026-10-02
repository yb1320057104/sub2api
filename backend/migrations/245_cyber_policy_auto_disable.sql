-- Counter lifetime is independent of the ranking date range and usage retention.
-- Existing usage is deliberately not backfilled: enabling the rule starts with
-- newly persisted safety-policy records, and toggling it off preserves counters.
CREATE TABLE IF NOT EXISTS cyber_policy_user_counters (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    trigger_count BIGINT NOT NULL DEFAULT 0 CHECK (trigger_count >= 0),
    last_triggered_at TIMESTAMPTZ,
    reset_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    auto_disabled_at TIMESTAMPTZ
);

CREATE OR REPLACE FUNCTION apply_cyber_policy_auto_disable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    rule_enabled BOOLEAN;
    rule_threshold BIGINT;
    hit RECORD;
    target_role TEXT;
    target_status TEXT;
    accumulated BIGINT;
BEGIN
    SELECT auto_disable_enabled, auto_disable_threshold
    INTO rule_enabled, rule_threshold
    FROM cyber_policy_settings
    WHERE id = 1;

    IF rule_enabled IS DISTINCT FROM TRUE THEN
        RETURN NULL;
    END IF;

    -- Only actual inserts enter the transition table. ON CONFLICT DO NOTHING,
    -- single writes, ordinary batches and best-effort batches share this rule.
    -- Stable user order prevents competing batches from reversing lock order.
    FOR hit IN
        SELECT user_id, COUNT(*) AS hit_count, MAX(created_at) AS last_at
        FROM inserted_cyber_policy_usage
        WHERE request_type = 4
        GROUP BY user_id
        ORDER BY user_id
    LOOP
        -- Usage FK checks hold KEY SHARE locks. NO KEY UPDATE is compatible
        -- with those locks, while still serializing disable/reset/debit actions.
        SELECT role, status
        INTO target_role, target_status
        FROM users
        WHERE id = hit.user_id AND deleted_at IS NULL
        FOR NO KEY UPDATE;

        IF NOT FOUND OR target_role = 'admin' THEN
            CONTINUE;
        END IF;

        INSERT INTO cyber_policy_user_counters AS counter
            (user_id, trigger_count, last_triggered_at)
        VALUES (hit.user_id, hit.hit_count, hit.last_at)
        ON CONFLICT (user_id) DO UPDATE
        SET trigger_count = counter.trigger_count + EXCLUDED.trigger_count,
            last_triggered_at = GREATEST(counter.last_triggered_at, EXCLUDED.last_triggered_at)
        RETURNING trigger_count INTO accumulated;

        IF accumulated >= rule_threshold AND target_status = 'active' THEN
            UPDATE users
            SET status = 'disabled', updated_at = NOW()
            WHERE id = hit.user_id;
            UPDATE cyber_policy_user_counters
            SET auto_disabled_at = NOW()
            WHERE user_id = hit.user_id;
            -- The existing users status trigger from migration 184 durably
            -- queues hashed API-key invalidations in this same transaction.
        END IF;
    END LOOP;

    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS usage_logs_cyber_policy_auto_disable ON usage_logs;
-- All application insert paths address usage_logs, including when PostgreSQL
-- routes rows into monthly partitions. External importers must also write the
-- parent table: direct leaf-partition INSERTs bypass parent statement triggers.
CREATE TRIGGER usage_logs_cyber_policy_auto_disable
AFTER INSERT ON usage_logs
REFERENCING NEW TABLE AS inserted_cyber_policy_usage
FOR EACH STATEMENT
EXECUTE FUNCTION apply_cyber_policy_auto_disable();

CREATE OR REPLACE FUNCTION reset_cyber_policy_counter_on_user_enable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM cyber_policy_settings
        WHERE id = 1 AND reset_on_manual_enable
    ) THEN
        UPDATE cyber_policy_user_counters
        SET trigger_count = 0, reset_at = NOW(), auto_disabled_at = NULL
        WHERE user_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS users_cyber_policy_reset_on_enable ON users;
CREATE TRIGGER users_cyber_policy_reset_on_enable
AFTER UPDATE OF status ON users
FOR EACH ROW
WHEN (OLD.status = 'disabled' AND NEW.status = 'active')
EXECUTE FUNCTION reset_cyber_policy_counter_on_user_enable();

COMMENT ON TABLE cyber_policy_user_counters IS
    'New safety-policy hits while auto-disable is enabled; retained independently of usage cleanup and ranking filters, optionally reset when a disabled user is re-enabled.';
