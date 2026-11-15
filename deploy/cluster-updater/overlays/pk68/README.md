# pk.68 HA balance-cache miss recovery

This profile carries the reviewed pk.67 HA adaptation forward. It changes three
profile outputs: the in-flight balance reader, the fixed-charge error handling,
and HA billing regression tests. Historical profiles and accepted artifacts
remain unchanged.

A durable debit invalidates the Redis balance after its database commit. A
different request can pass eligibility on a cache hit immediately before that
invalidation. In this interleaving there is no pending cache fill, so repeated
Redis reads cannot recover the current request.

On `redis.Nil` only, the HA reader performs one direct `userRepo.GetByID` call
with a maximum one-second timeout inherited from the caller context. It rejects
an expired request or read budget, a database failure, a missing or inactive
user, a mismatched user ID, and a non-finite balance. It rechecks positive balance
and the configured minimum reserve. An insufficient balance keeps the existing
business error, including through the fixed-charge wrapper.

The first Redis read and warm-cache behavior retain their original context and
cost. Other Redis errors still fail closed without a database fallback. The
database snapshot is never published to Redis or queued for publication, and
the fallback never invokes automatic-credit evaluation. Recovery emits the
fixed `billing.inflight_balance_cache_miss_recovered` event with `stage` and
`elapsed_ms` plus the existing request context. Database failures use bounded
diagnostic categories and do not log raw errors, balances, or user identifiers.

The shared reservation script still executes once per attempt. Ambiguous replies
are rejected and cleaned up under the same identity, without replay. Reservation
renewal, billing-task handoff, and release semantics are unchanged. Positive
balance still permits the first token request even if its estimate exceeds that
balance; fixed charges still require balance to cover their known amount.
The existing fixed-charge call chain can perform two independent balance checks;
both retain their fresh-balance validation and separate bounded miss reads.
The second check also requires the refreshed balance to cover the full fixed
amount before invoking the reservation script. If a concurrent debit changes
the balance from 10 to 5 after a first check for a charge of 8, the fixed charge
is rejected; the script's first-request allowance continues to apply to token
estimates only.

`UserRepository.GetByID` uses the configured Ent database client. The inspected
gateway and middleware paths do not attach an Ent transaction to the request.
Deployment must keep that database client routed to the current writer. This
profile does not implement read/write splitting or change database routing.

Tests reproduce eligibility on a cache hit followed by the real durable-billing
invalidation function, then verify recovery with the current database balance and
no cache publication. They also cover single-read bounds, cancellation and late
success, database and Redis failures, balance thresholds, automatic-credit call
counts, fixed charges, concurrent shared admission, and existing ambiguous-write
cleanup. A negative control substitutes the pk.67 reader and must fail the new
invalidation-interleaving test by returning the existing unavailable error.
A second negative control removes only the fixed-charge coverage guard and
must fail the 10-to-5 invalidation test, while the corrected code also proves
that a first token estimate of 8 remains admissible against positive balance 5.

This is a scoped cache-miss repair. It does not make PostgreSQL and Redis a single
transaction, eliminate the cross-store validation/reservation window, or change
the pre-existing asynchronous cache publication and stale-cache-hit behavior.
Local profile verification and tests are not production acceptance.

Use `prepare_candidate_overlay.py` with this profile. Its 96-path manifest pins
the candidate input and resulting bytes. If the candidate changes a listed input,
review and update this new profile before preparation; never alter historical
profiles or their acceptance evidence.
