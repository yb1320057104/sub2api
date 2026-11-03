# PK出版部署指南

pk.49最终范围同时包含审计风险分级和兼容迁移260，前端需要本次新构建。只发布GitHub供用户切换；回退保留新增证据及已执行账务，不撤销扣款或重跑历史。Xvfb专项已完成，最终制品状态以PK_AUDIT_RISK_PK49_REVIEW.md为准。

本指南用于获得授权的部署者，在**全新 Linux x86_64（amd64）服务器**上安装 PK出版。源码、镜像、网页更新和恢复版本均来自 [yb1320057104/sub2api 私库](https://github.com/yb1320057104/sub2api)。程序没有绑定固定服务器 IP 或域名。

部署前先查看[私有 Releases](https://github.com/yb1320057104/sub2api/releases)的说明，选择新私库中已正式发布、完成验收的明确版本；当前发行物仅支持 `linux/amd64`。迁入 Git 历史或标签不代表对应 Release 资产和 GHCR 镜像已发布。

**已有用户和数据的服务**，请使用 [PK维护、迁移与恢复流程](../PK_MAINTENANCE.md)，保留原数据库、Redis、密钥、端口和数据卷。下面的初始化步骤只用于新环境。

## 1. 准备环境与访问权限

服务器需已安装 Docker Engine、Docker Compose v2、Git、GitHub CLI、OpenSSL 和 curl，并能连接 GitHub、GHCR 及计划使用的上游 API。

```bash
uname -m
docker version
docker compose version
```

`uname -m` 应为 `x86_64`。本指南的 Bash 命令在新服务器上执行，使用有 Docker 权限的同一用户完成登录、拉取和启动。

部署者需要仓库所有者授权访问私库与私有镜像。两项下载权限分别配置：

| 用途 | 配置位置 | 权限 |
|---|---|---|
| 获取部署文件、恢复源码 | GitHub CLI / Git 登录 | `yb1320057104/sub2api` 仓库读取权限 |
| 拉取 Docker 镜像 | 宿主机 `docker login ghcr.io` | token 所属账号能读取该私有包；classic PAT 含 `read:packages` |
| 网页检查版本、下载升级包和回退包 | 应用环境 `UPDATE_GITHUB_TOKEN` | 由仓库所有者为该部署单独配置 fine-grained token，仅允许该私库，Contents 只读，Metadata 只读 |

网页更新凭据不会替宿主机登录 Docker。每个部署使用单独的受限凭据、数据库和上游 API Key；源码包不包含现有站点的数据或密码。外部协作者的 fine-grained token 存在访问限制，不能假定受邀后就能以自己的账号选择他人的个人私库；由所有者单独配置该部署的更新凭据，避免复用现有生产 token。权限规则见 [GitHub token 文档](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens) 和 [GHCR 认证文档](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)。

## 2. 获取私库与登录镜像仓库

在尚不存在 `sub2api` 子目录的位置执行：

```bash
gh auth login --hostname github.com --git-protocol https --web
gh repo clone yb1320057104/sub2api
cd sub2api/deploy

read -rp 'GitHub 用户名（镜像 token 所属账号）: ' GHCR_USER
read -rsp 'GHCR 只读 token: ' GHCR_READ_TOKEN
printf '\n'
printf '%s' "$GHCR_READ_TOKEN" | docker login ghcr.io -u "$GHCR_USER" --password-stdin
unset GHCR_READ_TOKEN GHCR_USER
```

如果私库或私有包返回 404/denied，先核对账号授权；私有资源未授权时也可能返回 404。仓库所有者与登录用户可以不同，镜像地址始终为 `ghcr.io/yb1320057104/sub2api`。

## 3. 配置新环境

本指南统一使用 [docker-compose.yml](docker-compose.yml)，包含应用、PostgreSQL、Redis 和三个持久化命名卷。首次部署前确认同机没有同名 `sub2api`、`sub2api-postgres`、`sub2api-redis` 容器。

```bash
umask 077
cp .env.example .env
chmod 600 .env
nano .env
```

修改 `.env` 中**已有的同名配置行**，不要重复追加：

| 配置 | 设置 |
|---|---|
| `SUB2API_PK_VERSION` | 新私库已验收的正式镜像标签，格式为 `0.2.19-pk.N`，将 `N` 替换为实际已发布修订号（镜像标签不带开头的 `v`；禁止使用 `latest`） |
| `UPDATE_GITHUB_TOKEN` | 上述私库只读 token |
| `POSTGRES_PASSWORD` | 新的独立随机密码，替换示例值 |
| `REDIS_PASSWORD` | 新的独立随机密码 |
| `ADMIN_EMAIL` | 你自己的管理员邮箱 |
| `ADMIN_PASSWORD` | 新的管理员强密码；只用于首次初始化 |
| `JWT_SECRET` | 独立随机值，保存后固定使用 |
| `TOTP_ENCRYPTION_KEY` | 独立随机值，保存后固定使用 |
| `BIND_HOST` | 同机反代使用 `127.0.0.1` |
| `SERVER_PORT` | 宿主机空闲端口，例如 `8080` |
| `RUN_MODE` | `standard`，保留计费及余额校验 |
| `TZ` | 例如 `Asia/Shanghai` |

每个密码/密钥分别运行一次 `openssl rand -hex 32`，将结果填入对应行。保管 `.env`，不要上传到 Git 或发送到公开日志。已有部署恢复时使用原密钥；首次初始化后，仅修改 `ADMIN_PASSWORD` 环境变量不会重置现有管理员密码。

合入上游 v0.2.14 后的新版本，全新初始化时显式填写的管理员邮箱须通过登录邮箱校验，密码须为 8–72 字节（不是字符数）；留空的邮箱或密码会随机生成，可从首次启动日志中的 `Generated admin` 获取。已有用户时跳过管理员创建，不因旧环境变量不符合新校验而重建账号或修改凭据。部署时以所选私有 Release 的说明为准。

同时检查模板中的数据库资源参数：示例 `POSTGRES_SHARED_BUFFERS=1GB`、`POSTGRES_MAX_CONNECTIONS=1024`、`DATABASE_MAX_OPEN_CONNS=256`，应按新服务器的内存和并发需求调整，不能将它们视为适合所有机器的最低配置。

`SERVER_PORT` 控制宿主机映射端口，容器内仍为 `8080`。保留模板中的 `SUB2API_DOCKER_SELF_UPDATE=true`、`restart: unless-stopped` 和 `/app/data` 持久化卷，网页更新依赖这些配置。单实例首次安装沿用模板，不设置蓝绿流程专用的后台激活文件。

如需目录挂载，可改用 `docker-compose.local.yml`，但之后每条命令都要使用同一文件。`docker-compose.pk.yml` 是已有环境的附加配置，不能独立启动完整服务。

## 4. 启动与验收

以下命令仍在 `sub2api/deploy` 目录执行：

```bash
docker compose --env-file .env -f docker-compose.yml pull
docker compose --env-file .env -f docker-compose.yml up -d
docker compose --env-file .env -f docker-compose.yml ps
docker compose --env-file .env -f docker-compose.yml logs --tail=100 sub2api

# 若设置了其他 SERVER_PORT，下方地址也要同步修改
curl --fail --silent --show-error http://127.0.0.1:8080/health
curl --fail --silent --show-error http://127.0.0.1:8080/api/v1/settings/public
```

等待数据库初始化及迁移完成，核对三个服务的状态、启动日志和页面。`/health` 只验证 HTTP 存活；公开设置接口还会读取数据库，不能只凭一个健康响应认定完整部署成功。

配置反代前，可从自己的电脑建立 SSH 隧道访问本机绑定端口：

```bash
ssh -L 18080:127.0.0.1:8080 你的SSH用户@你的服务器地址
```

保持连接，再在浏览器访问 `http://127.0.0.1:18080`。用 `.env` 中的管理员账号登录，核对顶部显示所选的正式版本及 `PK出版` 标识。

## 5. 域名、反向代理与首次业务配置

1. 将自己的域名解析到新服务器，在反向代理上配置 HTTPS，代理到上面的宿主机端口，例如 `http://127.0.0.1:8080`。此地址适用于反代与应用同机、反代能访问宿主机回环地址的情况；桥接网络中的代理容器需按实际网络配置目标。
2. 按 [边缘与反代配置](EDGE_SECURITY.md) 设置可信代理和转发头，并保留 WebSocket 转发。SSE 路由需及时转发数据，避免代理/CDN 缓冲或缓存响应；长请求超时按业务设置。
3. 通过域名检查首页、登录、静态资源和模型接口。早发心跳的效果还受实际客户端、代理和网络影响，需通过最终域名验收。
4. 在后台创建自己的分组，添加自己的上游 API Key 账号并关联分组，核对模型、价格和额度，再创建下游 API Key。账号测试和模型请求可能产生上游费用。
5. 按业务需要配置账号的缓存转换开关、倍率和费用设置，核对下游 usage 与账单。全新部署不会带入其他站点的号池和业务设置。

“系统设置 → 首字设置”默认关闭，随机范围默认 1–2 秒。可选择注释保活、下游兼容 ping、空格首字三种模式，旧配置保持原选项。前两种不写正文；空格模式发送并保留 U+0020，Responses 保留真实响应 ID，等待创建事件时可能晚于设置时间。普通用户仍看“首字”，管理员另见“上游首字”。详细协议范围与 New API 边界见 [PK维护说明](../PK_MAINTENANCE.md#三种提前响应模式)。

## 6. 后续网页更新

1. 先阅读目标私有 Release 的说明，备份数据库、应用数据、部署配置及密钥，并安排允许重启的维护时间。
2. 管理员打开顶部版本菜单，点击刷新，确认读取的是本私库的新 PK 版本。
3. 点击“立即更新”下载安装包并校验；提示需要重启后，点击“立即重启”。
4. 等待服务恢复，检查版本、登录、模型接口及使用记录。

网页操作不会修改 Docker 端口映射或反代配置；同一 `/app/data` 卷保留网页安装的程序。**单进程重启会短暂中断服务和在途流式请求**。需要保留请求时，使用维护文档中的人工蓝绿切换流程。

如果新版本还修改了运行库、资源文件或容器启动脚本，应按该版本说明更新镜像；网页替换二进制不足以应用这些变化。镜像更新只操作应用服务，保留现有数据库、Redis、网络和卷。不要将 Git 拉取代码当成运行中的服务已经升级。

## 7. 备份、回退与恢复

备份至少包含：PostgreSQL 的一致性备份、Redis 持久化/状态恢复方案、`/app/data` 全部内容、Compose 与 `.env`、反代配置，以及对应私库版本和镜像 digest。命名卷在 Docker 管理的目录中，单独打包源码目录不包含这些数据。

恢复和回退按 [PK维护文档](../PK_MAINTENANCE.md#回退与恢复先选热回滚) 执行：使用私库对应版本和原密钥，先确认数据库迁移兼容性。退回旧程序不会撤销数据库迁移；同镜像重建会保留网页升级后的程序，不能直接当作回退。

## 8. 账号授权所需的外置浏览器（Linux）

账号授权（PK-036）用真实浏览器完成 OpenAI 登录与 Cloudflare 验证。浏览器**不打包**进主程序、网页更新归档或应用镜像，必须在服务器运行环境单独安装；缺少浏览器时授权任务直接报 `browser_missing`，不会静默降级。

### 8.1 安装

在宿主机执行（只把依赖装进指定容器，不重启服务、不动数据）：

```bash
sh deploy/install-browser.sh <应用容器名>
```

脚本做三件事：`apk add --no-cache chromium xvfb`、校验 `/usr/bin/Xvfb`、生成 `/usr/local/bin/chromium` 包装器（默认带 `--no-sandbox`）。

手动等价操作：

```bash
docker exec --user 0 <应用容器名> apk add --no-cache chromium xvfb
docker exec <应用容器名> sh -c 'command -v chromium; command -v Xvfb; chromium --version'
```

**为什么必须装 Xvfb**：无头 Chromium 常被 Cloudflare 判为机器人，授权流程在挑战失败后会切换到“后台真 Chrome 会话”，这需要一个图形环境。既没有 `Xvfb` 也没有 `DISPLAY` 时，后台会话报 `browser_display_missing`。

pk.48及更早版本的同一提示也可能是`:90`～`:99`的残留锁占满，不能只凭提示重复安装。先以应用用户检查Xvfb可执行性与显示进程；不要批量删除显示锁，仍有进程使用的显示会受影响。pk.49起使用Xvfb自动分配和就绪通知，正常退出优先TERM，避免固定十槽耗尽；旧残锁不要求人工清空。该程序修复可通过网页更新生效，外置浏览器依赖仍按上述方式管理。

### 8.2 自动补装

入口脚本默认 `PK_AUTH_BROWSER_BOOTSTRAP=true`：容器启动时若缺 `chromium` 或 `Xvfb`，自动执行 `apk add`（最多等待 240 秒）。安装失败只打印一行警告，不阻塞网关启动；首次或重建容器需要能访问 Alpine 软件源。

- 网页更新只替换二进制并重启同一容器，**已安装的依赖保留**，不会重复安装。
- 更换镜像或删除重建容器会丢失系统依赖，由入口脚本自动补装；也可再执行一次 8.1 的安装脚本。
- 关闭自动补装：设置 `PK_AUTH_BROWSER_BOOTSTRAP=false`。

### 8.3 相关环境变量

| 变量 | 默认 | 作用 |
|---|---|---|
| `PK_AUTH_BROWSER_PATH` | 自动查找 `chromium` | 指定浏览器可执行文件路径 |
| `PK_AUTH_BROWSER_MODE` | 空（先无头，挑战失败转后台） | `headless` / `background`；机房 IP 建议直接 `background` |
| `PK_AUTH_BROWSER_NO_SANDBOX` | 容器内 `true` | 容器中的 Chromium 需要关闭沙箱 |
| `PK_AUTH_BROWSER_DEVICE` | macOS 身份 | 设为 `host` 时使用宿主真实平台，仅调试用 |
| `PK_AUTH_BROWSER_TRACE` | `true` | 授权流程日志（阶段、上游状态与响应、已脱敏），设 `false` 关闭 |

### 8.4 验收

```bash
docker exec <应用容器名> chromium --version
docker exec <应用容器名> sh -c 'Xvfb -help 2>&1 | head -2'
# 授权任务结束后确认没有残留进程
docker exec <应用容器名> sh -c 'ps -eo args | grep -c "[c]hrom"'
```

浏览器按任务回收：每次尝试结束都会调用 `Browser.close`、取消上下文杀进程、删除临时 profile 并关闭自启的 Xvfb，宿主机上不应出现持续运行的 chromium。

### 8.5 常见报错

| stage | 含义 | 处理 |
|---|---|---|
| `browser_missing` | 没找到浏览器 | 按 8.1 安装 |
| `browser_display_missing` | 后台会话缺少图形环境 | 安装 `xvfb` |
| `proxy_challenge` | 授权代理出口被 Cloudflare 拦住 | 更换授权代理 |
| `login_security` | 挑战未在窗口内完成 | 换出口或稍后重试 |
| `login_reload` | 页面无响应，已自动刷新重填 | 属正常自愈；频繁出现说明出口过慢 |

## 常见问题

| 现象 | 优先检查 |
|---|---|
| 拉镜像提示 denied | Docker 登录用户是否为 token 所属账号、是否有该包的读取权限 |
| 网页更新提示 401/403/404 | 应用的 `UPDATE_GITHUB_TOKEN` 是否有效、是否能读取本私库；不要只检查 Docker 登录 |
| 容器启动失败 | Compose 状态和日志、数据库密码、迁移错误、端口/容器名冲突 |
| 本机能打开，域名打不开 | 域名解析、TLS、反代目标端口与网络路径 |
| 心跳到达客户端仍慢 | 实际域名链路的流式缓冲、CDN 和客户端读取方式 |
| 新设置没有出现 | 运行版本是否已更新并重启；刷新浏览器页面 |

原 `install.sh`、`docker-deploy.sh` 安装入口在 PK版已停用。源码在提交前完整验证，默认使用用户指定服务器隔离环境，本机 Linux/WSL 仍可用；报告标明实际环境并绑定最终提交。候选及正式发行构建只在 Jenkins 进行，默认一次 `ACTION=release` 校验源码证据、构建、验收同次制品并正式发布，之后可按授权网页更新。完整门禁及旧手动兼容流程见 [PK_MAINTENANCE.md](../PK_MAINTENANCE.md)。
