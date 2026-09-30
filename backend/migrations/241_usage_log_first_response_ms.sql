-- Keep upstream first_token_ms unchanged. Historical rows have no measured
-- downstream first response time and must remain NULL.
-- Like request_type and native_compaction_v2, this column is maintained by the
-- explicit SQL usage repository rather than the generated Ent usage builders.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS first_response_ms INTEGER;
