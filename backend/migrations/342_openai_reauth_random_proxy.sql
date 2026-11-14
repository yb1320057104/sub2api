-- Resolve a random static login proxy for each attempt without binding it to
-- the account or changing the account's normal inference egress.
ALTER TABLE openai_oauth_reauth_configs
    DROP CONSTRAINT IF EXISTS openai_oauth_reauth_configs_proxy_source_check;
ALTER TABLE openai_oauth_reauth_configs
    ADD CONSTRAINT openai_oauth_reauth_configs_proxy_source_check CHECK (
        (proxy_source = 'managed_proxy' AND proxy_id IS NOT NULL)
        OR (proxy_source IN ('account', 'mihomo', 'random_managed') AND proxy_id IS NULL)
    );
