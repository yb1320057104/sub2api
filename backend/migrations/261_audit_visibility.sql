-- Display-only findings are independent of penalty identities and counters.
-- No historical backfill and no user/balance/usage mutation. Original logs may
-- be retained for a shorter period without removing this evidence.
CREATE TABLE cyber_policy_audit_observations (
    id BIGSERIAL PRIMARY KEY,
    source TEXT NOT NULL CHECK (source IN ('content_observation','prompt_observation')),
    source_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    group_id BIGINT,
    group_name TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    risk_score NUMERIC,
    risk_level TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source,source_id)
);
CREATE INDEX cyber_audit_observations_user_time ON cyber_policy_audit_observations(user_id,created_at DESC);
CREATE INDEX cyber_audit_observations_time ON cyber_policy_audit_observations(created_at DESC);

CREATE FUNCTION observe_content_audit_finding() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.user_id IS NULL OR NEW.user_id <= 0 OR NEW.action IN ('error','cyber_policy') THEN RETURN NULL; END IF;
    -- Enforced decisions already have the server-owned ranking/charge path.
    IF NEW.policy_action IN ('block','charge_allow') OR
       (NEW.policy_action IS NULL AND NEW.action IN ('block','hash_block','keyword_block')) THEN RETURN NULL; END IF;
    IF NOT (CASE WHEN NEW.policy_action IS NOT NULL THEN COALESCE(NEW.risk_score>0,FALSE) ELSE NEW.flagged END) THEN RETURN NULL; END IF;
    INSERT INTO cyber_policy_audit_observations(source,source_id,user_id,group_id,group_name,request_id,model,risk_score,risk_level,created_at)
    VALUES('content_observation',NEW.id,NEW.user_id,NEW.group_id,NEW.group_name,NEW.request_id,NEW.model,NEW.risk_score,NEW.risk_level,NEW.created_at)
    ON CONFLICT DO NOTHING;
    RETURN NULL;
EXCEPTION WHEN OTHERS THEN
    RAISE WARNING 'content_audit_observation_unavailable';
    RETURN NULL;
END;
$$;
CREATE TRIGGER cyber_content_audit_observation AFTER INSERT ON content_moderation_logs
FOR EACH ROW EXECUTE FUNCTION observe_content_audit_finding();

CREATE FUNCTION observe_prompt_audit_finding() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE execution TEXT;
BEGIN
    IF NEW.user_id IS NULL OR NEW.user_id<=0 OR NEW.decision NOT IN ('flag','critical') THEN RETURN NULL; END IF;
    SELECT execution_mode INTO execution FROM prompt_audit_jobs WHERE id=NEW.job_id;
    IF execution='blocking' AND (NEW.policy_action IN ('block','charge_allow') OR
        (NEW.policy_action IS NULL AND NEW.action='Block')) THEN RETURN NULL; END IF;
    INSERT INTO cyber_policy_audit_observations(source,source_id,user_id,group_id,group_name,request_id,model,risk_score,risk_level,created_at)
    VALUES('prompt_observation',NEW.id,NEW.user_id,NEW.group_id,NEW.group_name,NEW.request_id,NEW.model,NEW.risk_score,NEW.risk_level,NEW.created_at)
    ON CONFLICT DO NOTHING;
    RETURN NULL;
EXCEPTION WHEN OTHERS THEN
    RAISE WARNING 'prompt_audit_observation_unavailable';
    RETURN NULL;
END;
$$;
CREATE TRIGGER cyber_prompt_audit_observation AFTER INSERT ON prompt_audit_events
FOR EACH ROW EXECUTE FUNCTION observe_prompt_audit_finding();
