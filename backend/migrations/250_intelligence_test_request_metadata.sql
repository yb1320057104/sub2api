-- PK-032: snapshot the model/effort used for the local Responses request.
-- NULL means a historical row has no snapshot. Empty effort means the request
-- deliberately omitted reasoning.effort, leaving the model's default in use.
ALTER TABLE pk_intelligence_test_results
    ADD COLUMN IF NOT EXISTS request_model TEXT
        CHECK (octet_length(request_model) <= 200),
    ADD COLUMN IF NOT EXISTS request_effort TEXT
        CHECK (octet_length(request_effort) <= 20);
