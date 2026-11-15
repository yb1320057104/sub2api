ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS free_openai_ultrafast BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN groups.free_openai_ultrafast IS
    'Bill eligible OpenAI/Composite group Ultrafast requests at Standard user prices';
