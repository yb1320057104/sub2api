-- Preserve legacy NULL membership and group_id for old releases. No backfill.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS group_ids JSONB;
CREATE INDEX IF NOT EXISTS idx_api_keys_group_ids ON api_keys USING GIN (group_ids)
    WHERE deleted_at IS NULL;

CREATE OR REPLACE FUNCTION pk_api_key_ordered_groups_sync() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    candidate JSONB;
    candidate_id BIGINT;
    seen BIGINT[] := '{}';
BEGIN
    -- Older binaries only write group_id. Keep such writes meaningful after rollback.
    IF TG_OP = 'UPDATE' AND NEW.group_ids IS NOT DISTINCT FROM OLD.group_ids
       AND NEW.group_id IS DISTINCT FROM OLD.group_id THEN
        NEW.group_ids := CASE WHEN NEW.group_id IS NULL THEN '[]'::jsonb
                             ELSE jsonb_build_array(NEW.group_id) END;
    END IF;
    IF NEW.group_ids IS NULL THEN
        RETURN NEW;
    END IF;
    IF jsonb_typeof(NEW.group_ids) <> 'array' THEN
        RAISE EXCEPTION 'api_key_group_ids_invalid' USING ERRCODE = '23514';
    END IF;
    IF jsonb_array_length(NEW.group_ids) > 20 THEN
        RAISE EXCEPTION 'api_key_group_ids_invalid' USING ERRCODE = '23514';
    END IF;
    FOR candidate IN SELECT value FROM jsonb_array_elements(NEW.group_ids) LOOP
        IF jsonb_typeof(candidate) <> 'number' OR candidate::text !~ '^[1-9][0-9]*$' THEN
            RAISE EXCEPTION 'api_key_group_ids_invalid' USING ERRCODE = '23514';
        END IF;
        candidate_id := (candidate::text)::bigint;
        IF candidate_id = ANY(seen) THEN
            RAISE EXCEPTION 'api_key_group_ids_duplicate' USING ERRCODE = '23514';
        END IF;
        seen := array_append(seen, candidate_id);
    END LOOP;
    NEW.group_id := seen[1];
    RETURN NEW;
END;
$$;

CREATE TRIGGER pk_api_key_ordered_groups_sync
    BEFORE INSERT OR UPDATE OF group_id, group_ids ON api_keys
    FOR EACH ROW EXECUTE FUNCTION pk_api_key_ordered_groups_sync();
