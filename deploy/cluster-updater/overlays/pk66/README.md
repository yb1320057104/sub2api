# pk.66 HA credential worker adaptation

This profile carries the 96 reviewed pk.65 HA paths forward without changing
the prior profile or accepted artifacts. It adds a loopback-only callback
listener to the active worker. Only the existing OpenAI re-authentication
protocol's exact POST routes are reachable; the original worker-token and task
ownership checks still run. User and administrator APIs remain unavailable on
the worker listener.

The worker stops the credential guard before starting HTTP drain. The guard
cancels new scheduling, asks the managed Python pool to stop claiming tasks,
and waits for in-flight logins to finish while their authenticated callbacks
and database are still available. The ordinary HA HTTP and ordered background
cleanup then run unchanged. A timeout remains diagnostic, never permission to
kill a login or close storage beneath a callback.

`Application.CredentialGuard` is wired to the same guard used by the background
services. The candidate supplies the callback allowlist helper, managed runtime
package verification, graceful Python termination, and shared status reporting;
those source files are not copied into the overlay. The profile also preserves
the new status publisher's service wiring.

The existing `wire_gen_test.go` covers exact route admission, genuine worker
authentication, loopback address validation, and socket shutdown alongside the
previous HA drain and cleanup tests. Full runtime acceptance additionally needs
the verified Python package, isolated PostgreSQL/Redis, and an Excel task that
completes through the private listener. A healthy Go worker alone is not proof
of successful account authorization.
