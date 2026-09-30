# Sub2API PK Private Docker Image

Installation, updates, rollback, and disaster recovery use `ghcr.io/pengkang0322/sub2api` and [pengkang0322/sub2api Releases](https://github.com/pengkang0322/sub2api/releases). Follow [PK_MAINTENANCE.md](../PK_MAINTENANCE.md) for the production procedure.

## Private Registry and Release Access

```bash
# Run on the Docker host. Use a classic PAT with read:packages.
read -rsp 'GHCR read token: ' GHCR_READ_TOKEN; printf '\n'
printf '%s' "$GHCR_READ_TOKEN" | docker login ghcr.io -u pengkang0322 --password-stdin
unset GHCR_READ_TOKEN
```

Set `UPDATE_GITHUB_TOKEN` in the protected deployment environment file. This is a separate fine-grained token limited to `pengkang0322/sub2api`, with Contents: read. It authenticates both update checks and asset downloads inside the application. It does not authenticate the host Docker client.

## Docker Compose and Recovery

```yaml
services:
  sub2api:
    image: ghcr.io/pengkang0322/sub2api:${SUB2API_PK_VERSION:?Select an approved PK version}
    restart: unless-stopped
    environment:
      SUB2API_DOCKER_SELF_UPDATE: "true"
      UPDATE_GITHUB_TOKEN: ${UPDATE_GITHUB_TOKEN:?Set a private repository read token}
```

The snippet is an overlay for the existing application service, as supplied in `docker-compose.pk.yml`. Preserve its database, Redis, ports, environment, secrets, and `/app/data` volume. For a new deployment, the complete `docker-compose.yml`, `docker-compose.local.yml`, and `docker-compose.standalone.yml` templates also default to the private image.

For recovery, set `SUB2API_PK_VERSION` to the private release compatible with the database backup. Restore the matching data/configuration and original encryption secrets using the PK guide. Never substitute an upstream application image or regenerate production secrets. A program rollback does not undo database migrations.

## Persistent Web Updates

The image starts an application-owned executable at `/app/data/bin/sub2api`. The same data volume must remain attached after recreation. Admin-page updates replace this executable and restart the process through Docker's restart policy.

The image's original binary is root-owned and read-only at `/app/sub2api`. A SHA256 marker records which image seeded the data volume. The same seed preserves a web-installed version across restarts and container recreation. A different image seed installs that image's version, including when returning to an older image. Returning to the same seed cannot be distinguished from a normal restart; use the web version-rollback action for that case.

## Startup and Database Recovery

Sub2API runs database migrations while starting. PostgreSQL may still be
recovering briefly after a host or Docker daemon restart. The application
retries transient PostgreSQL startup and connection errors with bounded
exponential backoff, then continues startup when the database is ready.
Permanent errors such as invalid credentials, migration checksum mismatches,
SQL errors, and incompatible data fail immediately.

The Compose deployment also checks PostgreSQL readiness with both `pg_isready`
and a simple SQL query. `depends_on: condition: service_healthy` helps order a
fresh Compose start, but application-level retries are still required when
Docker restores existing containers after a host restart.

## Environment Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `SUB2API_PK_VERSION` | Private image tag; pin an approved version for recovery | Yes for PK overlay | `latest` in full templates |
| `UPDATE_GITHUB_TOKEN` | Private repository token with Contents: read | Yes | - |
| `SUB2API_DOCKER_SELF_UPDATE` | Persist web updates under `/app/data/bin` | Yes | `true` in PK images/templates |
| `DATABASE_*`, `REDIS_*` | Existing database and Redis settings | Yes | See `.env.example` |
| `SERVER_PORT` | Published application port | No | `8080` |

## Supported Architectures

- `linux/amd64`

Current PK releases do not publish `linux/arm64`. Apple container startup is disabled by default until a compatible image is explicitly published to the same private repository.

## Tags

- `latest` - Latest successfully published PK release
- `x.y.z-pk.N` - Specific private release, for example `0.2.10-pk.1`

## Links

- [Private repository](https://github.com/pengkang0322/sub2api)
- [PK maintenance guide](../PK_MAINTENANCE.md)
