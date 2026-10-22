-- PK-032: keep only the sent candy prompt and terminal assistant answer.
-- Empty defaults explicitly represent older rows without recorded details.
ALTER TABLE pk_intelligence_test_results
    ADD COLUMN prompt_snapshot TEXT NOT NULL DEFAULT ''
        CHECK (octet_length(prompt_snapshot) <= 100000),
    ADD COLUMN answer_text TEXT NOT NULL DEFAULT ''
        CHECK (octet_length(answer_text) <= 262144),
    ADD COLUMN error_code VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN error_detail TEXT NOT NULL DEFAULT ''
        CHECK (octet_length(error_detail) <= 2048),
    ADD CONSTRAINT pk_intelligence_details_candy_only
        CHECK (mode = 'candy' OR (prompt_snapshot = '' AND answer_text = '')),
    ADD CONSTRAINT pk_intelligence_answer_terminal_only
        CHECK (status = 'completed' OR answer_text = '');
