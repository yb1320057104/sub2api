# Application role contract

The root playbook calls `application_phase: configure`, then `start_api` on the
primary and secondary in that order, and finally `start_worker` and `verify`.
Configuration alone starts no long-lived application services. `start_api` uses
`--no-recreate`; this fresh-site installer is not a rolling upgrade command.

Required variables: `site_id`, `site_dir` (`/opt/sub2api-sites/<site_id>`), `mode`,
`node_role`, `mesh_ip`, `domain`, `acme_email`, `admin_email`, `app_image`, `wg_name`
(HA only), `groups.data`, `groups.cluster` and `hostvars` for each node's mesh IP
and domain. Data nodes must be named `primary` and `secondary`.

`cluster_secrets` must provide `pg_app_password`, `redis_password`,
`redis_sentinel_password`, `admin_password`, `jwt_secret` and `totp_key`.
Passwords and key material should be random hex strings. The common environment
is mode 0600 and does not become part of the release or Git history.

The pre-staged application image must contain the cluster r4 changes. It is never
implicitly pulled or updated by this role. The original Docker entrypoint stays
enabled to restore external Chromium/Xvfb runtime dependencies and drop to the
application UID; web self-updates are disabled. Browser bootstrap may add up to
240 seconds to a fresh container's first start.

PostgreSQL is reached through the local mesh address on port 6432, database
`sub2api`, role `sub2api_app`. HA uses the three mesh Sentinel addresses, master
name `site_id` and the separate Sentinel password. A single host uses local Redis
6379. Redis and JWT/TOTP credentials are identical within one site and unique
between sites.

API processes bind only their private mesh IP on 18080. Caddy listens on 80/443,
obtains a public ACME certificate for this node's domain and prefers its local
API. In HA it fails over to the other private API after readiness checks fail.
No failed application request is retried at another node. The other hostname is
another independent public entry; DNS for a dead host is not moved automatically.

The supervisor owns one pre-created worker container by full Docker ID and image
ID. It starts after two writer observations and sends TERM immediately on lost
writer readiness. It waits for orderly worker shutdown and never SIGKILLs it.
HA checks the local Patroni scope/name/primary response. Single-host mode checks
the API's actual PostgreSQL-writer and Redis-master readiness contract.

Patroni owns database leadership and the application retains task leases. Worker
placement is not a strict distributed fencing system: a partition/frozen process
can leave an old in-flight external operation running during failover. The kit
does not claim exactly-once model calls, emails or other external side effects.

`verify_public_https: false` may skip the final public TLS probe for an isolated
test. Normal deployments leave it enabled; point each domain's DNS at its own
node and allow inbound 80/443 before deploying.
