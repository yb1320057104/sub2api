-- Keep upstream first_token_ms unchanged. Historical rows have no measured
-- downstream first response time and must remain NULL.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS first_response_ms INTEGER;
