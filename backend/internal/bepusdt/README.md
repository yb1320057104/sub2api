# Embedded BEpusdt lifecycle

Single-process deployments retain the local SQLite engine. HA deployments set
`SUB2API_BEPUSDT_SHARED_POSTGRES=true` on both API and worker containers and call
`ConfigureSharedPostgres` before constructing payment providers. The shared
database contains only the engine's `bep_*` tables; upstream administrator and
installation routes are never created. External payment providers are unchanged.

The explicit HA role pair is authoritative: `SUB2API_WORKER_ONLY=true` with
`SUB2API_BACKGROUND_MODE=active` admits the worker; `false` with `disabled`
admits an API. Missing halves or conflicting values fail closed. Without either
HA flag, the configured ordinary runtime role applies. Only the background
activation gate calls `Resume`; API `Ensure`, settings, wallet and order calls
never start scanners. A fresh worker creates schema/defaults and remains idle
until an embedded provider requests its token.

The worker owns a PostgreSQL session advisory lock on its only database
connection. Its connector admits one connection per term and refuses automatic
reconnection. After a connection failure the supervisor cancels and joins all
scanner callbacks, payment notifications and merchant notifications before
arming another connection. All worker ORM access uses that same fenced pool.
`Stop` joins these tasks before releasing the lock or closing the database.
Application cleanup must call it before closing shared infrastructure.

API processes use a separate bounded pool. Order amount allocation and wallet
changes acquire the same transaction advisory lock, and all allocation queries
and inserts stay in that transaction. A wallet with waiting or confirming
payments cannot be disabled, reassigned or deleted. Configuration snapshots
refresh atomically; the worker refreshes them every two seconds. Worker status
and network statistics are persisted with a fifteen-second freshness bound;
API processes do not report their own empty scanner state as healthy.

The signing token is inserted once and preserved across restarts and leadership
changes. The schema marker is committed last and checked before any subsequent
initialization. An existing SQLite database or WAL prevents enabling shared
mode: stop/drain the old engine, back up all SQLite/WAL files, and perform an
explicit audited offline import preserving token, IDs, orders, wallet matching
addresses and notification states before removing the legacy files from the
active data directory. No automatic import, deletion or NFS WAL sharing occurs.

`go test -tags unit ./internal/bepusdt` covers the SQLite API and lock connector.
`TestSharedPostgresMultiProcess` additionally runs only when both
`BEPUSDT_DISPOSABLE_DATABASE=1` and `BEPUSDT_ISOLATED_TEST_DSN` are supplied. It
requires an empty disposable PostgreSQL database and replaces chain callbacks;
it does not make real chain or payment requests. It verifies independent API
processes, collision-free allocation, wallet protection, configuration refresh,
session termination, stable signing identity and orderly shutdown. Never point
this test at a production database.
