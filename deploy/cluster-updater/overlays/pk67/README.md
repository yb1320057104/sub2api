# pk.67 HA balance-cache fill recovery

This profile carries the reviewed pk.66 HA adaptation forward. The previous
profile and its accepted artifacts are unchanged. Only the in-flight billing
balance reader and its existing HA regression tests differ from pk.66.

An ordinary cache miss can occur after billing eligibility returns a database
balance while the existing cache-write worker is still filling Redis. The HA
reader now permits two additional reads, separated by 20 milliseconds, within
a single additional 250-millisecond budget. The original first read retains
the caller's deadline. A cache hit has no new wait or extra read.

The retry applies only to `redis.Nil`. A persistent miss, cancellation, invalid
balance, or other cache error still rejects admission. The change does not
read the database, publish a balance, replay a reservation Lua script, or replay
an upstream request. The shared reservation and its cleanup remain unchanged.

The existing `ha_billing_concurrency_test.go` additionally covers an actual
eligibility/read-worker interleaving, finite retries, errors after a miss,
cancellation, a blocked retry, and two services competing for one shared
balance. Earlier failure and ambiguous-reservation coverage is retained.

Users configured for automatic credit have a separate database-authoritative
balance path which does not fill this cache. That incompatibility is outside
this repair; the production failure sample contained no such users.

Use the existing `prepare_candidate_overlay.py` workflow with this profile.
Its manifest pins candidate input and patched output bytes. If another change
modifies a listed candidate file, review and update this new profile before
preparing it; never update the historical profiles or accepted artifacts.
