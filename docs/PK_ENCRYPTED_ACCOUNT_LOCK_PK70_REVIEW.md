# pk.70 encrypted account lock control

## Request and scope

The operator explicitly requested that automatic encrypted account locking be
disabled. The existing implementation has no independent off switch: known
opaque agent history forces its previously observed account, even when ordinary
sticky weighting is disabled. An unavailable owner can therefore reject a
request locally with HTTP 409 before attempting an upstream request.

`gateway.disable_encrypted_account_lock` is a reverse boolean with a default of
`false`. `GATEWAY_DISABLE_ENCRYPTED_ACCOUNT_LOCK=true` opts a process out of the
automatic encrypted owner binding and additional encrypted reasoning affinity.
Existing installations and zero-value configurations retain the original
behavior. This is an operator configuration, not a group billing option or the
BPS option that ignores encrypted history.

The disabled path keeps the original request and opaque history intact. It does
not read, create or update automatic owner observations, or require the previous
owner for account selection. Ordinary sticky routing, explicit response anchors,
account eligibility, limits, accounting and delivery failure handling retain
their existing semantics. Unknown opaque history and completed tool actions do
not gain permission for automatic replay. The WS lifecycle must retain its local
replay tracking independently of shared owner tracking.

Existing owner records are not deleted. Restoring the default resumes the old
behavior for records still within their normal lifetime. Upstream rejection,
provider-specific encrypted history compatibility and capacity errors remain
possible after the local lock is disabled.

## Delivery boundaries

This extends PK-006 and uses PK-021/047/058 without a new feature identifier.
No schema migration, account conversion, price change or historical data rewrite
is required. The HA adaptation receives a separate pk70 profile; old profiles,
release tags and sealed artifacts remain immutable.

A maintenance artifact may be deployed after its own source, hash, targeted
regressions and isolated runtime acceptance are verified. It must not be
described as the complete ordinary fourteen-asset formal release. Unchanged
frontend and authorization runtime artifacts may be reused only after their
source and artifact hashes are compared, with that reuse recorded explicitly.
The active deployment remains a single database writer and a single worker.
Process replacement uses the existing request gate and normal shutdown, with a
fresh recovery backup and a separate rollback record.

## Validation status

Implementation and validation are in progress. No passing new-artifact result or
production deployment is claimed by this source document yet. Final results must
identify actual tests, failures, executable hashes and effective process settings;
health checks alone cannot establish that all upstream 409 errors are resolved.
