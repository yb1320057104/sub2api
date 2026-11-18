-- Login independence is separate from upstream health and the current BYOK slot.
ALTER TABLE pk_kilo_prepared_accounts
    ADD COLUMN identity_mode TEXT NOT NULL DEFAULT 'unknown'
        CONSTRAINT pk_kilo_prepared_accounts_identity_mode_check
        CHECK (identity_mode IN ('unknown','gpt_coupled','independent')),
    ADD COLUMN identity_source_account_id BIGINT REFERENCES accounts(id) ON DELETE RESTRICT,
    ADD COLUMN identity_checked_at TIMESTAMPTZ;

ALTER TABLE pk_kilo_prepared_accounts
    DROP CONSTRAINT pk_kilo_prepared_accounts_identity_status_check;
ALTER TABLE pk_kilo_prepared_accounts
    ADD CONSTRAINT pk_kilo_prepared_accounts_identity_status_check
        CHECK (identity_status IN ('active','unknown','banned','retired'));

-- A successful, non-reused source onboarding records the original login owner.
-- Legacy captured identity credentials plus the same source email are also
-- evidence. A different current subscription must never become the origin.
WITH origin_candidates AS (
    SELECT p.id AS prepared_id,j.source_account_id
    FROM pk_kilo_prepared_accounts p
    JOIN pk_kilo_jobs j ON j.prepared_id=p.id
        AND j.job_kind='onboard_source' AND j.status='succeeded'
        AND NOT j.reused_identity AND j.source_account_id IS NOT NULL
    JOIN accounts a ON a.id=j.source_account_id
    WHERE BTRIM(p.email)<>''
      AND LOWER(BTRIM(p.email))=LOWER(COALESCE(
          NULLIF(BTRIM(a.credentials->>'email'),''),
          NULLIF(BTRIM(a.credentials->>'chatgpt_email'),''),BTRIM(a.name)))
    UNION
    SELECT p.id AS prepared_id,p.source_account_id
    FROM pk_kilo_prepared_accounts p
    JOIN accounts a ON a.id=p.source_account_id
    WHERE p.identity_credential_encrypted<>'' AND BTRIM(p.email)<>''
      AND LOWER(BTRIM(p.email))=LOWER(COALESCE(
          NULLIF(BTRIM(a.credentials->>'email'),''),
          NULLIF(BTRIM(a.credentials->>'chatgpt_email'),''),BTRIM(a.name)))
), unique_origins AS (
    SELECT prepared_id,MIN(source_account_id) AS source_account_id
    FROM origin_candidates
    GROUP BY prepared_id
    HAVING COUNT(DISTINCT source_account_id)=1
)
UPDATE pk_kilo_prepared_accounts p
SET identity_mode='gpt_coupled',identity_source_account_id=o.source_account_id
FROM unique_origins o
WHERE p.id=o.prepared_id AND p.identity_source_account_id IS NULL;

-- Migration evidence does not stand in for a fresh official provider check.
-- Gateway projections commit with the same migration and contain no provider emails.
UPDATE accounts g
SET extra=(COALESCE(g.extra,'{}'::jsonb)-'kilo_identity_source_account_id')
    ||jsonb_build_object('kilo_identity_mode',p.identity_mode)
    ||CASE WHEN p.identity_source_account_id IS NULL THEN '{}'::jsonb
           ELSE jsonb_build_object('kilo_identity_source_account_id',p.identity_source_account_id) END
FROM pk_kilo_prepared_accounts p
WHERE p.account_id=g.id AND g.extra->>'kilo_prepared_id'=p.id::text;

COMMENT ON COLUMN pk_kilo_prepared_accounts.identity_mode IS
    'Login independence: unknown until established; GPT-coupled and independent are not upstream health states.';
COMMENT ON COLUMN pk_kilo_prepared_accounts.identity_source_account_id IS
    'Original same-source GPT login owner. Preserve across subscription replacement and later independent login linking.';
COMMENT ON COLUMN pk_kilo_prepared_accounts.identity_checked_at IS
    'Database time of a successful official profile and auth-provider verification; NULL for historical migration evidence.';
COMMENT ON COLUMN pk_kilo_prepared_accounts.identity_status IS
    'Kilo identity health or local lifecycle state. Retired is local non-reuse and does not assert an upstream ban.';
