# PK出版：源码、发布与上游更新

## 仓库与当前基线

- 私有仓库：<https://github.com/pengkang0322/sub2api>，本地 remote 为 `origin`。
- 原作者仓库：<https://github.com/Wei-Shaw/sub2api>，本地 remote 为 `upstream`。
- 二开基于上游 `a60a29549f488a854966aaec9541abbe006cac22`，版本 `0.2.10`。
- 原样保存现有业务的提交：`6655dd8ca`；备份标签：`pk-baseline-20260930`。
- 真正业务差异为 26 个修改文件和 4 个新增文件。最初逐字节对照多出的一项为工具文件换行差异，Git 没有将它识别为内容修改。
- `main` 保存经过核对的 PK 代码。合并上游、发布新版本、更新线上是三个独立步骤。

## 必须保留的二开功能

| 功能 | 必須保持的行为 | 主要代码 |
|---|---|---|
| 账号设置 | OpenAI 新增/编辑账号支持缓存转换开关和独立倍率；默认关闭、倍率 1；0 有效 | `CreateAccountModal.vue`、`EditAccountModal.vue`、`account_cache_conversion_validation.go`、`admin_account.go` |
| 设置保存 | 使用 `accounts.extra.cache_conversion_enabled` 和 `cache_conversion_multiplier`；普通更新缺少字段时保留原配置 | 账号校验与账号 repository |
| 缓存转换 | 新读取量 = 原读取量 + floor(创建量 × 倍率)；创建量与 5m/1h 明细清零；保留普通输入和输出 | `pkg/cacheconversion/usage.go`、`openai_cache_conversion.go` |
| 费用计算 | 先从原始输入拆分缓存，再转换并定价；记录、余额、订阅、API Key 额度采用同一结果 | `openai_gateway_usage.go`、`billing_service.go` |
| 长上下文定价 | 保留原始上下文长度，不能用转换后的 token 决定阶梯 | `UsageTokens.OriginalContextTokens`、`InputContextTokens()` |
| 下游 usage | 开关开时同步改写；关时保持原路径；保留原始用量单独计费，避免二次转换 | `openai_cache_conversion_output.go` 与各协议出口 |
| 协议覆盖 | Responses、Chat、Messages 的 JSON/SSE、兼容桥接、直通、WebSocket 文本帧、图片和 embeddings | 所有 `openai_gateway_*`、`openai_ws_*` 及相关入口 |

账号成本统计的自定义价格路径使用原始 token；“将客户定价应用于账号统计”的分支使用转换后的 TotalCost。账号倍率、分组倍率与缓存转换倍率是不同设置，不可合并。

以前会话记录过服务器端受控探测和真实上游探测，例如创建 4791、倍率 0.5 转成读取 2395；这些是历史验证，本次没有重新对生产发起收费探测。

### 静态审计发现的既有边界

本次未改动以下业务行为，应作为独立修复处理：

1. 重新导入已有 Codex Session 时，新建弹窗的默认 `false/1` 会显式覆盖已有转换设置；普通编辑的缺字段保留逻辑无法挡住它。
2. 超大倍率可能使转换结果超过 int32。部分非流式 Responses/embeddings 路径返回 `nil,error`，可能丢失已采集 usage 的结算证据。
3. 批量编辑页面没有转换设置 UI，后台批量 API 支持；WS 二进制帧沿用直通行为。

## 私有发布与网页一键更新

版本格式：`0.2.10-pk.1`，下一次本地修订为 `0.2.10-pk.2`；合并下个上游后为 `0.2.11-pk.1`。页面追加 ` PK出版`，中文不进入机器版本号。

1. 核对改动并提交到 `main`。
2. 为该提交创建一个从未使用的新标签，例如 `v0.2.10-pk.1`，只推送该标签。
3. 在 GitHub Actions 的 **PK Release** 中输入该标签并手动执行。
4. 云端执行前端构建、更新模块检查、后端编译和 Docker 启动检查。
5. 同一个二进制同时用于 `ghcr.io/pengkang0322/sub2api:0.2.10-pk.1` 和 Release 的 `sub2api_0.2.10-pk.1_linux_amd64.tar.gz`；Release 包含 `checksums.txt`。
6. Release 设为正式发布，尽管标签中含有 `-pk.N`。只发布镜像而没有二进制和校验文件不能用于网页更新。
7. 发布不会连接生产服务器，也不会自动替换线上容器。用户确认后再进行首次部署；以后网页更新由管理员点击触发。

当前发布目标为生产服务器对应的 `linux/amd64`。其他架构须先扩展发布矩阵。

### 为什么首次部署不只是改 image

网页更新会替换正在运行的程序。PK 镜像以普通应用用户运行 `/app/data/bin/sub2api`，依赖原有 `/app/data` 持久化卷；不需要 Docker socket。

- 同一镜像重启、重建：保留网页更新后的程序。
- 更换到二进制不同的镜像：根据镜像 SHA256 种子原子安装该镜像的程序，包括回退到旧镜像。
- 同一镜像的普通重建保留网页更新后的程序。要强制恢复所选镜像内的版本，应使用网页版本回退，或按下方手动回退步骤停机后重置程序种子标记；页面的 Docker 手动回退指引包含这一步。
- 更新程序在同一文件系统内进行备份、替换；Docker `restart: unless-stopped` 负责网页点击重启后的进程恢复。
- 未来涉及镜像运行库、entrypoint、资源目录或数据库不兼容变更时，必须使用对应镜像部署；单独替换应用二进制不能替代这些部署步骤。

首次切换必须保留已有 Compose、端口、数据库、Redis 和所有数据卷。`deploy/docker-compose.pk.yml` 是附加配置，不能拿它替换线上完整 Compose。

### 私库凭据

- 网页更新：`UPDATE_GITHUB_TOKEN` 使用仅允许 `pengkang0322/sub2api`、`Contents: read` 的 fine-grained token；通过服务器环境配置注入。
- Docker 拉取：登录 `ghcr.io`，使用可读取此镜像的凭据；GitHub Packages 的经典 token 需要 `read:packages`。
- Actions 发布使用内置 `GITHUB_TOKEN`，不保存服务器密码。
- `.env.pk` 已加入忽略列表；`deploy/.env.pk.example` 仅含空凭据示例。绝不把真实 token、数据库配置或服务器密码写入仓库。

网页更新与回退仅检查本私库。资产 API 带授权，跳转到 GitHub CDN 时删除授权头；所有归档必须通过 SHA256 校验。更新缓存带仓库标识，旧官方缓存不能用于安装。

## 每次合并原作者的版本或分支

以合并 `v0.2.11` 为例，在工作区干净且已有备份时：

```sh
git switch main
git pull --ff-only origin main
git fetch upstream --no-tags
git fetch upstream tag v0.2.11
git switch -c sync/upstream-0.2.11
git merge --no-ff v0.2.11
```

要合并作者某个分支，可先 fetch 该分支，再在独立 `sync/` 分支合并相应 `upstream/分支名`。不要用整目录覆盖、`reset --hard upstream/main`、强制推送或 `push --mirror` 更新自己的仓库。

即便 Git 自动合并没有冲突，也必须核对上面的功能表。尤其不能只保留 4 个新增文件而丢掉协议出口接入点。解决冲突后先发 PR，在 GitHub 云端检查；确认通过再合入 `main` 并发布新的 PK 版本。生产部署另行确认。

上游 v0.2.11 与二开重叠 8 个文件，还默认引入余额并发预留。其预估成本与缓存转换后的实扣成本可能不同，需要核对低余额、多并发场景，不能直接当作无影响升级。目前业务基线仍为 0.2.10。

## 部署前与回退

2026-09-30 只读核查的生产实例：`sub2api-1`，应用端口 `127.0.0.1:18088`，PostgreSQL 18.6，Redis 8.10.1，应用数据使用已存在的命名卷。此信息供部署核对，不应写成新的全量 Compose 覆盖线上配置。

上线前备份数据库、应用数据及 Compose/.env，记录旧镜像 digest 和实际运行的程序版本。只重建应用服务，保留数据服务。新版本运行了数据库迁移后，不能假定退回旧镜像就能回滚；须结合备份和迁移兼容性处理。

手动 Docker 回退时，先把 Compose 中的应用镜像改为经过确认的目标版本并完成 GHCR 登录，再执行以下步骤（以 `0.2.10-pk.1` 为例）。所有 Compose 命令需沿用线上原有的 `-f`、`--env-file` 和项目名参数：

```sh
docker pull ghcr.io/pengkang0322/sub2api:0.2.10-pk.1
docker compose stop sub2api
docker compose run --rm --no-deps sub2api rm -f /app/data/bin/.image-seed.sha256
docker compose up -d --force-recreate --no-deps sub2api
```

清除的仅是程序种子标记，下次启动会从所选镜像重新安装程序；应用数据卷、数据库和 Redis 保留。即使所选镜像与当前 Compose 中的镜像相同，这些步骤也会撤销网页更新并恢复镜像内的程序。

用户要求：不运行任何本地构建或测试；新生成的临时测试文件用完删除。本流程的构建和检查在 GitHub Actions 执行，既有源码测试保留。
