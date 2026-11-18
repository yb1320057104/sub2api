# Kilo 执行器的独立构建与部署

本目录是可选的独立执行器入口。应用继续使用原来的 Dockerfile、Compose、更新源与发布门禁；`backend/internal/reauthruntime` 只监督 release 摘要绑定的 OpenAI quota worker，不负责启动 Kilo HTTP 服务。执行器 service 1.1.0 / protocol 1 支持 `login`、`resume_identity`、`connect_subscription`、`reconcile_subscription`、`reauthorize_subscription`、`approve_device`。Go 保存任务、身份与设备消费秘密，执行器只处理短期会话；设备 token 由 Go 独占消费。

当前交付包含源码、固定 MIT helpers 与 profile、Linux 构建脚本及可选 Compose。Windows 本轮没有执行 Linux cfsolve wrapper、Docker 镜像构建、真实登录、线上部署或 Release；已有离线协议检查不能替代这些验证。

## 固定输入与许可

| 输入 | 固定来源与校验 | 本仓库状态 |
| --- | --- | --- |
| toSub2 helpers | `poxiao33/toSub2@8548397e89bf80e508eda64a87e0d556d43abc84`；`protocol-login.mjs` Git blob `ff2db2507e401c29fa78705e44f5b40a3108a8c6`；`tls-transport.mjs` blob `3c8ba4508effa4856db725db9969945621fe8223` | `tools/kilo-runtime/vendor/tosub2/` 只保存两个原始 helper 及上游 MIT LICENSE，`.gitattributes` 保持精确字节。它不是完整 toSub2 CLI；quota worker 仍由现有 reauth Dockerfile 获取完整固定源码。 |
| cfsolve | `chris124567/cfsolve@19efa31656328528457376ae0895a61164b22da0`，MIT；Rust `1.98.0`，原 `Cargo.lock` | 保存 stdin wrapper 和 `licenses/cfsolve.LICENSE`；完整源码与原 vendor 是构建输入，禁止用历史 `challenge` 二进制替代 wrapper。 |
| profile 与 answers | 上述 cfsolve 的 `profiles/safari-26-ios.json`、`safari-26-ios/answers.json`、`safari-26-ios/challenge-page-answers.json` | 三文件已保存；按 `asset-manifest.json` 的 LF SHA256 校验。只保存无会话的浏览器 profile/答案，不复制 captures、attempts、运行目录或日志。 |
| Sentinel | `sentinel_bridge.js`、`sentinel_bootstrap.js`、`sentinel_sdk.js`，三项精确 SHA256 见 `asset-manifest.json` | SDK 是官方运行资产，当前未找到可确认的再分发许可；三 JS 均作为外部输入，不随源码分发。准备方必须自行取得合法使用/交付授权，再提供精确字节。仅 SHA 匹配不代表取得许可。`sentinel_curl_fetch.py` 使用本项目固定代理传输 shim。 |
| Node / Python | 镜像固定 `node:22.22.2-bookworm-slim`、`python:3.12.12-slim-bookworm`，npm 使用已有 lock 中的 `jsdom@26.1.0`，Python 依赖见 `requirements.txt` | 构建下载发生在隔离构建机，按实际镜像 digest、包锁与输出 manifest 留证；本轮未安装依赖。 |

原始来源：<https://github.com/poxiao33/toSub2/tree/8548397e89bf80e508eda64a87e0d556d43abc84>、<https://github.com/chris124567/cfsolve/tree/19efa31656328528457376ae0895a61164b22da0>。获取完整 cfsolve 时必须包含原 vendor，核对 HEAD、工作树和许可，不能把本机修改后的目录当作固定提交。

Sentinel 输入目录应只含这三个 JS，不能指向整个 staging、生产 runtime、配置或日志目录。镜像构建的 `--check` 会校验三 JS、helpers、profile，并通过真正 wrapper 的 `check_only` 加载答案；缺失、摘要不符或 wrapper 不能运行时构建失败，不会伪造就绪。

## 独立镜像和 Compose

在具备 Docker BuildKit、网络和 C/C++ 构建依赖的 Linux 构建机，从仓库根执行以下命令。构建会获取固定 cfsolve 源码、Rust crates、npm/Python 依赖及基础镜像；不启动应用。

```sh
docker buildx build --load \
  --file deploy/kilo-runtime/Dockerfile \
  --build-context sentinel_assets=/build/authorized-sentinel \
  --tag sub2api-kilo-runtime:reviewed-local .
```

记录输出镜像 digest，并将 `KILO_RUNTIME_IMAGE` 设置为已验收的不可变本地标签或 digest。Compose 示例作为 `deploy/docker-compose.local.yml` 的显式 override 使用，不自动覆盖已有环境。先从 `.env.example` 准备受限配置，使用独立组和共享只读 token 文件：

```sh
sudo groupadd --system --gid 29999 kilo-token
sudo install -d -m 0750 -o root -g kilo-token /etc/sub2api-kilo
# Initialize once: noclobber prevents silently replacing an existing token.
sudo sh -c '
  set -eu
  umask 077
  set -C
  openssl rand -hex 48 > /etc/sub2api-kilo/service.token
  chown root:kilo-token /etc/sub2api-kilo/service.token
  chmod 0640 /etc/sub2api-kilo/service.token
'
```

`KILO_TOKEN_GID=29999` 只是示例，选本机未占用、且不与镜像现有组冲突的专用 GID，使用 1024..2147483647 的无前导零十进制数，不能为执行器主组 10001 或应用主组 1000。token 文件由 root 持有、0640，父目录 0750；不要改为全员可读或把 token 放进环境/命令行。以上初始化遇到已有文件会直接失败；迁移时复用已配置的 token，不要重复生成。Go 的 `KILO_EXECUTOR_TOKEN_FILE` 与执行器的 `KILO_SERVICE_TOKEN_FILE` 映射到**同一个文件**，token 至少 32 个非空白可打印字符。

应用镜像账户为 `sub2api`（UID/GID 1000:1000），执行器为 10001:10001。**仅设置 `group_add` 不足以支持应用**：原入口的 `su-exec sub2api` 会从容器 `/etc/group` 重建附加组。本 override 以 root 进入只读挂载的 `app-entrypoint.sh`，验证专用 GID、创建 `kilo-token` 组并登记 `sub2api`；随后使用同一 `su-exec` 实际降权，核对附加组并尝试打开 token。GID 冲突、文件缺失/空文件、owner/mode 不符或降权后不能打开时立即退出，且不输出 token。原镜像入口随后照常修复 `/app/data` 所有权、恢复 PK 浏览器依赖、降权及初始化持久化可执行文件，最终应用仍以 UID 1000 运行；执行器直接以 UID 10001 与相同 `group_add` 运行。

保留原 `deploy/data:/app/data` 数据卷，不能挂到执行器，也不需要为 token 改动数据文件权限。数据目录及其可写子目录必须允许应用 UID 1000 写入；原入口对只读配置的 chown 失败仍保持兼容，但 NFS root-squash、只读数据卷或禁止 chown 的宿主机需由运维预先为 UID/GID 1000:1000 准备写权限，不能靠放宽 token 权限解决。这个 override 需要 rootful、可写容器 `/etc/group` 的现有 Alpine 应用镜像；应用 token 组登记和原 PK 初始化只发生在显式加载 override 时，普通部署不加载此入口。

```sh
# Run from the repository root; reuse these exact files for every operation.
kilo_compose() {
  docker compose \
    --env-file deploy/.env \
    --env-file deploy/kilo-runtime/.env \
    -f deploy/docker-compose.local.yml \
    -f deploy/kilo-runtime/compose.example.yml "$@"
}
kilo_compose config --quiet
```

多 Compose 文件相对路径基于第一份配置文件所在目录，因此 `./kilo-runtime/app-entrypoint.sh` 对应本目录脚本。两个 token bind 与入口脚本 bind 都设置 `create_host_path: false`，漏配文件时不会被 Docker 自动创建为目录。镜像建议先用上述根目录命令独立构建；`additional_contexts`、bind `create_host_path` 和依赖 `restart` 需要支持相应字段的 Compose 版本。`config --quiet` 校验但不打印可能含现有数据库/更新凭据的完整环境。确认镜像、token 路径/组、原数据库/Redis/持久化卷后，再由运维按原逐节点排空流程应用；本示例不代为执行 `up`、替换生产镜像或重建现有应用。

`network_mode: service:sub2api` 让执行器使用应用的网络命名空间。执行器只 bind `127.0.0.1:8099`，没有 published port，Go 使用 `http://127.0.0.1:8099`。不要改成 `http://kilo-runtime:8099`：Go 明确拒绝非回环明文 HTTP。应用容器重建后必须在同一次运维操作中一并重建 `kilo-runtime`，使其加入新命名空间；仅 `restart` 执行器不会重新绑定已替换的容器，不能用它代替重建。两个进程有独立监督器和只读 token；执行器禁用 core dump、不装应用数据卷。

在隔离 Linux 验收机上，先使用上述 `kilo_compose` 函数进行一次原入口的最终账户检查（会按原行为修复该验收数据卷，执行自定义命令时不会安装浏览器）：

```sh
kilo_compose run --rm --no-deps sub2api /bin/sh -ec '
  test "$(id -u)" = 1000
  case " $(id -G) " in *" $KILO_TOKEN_GID "*) ;; *) exit 1 ;; esac
  test -w /app/data
  : < "$KILO_EXECUTOR_TOKEN_FILE"
'
```

运维启动验收组合后，执行器必须通过 Compose 自带的带鉴权 `/v1/health` 检查（在 UID 10001 下读取同一文件，校验 protocol 1 与三项订阅能力），再由应用侧发起 Kilo 操作确认完整调用。`docker compose exec` 默认可能以 root 执行，单独在 root shell 中 `cat` 成功不构成应用读取权限证据。当前 Windows 环境没有 Docker/WSL，上述 Linux 读取、镜像内组登记和两容器真实鉴权尚未实测；shell/配置离线检查不能替代此验收。

## Linux portable / systemd

可使用一个**干净、只包含程序依赖**的 portable reauth 构建目录作为 Python/Node/toSub2 输入，不能直接打包正在使用的生产 runtime 或它的 token、journal、ready、release 描述和日志。已有 `tools/kilo-runtime/deploy/build_linux.sh` 会复制输入到全新构建目录，然后只在副本安装依赖、编译 stdin wrapper、运行离线夹具、打包并重定位 `--check`。构建机须预先具备 Rust 1.98.0、CMake、C/C++、libclang、Git；`KILO_WHEELHOUSE` 可提供离线 Python wheels。

```sh
KILO_BASE_RUNTIME=/build/clean-reauth-runtime \
CFSOLVE_SOURCE=/build/cfsolve-fixed \
KILO_SENTINEL_SOURCE=/build/authorized-sentinel \
KILO_PROFILE_SOURCE="$PWD/tools/kilo-runtime/profiles/safari-26-ios.json" \
KILO_BUILD_DIR=/tmp/kilo-build-unique \
bash tools/kilo-runtime/deploy/build_linux.sh
```

`build_cfsolve.sh` 验证固定 Git HEAD，在隔离副本编译，默认仅复用原树的 Cargo target 缓存，可用 `CFSOLVE_TARGET_DIR` 改为独立缓存；产物旁保存 `.source-verification.json`。原 checkout 的源码与锁文件不能被修改。system 模式可用 `build_bundle.py --system-python /usr/bin/python3 --system-node /绝对/node --tosub2-root /build/fixed-tosub2 ...`，它明确依赖目标主机的相同绝对路径与模块，不是跨主机 portable 包。

安装前先在隔离 Linux 主机检查 bundle manifest、重定位 `--check` 与鉴权 health。`install.sh` 将服务安装到 `/opt/kilo-runtime`，仅监听回环，token 默认位于 `config/service.token`。若应用是宿主机 systemd 服务，设置它的 `KILO_EXECUTOR_URL=http://127.0.0.1:8099` 和 `KILO_EXECUTOR_TOKEN_FILE=/opt/kilo-runtime/config/service.token`；通过专用组 ACL 给应用用户最小读取权限，包括父目录 traversing 权限，不能只 chmod token 而忽略默认 0700 config。推荐另将 token 安装到共用的 root:专用组 0640 文件，并让两个 unit 指向该文件；不要在 unit Environment 写原始 token。现有应用 unit 的排空/退出预算保持。

`install.sh ... --replace-legacy` 是**确认旧服务存在**，脚本不会 stop/disable `kilo-login.service`。先自行安排旧监听端口或经独立授权停止旧服务；若旧服务仍占 8099，新服务的鉴权就绪检查失败并回滚新 unit/current，旧服务保持原状。安装器不会因为该 flag 自动迁移凭据或宣称旧登录器已被替代。

## 本轮验证范围

本轮在 Windows / Python 3.15 / Node 24.21.0 执行现有检查：Kilo 生命周期/API 31 项通过；真实固定 helper 的完整 `test_protocol.mjs` 通过，覆盖精确工作区、来源签名、身份关联和只读观察。Codex adapter/runtime controls 共 14 项中 13 项通过、1 项 POSIX SIGTERM 专项跳过；原 worker 34 项通过；发布输入绑定 3 项通过；Node password/diagnostics 首次 8 项中 7 项通过、1 项因 Windows 将 POSIX 0600 报告为 0666 而失败，未改测试，随后只执行跨平台 7 项均通过。未修改的 Go `reauthruntime` 检查为 5 项顶层通过、2 项 Windows 路径/权限断言失败、5 项 Linux 专项跳过；不能称 Go/Linux 生命周期全通过。3 份部署脚本在已有 Git Bash 做 `bash -n` 语法检查通过，没有执行安装或 Linux wrapper。

另以临时合成依赖与 ELF 头运行组包合同夹具：生成归档的 29 项文件逐一 SHA256 核验通过，确认旧 `release.json`、worker、token、config 全部排除；未执行合成 ELF，也不将其视为 Linux wrapper 验收，夹具和归档已清理。最终 Python/Node 语法、固定资产/许可摘要、Docker COPY 源输入与共享回环/token 配置检查通过。

部署权限补充检查：新增前置入口在 Git Bash 通过 `bash -n`；既有 PyYAML 成功解析两份 Compose，并离线核对专用 GID、相同 token bind、缺失源拒绝、数据卷保留及带鉴权回环 health 配置。临时 shell 模型夹具 16 项通过，覆盖原入口丢失附加组的模型、修补后组登记与原入口衔接、重启、非法/冲突 GID、缺失/空 token、错误 mode 和降权探测失败。夹具中的 `id`、`stat`、`addgroup`、`su-exec` 都是替身，临时文件已删除；这没有验证 Linux 内核 DAC、真实 `su-exec` 或 Docker Compose 渲染，不能称为两容器上线验收通过。

协议 HTTP 均由内存/离线 fixture 接管。本机现有 jsdom 为 24.1.3，正式锁定依赖为 26.1.0；镜像里的相同夹具必须再以锁定依赖通过。本轮没有 Docker/WSL/Linux wrapper 实际执行，也未确认 Sentinel 可再分发授权和真实上游授权/模型路由。上线前须在隔离 Linux 环境完成这些实际检查，之后按原应用发布和逐节点流程处理；不能复用其他仓库的 `release.json` 或旧版本发行包作为本版证据。
