# pk.70 optional encrypted account lock

This profile applies the exact pk.69 HA patch to the pk.70 candidate and records
new input/output hashes for the affected files. All 96 original overlay paths
remain covered. It preserves the single writer, single active worker, shared
billing, and graceful shutdown behavior.

The candidate adds `gateway.disable_encrypted_account_lock` (environment
`GATEWAY_DISABLE_ENCRYPTED_ACCOUNT_LOCK`), default false. Operators can disable
automatic encrypted owner binding and the extra encrypted reasoning affinity;
the original request and the existing replay protections remain intact.

This release has no SQL, frontend, or BPS runtime change. Reusing a prior frontend
or BPS archive requires exact source and artifact hash comparisons and explicit
version/hash binding. A source/profile match is not runtime acceptance.

The HA maintenance artifact must pass focused HTTP/WS configuration and replay
tests, exact-binary API/worker startup and graceful exit, and BPS archive
compatibility checks before deployment. It does not constitute the separate
ordinary-program or full public release validation.
