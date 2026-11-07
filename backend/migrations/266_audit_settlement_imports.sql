-- Opt-in relay settlement. Ordinary usage, token counters and published migrations stay intact.
CREATE TABLE audit_settlement_imports (
    id BIGSERIAL PRIMARY KEY,
    usage_id BIGINT NOT NULL,
    usage_created_at TIMESTAMPTZ NOT NULL,
    account_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    group_id BIGINT,
    upstream_identity TEXT NOT NULL CHECK (upstream_identity ~ '^[0-9a-f]{64}$'),
    receipt_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','deducted','zero_cost','insufficient_balance','unavailable','failed','skipped')),
    reason TEXT NOT NULL DEFAULT '',
    reported_amount NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK (reported_amount BETWEEN 0 AND 1000000),
    amount NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK (amount BETWEEN 0 AND 1000000),
    balance_before NUMERIC(20,8),
    balance_after NUMERIC(20,8),
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    charge_started_at TIMESTAMPTZ,
    claimed_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMPTZ,
    UNIQUE(usage_id,usage_created_at),
    UNIQUE(upstream_identity,receipt_id),
    CHECK ((status='deducted' AND amount>0 AND amount=reported_amount) OR (status<>'deducted' AND amount=0))
);
CREATE INDEX audit_settlement_imports_pending ON audit_settlement_imports(available_at,id) WHERE status='pending';
CREATE INDEX audit_settlement_imports_user ON audit_settlement_imports(user_id,created_at DESC);

-- The user lock and all financial effects share one transaction. A failed
-- ledger/cache write rolls back the debit, retains evidence and is terminal.
CREATE FUNCTION settle_audit_settlement_import(import_id BIGINT, expected_owner TEXT, incoming_amount NUMERIC)
RETURNS TEXT LANGUAGE plpgsql AS $$
DECLARE entry audit_settlement_imports%ROWTYPE; target_user BIGINT; before_balance NUMERIC; after_balance NUMERIC;
BEGIN
    IF incoming_amount IS NULL OR incoming_amount<0 OR incoming_amount>1000000 OR incoming_amount<>ROUND(incoming_amount,8) THEN
        RAISE EXCEPTION 'invalid audit settlement amount';
    END IF;
    SELECT user_id INTO target_user FROM audit_settlement_imports WHERE id=import_id;
    IF NOT FOUND THEN RETURN 'unavailable'; END IF;
    SELECT balance INTO before_balance FROM users WHERE id=target_user AND deleted_at IS NULL FOR NO KEY UPDATE;
    SELECT * INTO entry FROM audit_settlement_imports WHERE id=import_id FOR UPDATE;
    IF entry.status<>'pending' OR entry.claimed_by IS DISTINCT FROM expected_owner THEN RETURN entry.status; END IF;
    IF entry.charge_started_at IS NULL THEN RAISE EXCEPTION 'audit settlement not durably started'; END IF;
    UPDATE audit_settlement_imports SET reported_amount=incoming_amount,balance_before=before_balance,balance_after=before_balance,settled_at=NOW() WHERE id=import_id;
    IF before_balance IS NULL THEN
        UPDATE audit_settlement_imports SET status='skipped',reason='user_unavailable' WHERE id=import_id;
        RETURN 'skipped';
    ELSIF incoming_amount=0 THEN
        UPDATE audit_settlement_imports SET status='zero_cost' WHERE id=import_id;
        RETURN 'zero_cost';
    ELSIF before_balance<incoming_amount THEN
        UPDATE audit_settlement_imports SET status='insufficient_balance',reason='insufficient_balance' WHERE id=import_id;
        RETURN 'insufficient_balance';
    END IF;
    BEGIN
        UPDATE users SET balance=balance-incoming_amount,updated_at=NOW() WHERE id=target_user AND deleted_at IS NULL AND balance>=incoming_amount RETURNING balance INTO after_balance;
        IF NOT FOUND THEN RAISE EXCEPTION 'audit settlement balance changed'; END IF;
        INSERT INTO redeem_codes(code,type,value,status,used_by,used_at,notes)
        VALUES('AS-'||SUBSTR(md5(import_id::TEXT||':'||entry.receipt_id::TEXT),1,29),'admin_balance',-incoming_amount,'used',target_user,NOW(),
            '上游审计追加费用 / 用量#'||entry.usage_id::TEXT||' / 实际结算 USD '||incoming_amount::TEXT);
        INSERT INTO cyber_policy_auto_charge_outbox AS pending(user_id) VALUES(target_user)
        ON CONFLICT(user_id) DO UPDATE SET revision=pending.revision+1,available_at=NOW(),stage=0,claimed_at=NULL,claimed_by=NULL;
        INSERT INTO auth_cache_invalidation_outbox(cache_key)
        SELECT encode(sha256(convert_to(key,'UTF8')),'hex') FROM api_keys WHERE user_id=target_user AND deleted_at IS NULL AND key<>'';
        UPDATE audit_settlement_imports SET status='deducted',amount=incoming_amount,balance_after=after_balance,reason='' WHERE id=import_id;
    EXCEPTION WHEN OTHERS THEN
        UPDATE audit_settlement_imports SET status='failed',reason='charge_failed',amount=0,balance_after=before_balance WHERE id=import_id;
    END;
    RETURN (SELECT status FROM audit_settlement_imports WHERE id=import_id);
END;
$$;
