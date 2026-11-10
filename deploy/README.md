# Sub2API Deployment Files

This private edition installs, updates, and restores from `yb1320057104/sub2api` only. Follow [PK_MAINTENANCE.md](../PK_MAINTENANCE.md) for the approved production procedure; preserve the existing database, Redis, ports, secrets, and data volumes. Current private releases support `linux/amd64`.

**新服务器安装：请按 [PK版中文部署指南](PK_DEPLOYMENT_CN.md) 操作。** It covers private access, environment configuration, startup checks, reverse proxy setup, web updates, and recovery.

网页注册额度、登录失败封禁及可信代理升级要求见 [网页认证 IP 防护](AUTH_IP_GUARD.md)。

## API Key 并发槽位 TTL 与升级切换

`gateway.api_key_slot_ttl_seconds` 只调整共享 Redis 实现的普通 API Key active slots。默认 `0` 继承 `gateway.concurrency_slot_ttl_minutes`；保持默认值升级不会缩短原租约。显式值至少为 30 秒，120 秒可作为运维评估起点；30 秒下限是配置策略，未经过负载测量，不构成安全 SLA。account/user 槽位、Live 自身租约、WS ingress 租约及 regional concurrency authority 继续沿用各自期限。

运行中的请求复用既有续租、watchdog 和精确释放流程。进程硬退出后，槽位在最后一次成功 Redis 更新时间加有效 TTL 后，由相关准入或统计访问清理，或随 Redis key 到期释放；重启不会立即清零。单独设置短 TTL 不能代替请求取消与退出确认。

**共享同一 Redis 槽位命名空间的进程必须使用相同的有效 API Key TTL。首次启用短 TTL、修改有效 TTL、回退配置或回滚到不识别该字段的旧二进制时，都必须统一 drain，禁止长短 TTL 实例混跑。** 准入和统计读取会按调用进程的 TTL 裁剪成员；短 TTL 进程可能把仍按长周期续租的活跃请求误判为到期，提前重新授予容量。

1. 保存当前二进制与配置，记录旧、新有效 TTL，以及共享该槽位命名空间的全部实例。配置为 `0` 时，也要计入继承的分钟值；regional authority 不应用这个独立 TTL。
2. 从负载均衡和其他入口撤下全部旧 TTL 实例，停止接收新请求，等待已有 HTTP/SSE、WebSocket turn 和相关 Live 会话正常结束。确认实际转发及槽位所有权已结束后再停止旧实例；达到 drain 超时或进程退出，本身不能证明上游请求已停止。
3. 确认旧 TTL 进程及其相关统计读取已全部停止，再以统一的新有效 TTL 启动实例并恢复流量。不得逐个滚动切换不同 TTL 的实例，也不得通过清 Redis、删除其他进程成员或启动清理扫描替代 drain。
4. 核对实际加载的配置、服务日志、并发统计及请求路径。回滚执行同一套 drain；旧二进制忽略专用字段后会恢复共享 TTL，不能直接与短 TTL 实例并行运行。

新增回归覆盖默认继承、独立 TTL、失效回收、双客户端活跃成员、队列和 Live handoff，以及 account/user 与 regional 隔离。实际负载、多 Pod drain 和生产切换仍需在各自环境验收；测试通过不代表这些操作已验证。

## Deployment Methods

| Method | Best For | Setup Wizard |
|--------|----------|--------------|
| **Docker Compose** | Quick setup, all-in-one | Not needed (auto-setup) |
| **Apple container** | Requires a separately published private `linux/arm64` image; currently unavailable | Not needed (auto-setup) |
| **Legacy binary installer** | Disabled for this private edition; use the PK maintenance procedure | — |

## Files

| File | Description |
|------|-------------|
| `PK_DEPLOYMENT_CN.md` | PK edition setup guide for a new Linux amd64 server (中文) |
| `docker-compose.yml` | Docker Compose configuration (named volumes) |
| `docker-compose.local.yml` | Docker Compose configuration (local directories, easy migration) |
| `docker-deploy.sh` | Legacy entry point; redirects private-edition operators to the PK maintenance guide |
| `apple-container.sh` | Native Apple `container` lifecycle script |
| `APPLE_CONTAINER.md` | Apple `container` deployment and operations guide |
| `.env.example` | Container environment variables template |
| `DOCKER.md` | Private GHCR deployment and recovery requirements |
| `install.sh` | Legacy entry point disabled for private-edition installation |
| `install-datamanagementd.sh` | datamanagementd 一键安装脚本 |
| `sub2api.service` | Systemd service unit file |
| `sub2api-datamanagementd.service` | datamanagementd systemd service unit file |
| `DATAMANAGEMENTD_CN.md` | datamanagementd 部署与联动说明（中文） |
| `config.example.yaml` | Example configuration file |
| `install-mihomo-codex.sh` | Optional Mihomo sidecar for rotating Codex ticket harvest exits |
| `mihomo-codex.service` | Systemd unit for the Mihomo ticket proxy |
| `mihomo-codex.config.example.yaml` | Sanitized Mihomo subscription configuration example |
| `EDGE_SECURITY.md` | Reverse proxy, CDN/WAF, trusted proxy, and ingress hardening guide |

---

## Apple container Deployment

The current PK workflow publishes only `linux/amd64`, so Apple-silicon startup is unavailable by default. The script requires an explicitly selected image from `ghcr.io/yb1320057104/sub2api` that has been verified to support `linux/arm64`; it never substitutes an upstream image. Existing stacks can still be inspected or stopped.

See [APPLE_CONTAINER.md](./APPLE_CONTAINER.md) for configuration, upgrades, persistence, networking behavior, and limitations.

---

## Docker Deployment (Recommended)

### Existing Production Deployment and Recovery

Use the private repository's [PK maintenance procedure](../PK_MAINTENANCE.md) and `docker-compose.pk.yml` overlay. It changes the application source while retaining the existing database, Redis, ports, environment, and data volumes. The legacy installation scripts are disabled because replacing production configuration could lose those settings.

Authenticate the host to GHCR with a classic PAT that has `read:packages`. Set `UPDATE_GITHUB_TOKEN` separately for the running application's private Release API and asset downloads. See [DOCKER.md](DOCKER.md) for the two credentials and persistence behavior.

### New Deployment

Follow [PK_DEPLOYMENT_CN.md](PK_DEPLOYMENT_CN.md) in order. It uses the complete `docker-compose.yml` with named volumes, a pinned private release, separate registry and update credentials, and an explicit administrator password. Edit existing environment keys instead of appending duplicate secret definitions. The procedure is for a new, empty deployment only.

Starting with the v0.2.14 bootstrap changes, an empty `ADMIN_EMAIL` generates a random login email and an empty `ADMIN_PASSWORD` generates a random password. Generated values appear in first-start logs (`Generated admin`). Explicit credentials require a valid login email and a password of 8–72 bytes; existing users and administrator credentials are not changed by this bootstrap validation.

### Deployment Version Comparison

| Version | Data Storage | Migration | Best For |
|---------|-------------|-----------|----------|
| **docker-compose.local.yml** | Local directories (./data, ./postgres_data, ./redis_data) | Requires consistent database backup and file backup | Operators who manage bind-mounted data |
| **docker-compose.yml** | Docker-managed named volumes | Back up database and volumes separately from source | Default used by the PK setup guide |

Use the same Compose file consistently for all commands. Keep an existing deployment's current volume layout when applying the PK overlay. Copying live database files is not a consistency-safe backup.

### Docker behind host Nginx: trusted client IPs

Registration quotas and authentication abuse protection always use the trusted
proxy chain. With host Nginx and a Docker published port, the application's TCP
peer may be a bridge gateway, which is not trusted by default. Configure only
the actual proxy IPs through `SERVER_TRUSTED_PROXIES` in `.env` or the persistent
`/app/data/config.yaml`. Leave the environment variable **unset**, not empty, to
keep YAML configuration. Updating `.env` alone has no effect with an older
Compose template that does not pass this variable through.

See [Docker + host Nginx trusted proxy configuration](../docs/docker-trusted-proxies.md)
for peer inspection, exact proxy configuration, application-only recreation,
registration IP diagnostics and preserving site settings across template updates.

### How Auto-Setup Works

When using Docker Compose with `AUTO_SETUP=true`:

1. On first run, the system automatically:
   - Connects to PostgreSQL and Redis
   - Applies database migrations (SQL files in `backend/migrations/*.sql`) and records them in `schema_migrations`
   - Generates JWT secret (if not provided)
   - Creates admin account (email and password auto-generated if not provided; a provided password must be 8-72 bytes)
   - Writes config.yaml

2. No manual Setup Wizard needed - just configure `.env` and start

3. If `ADMIN_EMAIL` / `ADMIN_PASSWORD` are not set, check logs for the generated admin email (login username) and password:
   ```bash
   docker compose logs sub2api | grep "Generated admin"
   ```

### Startup and Database Recovery

Sub2API applies database migrations during application startup. PostgreSQL can
remain in its recovery/startup phase briefly after a host or Docker daemon
restart. The application retries transient PostgreSQL startup and connection
errors with bounded exponential backoff, then starts automatically when the
database becomes ready. Authentication errors, migration checksum mismatches,
SQL errors, and other permanent configuration or data errors fail immediately.

The Compose example also uses a PostgreSQL health check that verifies both
server readiness and a simple SQL query. `depends_on: condition: service_healthy`
controls dependency ordering for a fresh Compose start, but it is not a
replacement for application-level retries when Docker restores existing
containers after a host restart.

For systemd deployments, keep `Restart=always` and `RestartSec` configured in
`sub2api.service`; the application retry covers transient database startup,
while systemd remains the supervisor for permanent process exits. For
Kubernetes, use a PostgreSQL readiness probe and retain the Sub2API startup
retry behavior; configure the application liveness probe separately so a
database recovery period is not treated as a permanent process failure.

### Database Migration Notes (PostgreSQL)

- Migrations are applied in lexicographic order (e.g. `001_...sql`, `002_...sql`).
- `schema_migrations` tracks applied migrations (filename + checksum).
- Migrations are forward-only; rollback requires a DB backup restore or a manual compensating SQL script.

**Verify `users.allowed_groups` → `user_allowed_groups` backfill**

During the incremental GORM→Ent migration, `users.allowed_groups` (legacy `BIGINT[]`) is being replaced by a normalized join table `user_allowed_groups(user_id, group_id)`.

Run this query to compare the legacy data vs the join table:

```sql
WITH old_pairs AS (
  SELECT DISTINCT u.id AS user_id, x.group_id
  FROM users u
  CROSS JOIN LATERAL unnest(u.allowed_groups) AS x(group_id)
  WHERE u.allowed_groups IS NOT NULL
)
SELECT
  (SELECT COUNT(*) FROM old_pairs)           AS old_pair_count,
  (SELECT COUNT(*) FROM user_allowed_groups) AS new_pair_count;
```

### datamanagementd（数据管理）联动

如需启用管理后台“数据管理”功能，请额外部署宿主机 `datamanagementd`：

- 主进程固定探测 `/tmp/sub2api-datamanagement.sock`
- Docker 场景下需把宿主机 Socket 挂载到容器内同路径
- 详细步骤见：`deploy/DATAMANAGEMENTD_CN.md`

### Commands

For **local directory version** (docker-compose.local.yml):

```bash
# Start services
docker compose -f docker-compose.local.yml up -d

# Stop services
docker compose -f docker-compose.local.yml down

# View logs
docker compose -f docker-compose.local.yml logs -f sub2api

# Restart Sub2API only
docker compose -f docker-compose.local.yml restart sub2api

# Update to latest version
docker compose -f docker-compose.local.yml pull
docker compose -f docker-compose.local.yml up -d

# Remove all data (caution!)
docker compose -f docker-compose.local.yml down
rm -rf data/ postgres_data/ redis_data/
```

For **named volumes version** (docker-compose.yml):

```bash
# Start services
docker compose up -d

# Stop services
docker compose down

# View logs
docker compose logs -f sub2api

# Restart Sub2API only
docker compose restart sub2api

# Update to latest version
docker compose pull
docker compose up -d

# Remove all data (caution!)
docker compose down -v
```

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `POSTGRES_PASSWORD` | **Yes** | - | PostgreSQL password |
| `JWT_SECRET` | **Recommended** | *(auto-generated)* | JWT secret (fixed for persistent sessions) |
| `TOTP_ENCRYPTION_KEY` | **Recommended** | *(auto-generated)* | TOTP encryption key (fixed for persistent 2FA) |
| `SERVER_PORT` | No | `8080` | Server port |
| `ADMIN_EMAIL` | No | *(auto-generated)* | Admin email (login username) |
| `ADMIN_PASSWORD` | No | *(auto-generated)* | Admin password (8-72 bytes) |
| `TZ` | No | `Asia/Shanghai` | Timezone |
| `UPDATE_GITHUB_TOKEN` | Yes | *(empty)* | Token with Contents: read on `yb1320057104/sub2api`, used for private release checks and asset downloads. |
| `SUB2API_PK_VERSION` | **Yes** | *(empty)* | Private GHCR tag; select the backup-compatible approved version explicitly. `latest` is not accepted by the PK compose files. |
| `GEMINI_OAUTH_CLIENT_ID` | No | *(builtin)* | Google OAuth client ID (Gemini OAuth). Leave empty to use the built-in Gemini CLI client. |
| `GEMINI_OAUTH_CLIENT_SECRET` | No | *(builtin)* | Google OAuth client secret (Gemini OAuth). Leave empty to use the built-in Gemini CLI client. |
| `GEMINI_OAUTH_SCOPES` | No | *(default)* | OAuth scopes (Gemini OAuth) |
| `GEMINI_QUOTA_POLICY` | No | *(empty)* | JSON overrides for Gemini local quota simulation (Code Assist only). |
| `GATEWAY_API_KEY_QUEUE_MAX_WAITING` | No | `5` | Extra waiting requests per API key with `concurrency_limit>0`; `0` disables key queueing and restores immediate `429`. Read at process start; recreate the container after changing. |
| `GATEWAY_API_KEY_QUEUE_TIMEOUT_SECONDS` | No | `30` | Per-request wait budget in seconds for key capacity; must be a positive integer. Read at process start; recreate the container after changing. |

See `.env.example` for all available options.

> For a new deployment, configure these secrets in the protected environment file. During migration or recovery, retain the original `JWT_SECRET`, `TOTP_ENCRYPTION_KEY`, and database credentials from the deployment backup.

### Easy Migration (Local Directory Version)

When using `docker-compose.local.yml`, all data is stored in local directories, making migration simple:

```bash
# On source server: Stop services and create archive
cd /path/to/deployment
docker compose -f docker-compose.local.yml down
cd ..
tar czf sub2api-complete.tar.gz deployment/

# Transfer to new server
scp sub2api-complete.tar.gz user@new-server:/path/to/destination/

# On new server: Extract and start
tar xzf sub2api-complete.tar.gz
cd deployment/
docker compose -f docker-compose.local.yml up -d
```

Your entire deployment (configuration + data) is migrated!

---

## Gemini OAuth Configuration

Sub2API supports three methods to connect to Gemini:

### Method 1: Code Assist OAuth (Recommended for GCP Users)

**No configuration needed** - always uses the built-in Gemini CLI OAuth client (public).

1. Leave `GEMINI_OAUTH_CLIENT_ID` and `GEMINI_OAUTH_CLIENT_SECRET` empty
2. In the Admin UI, create a Gemini OAuth account and select **"Code Assist"** type
3. Complete the OAuth flow in your browser

> Note: Even if you configure `GEMINI_OAUTH_CLIENT_ID` / `GEMINI_OAUTH_CLIENT_SECRET` for AI Studio OAuth,
> Code Assist OAuth will still use the built-in Gemini CLI client.

**Requirements:**
- Google account with access to Google Cloud Platform
- A GCP project (auto-detected or manually specified)

**How to get Project ID (if auto-detection fails):**
1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Click the project dropdown at the top of the page
3. Copy the Project ID (not the project name) from the list
4. Common formats: `my-project-123456` or `cloud-ai-companion-xxxxx`

### Method 2: AI Studio OAuth (For Regular Google Accounts)

Requires your own OAuth client credentials.

**Step 1: Create OAuth Client in Google Cloud Console**

1. Go to [Google Cloud Console - Credentials](https://console.cloud.google.com/apis/credentials)
2. Create a new project or select an existing one
3. **Enable the Generative Language API:**
   - Go to "APIs & Services" → "Library"
   - Search for "Generative Language API"
   - Click "Enable"
4. **Configure OAuth Consent Screen** (if not done):
   - Go to "APIs & Services" → "OAuth consent screen"
   - Choose "External" user type
   - Fill in app name, user support email, developer contact
   - Add scopes: `https://www.googleapis.com/auth/generative-language.retriever` (and optionally `https://www.googleapis.com/auth/cloud-platform`)
   - Add test users (your Google account email)
5. **Create OAuth 2.0 credentials:**
   - Go to "APIs & Services" → "Credentials"
   - Click "Create Credentials" → "OAuth client ID"
   - Application type: **Web application** (or **Desktop app**)
   - Name: e.g., "Sub2API Gemini"
   - Authorized redirect URIs: Add `http://localhost:1455/auth/callback`
6. Copy the **Client ID** and **Client Secret**
7. **⚠️ Publish to Production (IMPORTANT):**
   - Go to "APIs & Services" → "OAuth consent screen"
   - Click "PUBLISH APP" to move from Testing to Production
   - **Testing mode limitations:**
     - Only manually added test users can authenticate (max 100 users)
     - Refresh tokens expire after 7 days
     - Users must be re-added periodically
   - **Production mode:** Any Google user can authenticate, tokens don't expire
   - Note: For sensitive scopes, Google may require verification (demo video, privacy policy)

**Step 2: Configure Environment Variables**

```bash
GEMINI_OAUTH_CLIENT_ID=your-client-id.apps.googleusercontent.com
GEMINI_OAUTH_CLIENT_SECRET=GOCSPX-your-client-secret

# 可选：如需使用 Gemini CLI 内置 OAuth Client（Code Assist / Google One）
# 安全说明：本仓库不会内置该 client_secret，请在运行环境通过环境变量注入。
# GEMINI_CLI_OAUTH_CLIENT_SECRET=GOCSPX-your-built-in-secret
```

**Step 3: Create Account in Admin UI**

1. Create a Gemini OAuth account and select **"AI Studio"** type
2. Complete the OAuth flow
   - After consent, your browser will be redirected to `http://localhost:1455/auth/callback?code=...&state=...`
   - Copy the full callback URL (recommended) or just the `code` and paste it back into the Admin UI

### Method 3: API Key (Simplest)

1. Go to [Google AI Studio](https://aistudio.google.com/app/apikey)
2. Click "Create API key"
3. In Admin UI, create a Gemini **API Key** account
4. Paste your API key (starts with `AIza...`)

### Comparison Table

| Feature | Code Assist OAuth | AI Studio OAuth | API Key |
|---------|-------------------|-----------------|---------|
| Setup Complexity | Easy (no config) | Medium (OAuth client) | Easy |
| GCP Project Required | Yes | No | No |
| Custom OAuth Client | No (built-in) | Yes (required) | N/A |
| Rate Limits | GCP quota | Standard | Standard |
| Best For | GCP developers | Regular users needing OAuth | Quick testing |

---

## Binary Installation

The legacy binary installer is disabled for the PK private edition. Follow [PK_MAINTENANCE.md](../PK_MAINTENANCE.md) for installation, updates, and recovery; all program downloads come from the private repository.

Private release artifacts are available through authenticated access to [yb1320057104/sub2api Releases](https://github.com/yb1320057104/sub2api/releases). Existing systemd installations require an explicit migration plan preserving their paths and configuration; do not replace them with a legacy template.

### Service Management

```bash
# Start the service
sudo systemctl start sub2api

# Stop the service
sudo systemctl stop sub2api

# Restart the service
sudo systemctl restart sub2api

# Check status
sudo systemctl status sub2api

# View logs
sudo journalctl -u sub2api -f

# Enable auto-start on boot
sudo systemctl enable sub2api
```

### Codex 292 ticket proxy

The Codex ticket harvester can use a local Mihomo sidecar backed by an airport
subscription. This keeps subscription credentials outside the application
database and limits the proxy listener to localhost. Run the optional installer
as root on the same server as Sub2API:

```bash
sudo MIHOMO_CODEX_SUBSCRIPTION_URL='https://provider.example/subscription' \
  bash deploy/install-mihomo-codex.sh
```

For a fresh or versioned binary install, the same environment variable makes
`install.sh` configure the sidecar automatically after downloading the
release:

```bash
sudo MIHOMO_CODEX_SUBSCRIPTION_URL='https://provider.example/subscription' \
  bash deploy/install.sh install -v v2.7.2
```

Without the variable, the installer detects an already active sidecar and
leaves it untouched. It cannot provision an airport subscription on your
behalf; the subscription URL remains the only required provider input.

The installer downloads the pinned compatible Mihomo release, configures the
`airport` provider with health checks and a `CODEX-ROTATE` round-robin group,
and listens on `http://127.0.0.1:3101`. Set the admin setting **292 harvest
proxy** to that local URL. Mihomo rotates only the ticket-harvest traffic;
normal account requests continue to use their configured residential proxy.

The subscription URL is written only to `/etc/mihomo-codex/config.yaml`
(`0640`, `root:mihomo-codex`) and must not be committed or printed in logs.

### Configuration

#### Server Address and Port

During installation, you will be prompted to configure the server listen address and port. These settings are stored in the systemd service file as environment variables.

To change after installation:

1. Edit the systemd service:
   ```bash
   sudo systemctl edit sub2api
   ```

2. Add or modify:
   ```ini
   [Service]
   Environment=SERVER_HOST=0.0.0.0
   Environment=SERVER_PORT=3000
   ```

3. Reload and restart:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl restart sub2api
   ```

#### Gemini OAuth Configuration

If you need to use AI Studio OAuth for Gemini accounts, add the OAuth client credentials to the systemd service file:

1. Edit the service file:
   ```bash
   sudo nano /etc/systemd/system/sub2api.service
   ```

2. Add your OAuth credentials in the `[Service]` section (after the existing `Environment=` lines):
   ```ini
   Environment=GEMINI_OAUTH_CLIENT_ID=your-client-id.apps.googleusercontent.com
   Environment=GEMINI_OAUTH_CLIENT_SECRET=GOCSPX-your-client-secret
   ```

   如需使用“内置 Gemini CLI OAuth Client”（Code Assist / Google One），还需要注入：
   ```ini
   Environment=GEMINI_CLI_OAUTH_CLIENT_SECRET=GOCSPX-your-built-in-secret
   ```

3. Reload and restart:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl restart sub2api
   ```

> **Note:** Code Assist OAuth does not require any configuration - it uses the built-in Gemini CLI client.
> See the [Gemini OAuth Configuration](#gemini-oauth-configuration) section above for detailed setup instructions.

#### Application Configuration

The main config file is at `/etc/sub2api/config.yaml` (created by Setup Wizard).

Login rate limiting can be toggled in **System Settings → Security → Login rate limiting** (enabled by default). Saving applies immediately to admins and users, including password, two-factor, Passkey, and OAuth account-binding sign-ins. Disabling skips both the request counter and the IP failure guard, including existing bans and Redis/Lua failures on these login paths. Other authentication endpoints retain their protection. Existing counters and bans keep their original expiry and apply again if re-enabled before expiry. The setting is stored as `login_rate_limit_enabled` in the database; no `.env` change or restart is required. An older settings client that omits this field preserves the current value.

The independent **System Settings → Security → Registration account limit per IP** switch defaults to enabled (at most two registered accounts per IP, including email and OAuth signups). Administrators can disable it for shared/NAT networks without disabling CAPTCHA, email verification, request throttling or login abuse protection. Saving `registration_ip_limit_enabled` takes effect on the next auth request without a restart. Missing/invalid values and lookup failures retain the quota; older settings clients that omit the field preserve its current value. This uses the existing settings store and requires no schema migration. Existing IP quota rows are never deleted or reset. Signups while disabled do not increment the quota, and re-enabling resumes the previously retained counts rather than backfilling those signups. Disabling the quota can increase signup abuse, so retain other verification controls. Reverting to a binary without this switch restores the original always-on quota.

### Prerequisites

- Linux server (Ubuntu 20.04+, Debian 11+, CentOS 8+, etc.)
- PostgreSQL 14+
- Redis 6.0+
- systemd

### Directory Structure

```
/opt/sub2api/
├── sub2api              # Main binary
├── sub2api.backup       # Backup (after upgrade)
└── data/                # Runtime data

/etc/sub2api/
└── config.yaml          # Configuration file
```

---

## Troubleshooting

### Docker

For **local directory version**:

```bash
# Check container status
docker compose -f docker-compose.local.yml ps

# View detailed logs
docker compose -f docker-compose.local.yml logs --tail=100 sub2api

# Check database connection
docker compose -f docker-compose.local.yml exec postgres pg_isready

# Check Redis connection
docker compose -f docker-compose.local.yml exec redis redis-cli ping

# Restart all services
docker compose -f docker-compose.local.yml restart

# Check data directories
ls -la data/ postgres_data/ redis_data/
```

For **named volumes version**:

```bash
# Check container status
docker compose ps

# View detailed logs
docker compose logs --tail=100 sub2api

# Check database connection
docker compose exec postgres pg_isready

# Check Redis connection
docker compose exec redis redis-cli ping

# Restart all services
docker compose restart
```

### Binary Install

```bash
# Check service status
sudo systemctl status sub2api

# View recent logs
sudo journalctl -u sub2api -n 50

# Check config file
sudo cat /etc/sub2api/config.yaml

# Check PostgreSQL
sudo systemctl status postgresql

# Check Redis
sudo systemctl status redis
```

### Common Issues

1. **Port already in use**: Change `SERVER_PORT` in `.env` or systemd config
2. **Database connection failed**: Check PostgreSQL is running and credentials are correct
3. **Redis connection failed**: Check Redis is running and password is correct
4. **Permission denied**: Ensure proper file ownership for binary install

---

## TLS Fingerprint Configuration

Sub2API supports TLS fingerprint simulation to make requests appear as if they come from the official Claude CLI (Node.js client).

> **💡 Tip:** Visit **[tls.sub2api.org](https://tls.sub2api.org/)** to get TLS fingerprint information for different devices and browsers.

### Default Behavior

- Built-in `claude_cli_v2` profile simulates Node.js 20.x + OpenSSL 3.x
- JA3 Hash: `1a28e69016765d92e3b381168d68922c`
- JA4: `t13d5911h1_a33745022dd6_1f22a2ca17c4`
- Profile selection: `accountID % profileCount`

### Configuration

```yaml
gateway:
  tls_fingerprint:
    enabled: true  # Global switch
    profiles:
      # Simple profile (uses default cipher suites)
      profile_1:
        name: "Profile 1"

      # Profile with custom cipher suites (use compact array format)
      profile_2:
        name: "Profile 2"
        cipher_suites: [4866, 4867, 4865, 49199, 49195, 49200, 49196]
        curves: [29, 23, 24]
        point_formats: 0

      # Another custom profile
      profile_3:
        name: "Profile 3"
        cipher_suites: [4865, 4866, 4867, 49199, 49200]
        curves: [29, 23, 24, 25]
```

### Profile Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Display name (required) |
| `cipher_suites` | []uint16 | Cipher suites in decimal. Empty = default |
| `curves` | []uint16 | Elliptic curves in decimal. Empty = default |
| `point_formats` | []uint8 | EC point formats. Empty = default |

### Common Values Reference

**Cipher Suites (TLS 1.3):** `4865` (AES_128_GCM), `4866` (AES_256_GCM), `4867` (CHACHA20)

**Cipher Suites (TLS 1.2):** `49195`, `49196`, `49199`, `49200` (ECDHE variants)

**Curves:** `29` (X25519), `23` (P-256), `24` (P-384), `25` (P-521)
