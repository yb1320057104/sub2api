# PK出版部署指南

本指南用于获得授权的部署者，在**全新 Linux x86_64（amd64）服务器**上安装 PK出版。源码、镜像、网页更新和恢复版本均来自 [pengkang0322/sub2api 私库](https://github.com/pengkang0322/sub2api)。程序没有绑定固定服务器 IP 或域名。

本文以已发布的 `0.2.10-pk.7` 为例。以后安装其他版本时，先查看[私有 Releases](https://github.com/pengkang0322/sub2api/releases)的说明，再选择明确版本；当前发行物仅支持 `linux/amd64`。

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
| 获取部署文件、恢复源码 | GitHub CLI / Git 登录 | `pengkang0322/sub2api` 仓库读取权限 |
| 拉取 Docker 镜像 | 宿主机 `docker login ghcr.io` | token 所属账号能读取该私有包；classic PAT 含 `read:packages` |
| 网页检查版本、下载升级包和回退包 | 应用环境 `UPDATE_GITHUB_TOKEN` | 由仓库所有者为该部署单独配置 fine-grained token，仅允许该私库，Contents 只读，Metadata 只读 |

网页更新凭据不会替宿主机登录 Docker。每个部署使用单独的受限凭据、数据库和上游 API Key；源码包不包含现有站点的数据或密码。外部协作者的 fine-grained token 存在访问限制，不能假定受邀后就能以自己的账号选择他人的个人私库；由所有者单独配置该部署的更新凭据，避免复用现有生产 token。权限规则见 [GitHub token 文档](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens) 和 [GHCR 认证文档](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)。

## 2. 获取私库与登录镜像仓库

在尚不存在 `sub2api` 子目录的位置执行：

```bash
gh auth login --hostname github.com --git-protocol https --web
gh repo clone pengkang0322/sub2api
cd sub2api/deploy

read -rp 'GitHub 用户名（镜像 token 所属账号）: ' GHCR_USER
read -rsp 'GHCR 只读 token: ' GHCR_READ_TOKEN
printf '\n'
printf '%s' "$GHCR_READ_TOKEN" | docker login ghcr.io -u "$GHCR_USER" --password-stdin
unset GHCR_READ_TOKEN GHCR_USER
```

如果私库或私有包返回 404/denied，先核对账号授权；私有资源未授权时也可能返回 404。仓库所有者与登录用户可以不同，镜像地址始终为 `ghcr.io/pengkang0322/sub2api`。

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
| `SUB2API_PK_VERSION` | 已验收的私库正式标签，例如 `0.2.19-pk.9`（镜像标签不带开头的 `v`；禁止使用 `latest`） |
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

合入上游 v0.2.14 后的新版本，全新初始化时显式填写的管理员邮箱须通过登录邮箱校验，密码须为 8–72 字节（不是字符数）；留空的邮箱或密码会随机生成，可从首次启动日志中的 `Generated admin` 获取。已有用户时跳过管理员创建，不因旧环境变量不符合新校验而重建账号或修改凭据。本文旧版本示例不代表已包含这项修复，部署时以所选私有 Release 的说明为准。

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

保持连接，再在浏览器访问 `http://127.0.0.1:18080`。用 `.env` 中的管理员账号登录，核对顶部显示 `0.2.10-pk.7 PK出版`。

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
