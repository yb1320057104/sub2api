# Independent site storage

This role creates new storage; it never imports another site's database, Redis data,
credentials, account tokens or deployment receipts. Run `configure` on all nodes,
then `start` on all nodes, then `verify`. Do not use a serial one-node-at-a-time play
for initial etcd startup: a fresh three-member cluster requires a majority.

`single` runs PostgreSQL on loopback 6432 and Redis on loopback 6379.
`ha` runs etcd on all three mesh addresses (2379/2380), Sentinel on all three (26379),
PostgreSQL/Patroni on the two data nodes (5432/8008), Redis on the two data nodes (6379),
and a local HAProxy writer endpoint (6432) on each data node. Host networking is used;
all service listeners bind the generated WireGuard address, never 0.0.0.0. Internal
connections are plaintext inside WireGuard, so only trusted cluster members belong
on this mesh. Public firewall rules must not expose these storage ports.

The application database is `sub2api`, owned by `sub2api_app`. Its connection limit
is 90; PostgreSQL permits 100 total. The intended app pool maxima are 24 + 24 + 12
(two APIs and one background worker). Every site gets independent data directories,
passwords and etcd scope. Hosts must be dedicated to this installation's ports.

Images are explicit version tags: PostgreSQL 18.1, Redis 7.4.2, etcd 3.5.17 and
HAProxy 3.0.5. The HA image adds Patroni 4.1.5 and psycopg 3.2.3. These tags and
package versions make upgrades intentional; tags are not immutable image digests.
Record deployment digests and review upstream security fixes before long-term use.

PostgreSQL uses ordinary asynchronous streaming replication with one elected
writer and `pg_rewind`. Redis uses asynchronous replication, AOF every second,
Sentinel quorum 2 and independent leader election. Acknowledged writes can be lost
in failover; PostgreSQL and Redis do not share an atomic transaction or promotion.
Redis permits its sole remaining master to write (`min-replicas-to-write 0`), which
preserves one-node failure availability but does not provide partition-safe writes
on an isolated old master. Applications must use Sentinel, not a fixed Redis IP.

Redis and Sentinel runtime configuration directories are writable because native
failover persists role changes there. Configuration tasks use `force: false`; reruns
must not reset the elected leader. Existing credentials must match exactly; this
installer does not silently rotate database passwords. Startup uses `--no-recreate`
so repeating installation does not restart running storage or replace its data.
Intentional upgrades and credential rotation require their own controlled process.

`verify_storage.py` checks three etcd members agree, exactly one Patroni writer,
the physical streaming replica, three Sentinel members with quorum and identical
master address, Redis replication health, and real app authentication through local
6432. It reads only; it does not force a switchover or claim to test host failure.
An outage test on a disposable staging cluster is required before promising an
automatic recovery time. Dedicated backup and restore procedures remain necessary.

Variables: `mode`, `site_id`, `site_dir`, `node_role`, `mesh_ip`, `groups.cluster`,
`groups.data`, `hostvars.primary`, and `cluster_secrets` with `pg_superuser_password`,
`pg_replication_password`, `pg_app_password`, `redis_password`, and
`redis_sentinel_password`. Passwords must be independently generated hexadecimal
strings of at least 48 characters. Patroni member names equal `node_role` and scope
equals `site_id`, matching the application worker supervisor.
