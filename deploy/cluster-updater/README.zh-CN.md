# 集群应用统一更新

GitHub 私库发布正式 Release 后，管理员在 `/cluster-update/` 确认一次具体版本。更新器下载确定提交的源码、合并已校验的 HA 补丁，构建一份程序，依次更新两台 API 和两台后台 worker 候选。只在数据库写主运行后台 worker。第三台纯仲裁服务器没有应用程序，不随应用版本更新。

主、副实际版本和后台版本分别显示。GitHub 正式最新版本与两台 API 的正式基线相同时显示已同步；仅推送 main 或工具包更新不会自动成为应用更新。目标标签、提交或候选变化会使旧确认失效。不会自动批准新版本，也不会自动降级。

## 更新流程

1. 两节点健康和更新能力预检，验证部署身份与固定 SSH 主机密钥。
2. 获取私库正式 Release 对应的确定提交，在隔离构建容器中保留 HA 代码、完成前端构建和定向 Go 检查。GitHub 凭据不交给构建容器；有合并冲突即停止。
3. 两台接收并校验同一份程序 SHA256，准备停止状态的新 API 和 worker；用户数据、配置、数据库、Redis、域名保持。
4. 先副 API，后主 API：从两入口摘除目标，另一台继续接流；等旧请求自然完成和计费收尾后退出旧进程，启动新程序，验证真实存储就绪，再恢复入口。
5. 逐台替换 worker，保持与当前数据库写主一致。最终核对所有应用程序版本、就绪和单个活动 worker。

每一步保存本机私有记录。失败时停止后续节点更新；能证明归属和健康的当前节点执行局部恢复。不会强制杀掉尚在处理的模型流，不会自动撤销数据库迁移。失联、进程被杀或恢复证据不完整时，显示需要检查，不猜测继续。

## 适用范围

本控制器适配已经部署的两应用节点与专用存储集群：已接受的 gate/runtime、候选 Docker 规格、HAProxy 入口及 worker supervisor。它不是新机安装器；新站安装见 `deploy/cluster-kit/`。节点地址、域名、私钥、GitHub令牌和已运行容器身份从本机私有配置或现有运行记录读取，不进入源码。

自动更新要求目标 Release 的迁移目录全部文件及 SHA256 与已审查 HA 基线一致，目前共348个文件，其中324份SQL。新增、删除或修改任一文件都会在构建和部署前拒绝，并提示 `cluster_migrations_require_compatibility_review`；需先审查新旧程序兼容性并维护新的 HA 基线。程序回滚不撤销迁移；不自动升级 PostgreSQL、Redis、etcd 或操作系统。

## 运维

`server.py` 提供管理员确认界面；`builder.py` 解析正式 Release 并构建；`cluster_rollout.py` 按顺序协调；`agent.py` 在各节点执行限定动作。远端密钥使用 SSH forced command，不提供交互 shell、转发或 SFTP。

控制器配置包含 `root`、`rollout_enabled`、`auth_port`、`auth_host`、`allowed_origins`、`listen_port`、`poll_interval_seconds`、`github_token_file`、`overlay_dir`、`work_dir`、`nodes`。节点代理配置包含 `schema=1`、`node`、`root`、`gate_runtime_dir`、`worker_runtime_dir`、`edge_rollout_module`、`candidate_root`，可选 `worker_unit`、`github_token_file`、`browser_runtime_images`、`api_resources` 与 `worker_resources`。这些文件及密钥需要 root 私有权限。

`api_resources` 与 `worker_resources` 分别控制本节点下一代 API、worker 候选，互不继承。每个可选对象例如 `{"cpus":8,"memory_mib":4096,"go_memory_mib":3276,"cpu_shares":4096}`：`cpus` 必须是 1～32 的整数；`memory_mib` 是 1024～65536 的整数；`go_memory_mib` 是大于 0 且严格小于容器内存的整数，须给非 Go 堆、浏览器和进程留出余量。前三项必须同时配置；可选 `cpu_shares` 是 2～262144 的整数，表示同宿主竞争 CPU 时的相对权重，不预留 CPU，也不能消除宿主机的 steal。显式 null、布尔值、小数和未知字段均拒绝。按宿主实际 CPU、可用内存、其他服务与容器外内存开销选择值；该配置不为宿主增加实际资源。

代理按角色同步设置 Docker `NanoCpus`/`Memory`、可选 `CpuShares` 与 Go `GOMAXPROCS`/`GOMEMLIMIT`，后者使用 `MiB` 单位。未配置的角色完整继承原有资源和环境，未配置权重则保留原权重；worker 不会继承 API 预算或补入 API 发行凭据。非零 `CpuQuota` 或 `CpuPeriod` 拒绝与新限额混用。原 `MemorySwap` 为正数时保留其与原 `Memory` 之差，即允许使用的额外 swap 字节数：两者原本相等则继续禁用 swap；Docker 默认值 `0` 和不限 swap 的 `-1` 保留。无效的旧 memory/swap 关系拒绝转换；新内存不能小于原 `MemoryReservation`。

资源变更写入新候选规格及回执哈希，仍通过现有摘流、排空、就绪和回退流程；修改私有配置本身不会调整运行容器。运维端可以用 `cluster_rollout.deploy(config, artifact, report)` 对同一受校验产物发起新的 revision，先更新 secondary，再更新 primary；普通版本界面会把相同 baseline 显示为已同步。不要用未记录的 `docker update` 代替候选更新，否则下一次更新仍会继承旧规格。

节点代理的可选 `github_token_file` 指向本机已有、root私有的GitHub令牌文件。只在创建API候选且原环境的 `UPDATE_GITHUB_TOKEN` 缺失或为空时补入；已有非空令牌保持，不给worker补入，不向构建容器提供。未配置该字段时不改变原凭据行为，令牌值不进入公开回执或日志。这解决API访问私库正式版本时缺凭据导致强制检查失败的情况。

历史r1候选回执没有 `revision` 字段时，代理补为1后仍通过原身份校验；名称必须精确属于r1，容器、镜像、规格、程序及网络等原有固定信息继续核对，不放宽其他候选代次。

HA 补丁源由 `prepare_overlay.py` 从固定私库提交与已校验源码生成。更新器不读取开发者未提交的工作区改动。历史 ordinary-secondary 更新器保持停用发布能力。

`patches/version-baseline.patch` 补充应用后台的版本比较修复：运行版本的严格 `-ha.YYYYMMDD.N` 或 `-secondary.YYYYMMDD.N` 后缀只在比较时归一到正式基线，实际版本显示和构建类型保持。未知／畸形后缀不归一；正式发行标签准入及非release构建禁止原地升级／回退的规则保持。生成器从已验证r4源码在隔离临时目录应用此补丁，核对补丁与修改前后SHA，最终生成96份合并输入（原94份加2份版本比较源码／测试），并加入独立版本比较测试组。原cluster-kit的r4清单和安装包保持。

本次同基线修复已通过人工接续完成：两API及两worker均运行`0.2.19-pk.58-ha.20261108.5`，候选代次为7；两gate释放且API就绪，仅主节点writer上的worker运行。真实管理员检查中，两节点缓存与强制刷新共四次均返回200，正式最新版本为pk.58，`has_update=false`，实际HA版本和`cluster-ha`构建类型保持。统一更新控制器状态为`in_sync`，无待确认候选或错误；两公网就绪检查均200。

这次接续按用户“无需等待”的明确授权终止了旧连接，两个旧API退出码均为137，存在连接中断，不能视为无中断升级。默认自动更新流程仍等待请求完成，不因此获得以后强制终止的授权。本次是同基线修复，不替代下一正式Release的完整“页面确认→构建→逐台更新”验收。

## 重现 HA 补丁输入

在包含本目录及 `deploy/cluster-kit/` 的私库检出目录执行。`SOURCE_REPO` 指向拥有清单固定提交的私库克隆，`WORK_DIR` 指向受限的临时工作目录；生成目录不提交到Git，也不放入更新器源码包。

```sh
SOURCE_REPO="/path/to/private-repository"
WORK_DIR="/path/to/private-work-directory"
umask 077
mkdir -p "$WORK_DIR"
python3 deploy/cluster-kit/build/prepare_source.py \
  --repo "$SOURCE_REPO" --output "$WORK_DIR/source"
python3 deploy/cluster-updater/prepare_overlay.py \
  --repo "$SOURCE_REPO" --source "$WORK_DIR/source" \
  --manifest deploy/cluster-kit/patches/manifest.json \
  --output "$WORK_DIR/overlay"
```

第一步只从固定Git提交导出并校验源码；第二步逐文件核对、在临时目录应用本目录固定版本比较补丁，再生成三方合并输入与迁移基线映射。当前合并清单包括96份HA差异文件及348份迁移目录文件，其中324份SQL。把输出目录作为私有控制器配置中的 `overlay_dir`；再次生成时使用新的空输出目录。上述命令不安装服务、不更新应用，也不需要复制现有站点数据。

## 只读HA容器的浏览器运行环境

HA候选以用户1000运行、根文件系统只读，且不经过原镜像入口安装浏览器。若镜像未包含Chromium／Xvfb，授权预检会报`browser_missing`；直接对这类运行容器执行`install-browser.sh`会因只读系统拒绝apk安装。应在各节点从固定原镜像准备独立运行镜像，再通过候选更新切换，保持应用二进制、用户、只读根、网络和原业务挂载。

节点代理可选`browser_runtime_images`是“原镜像完整ID→浏览器镜像完整ID”的JSON映射，键和值均为`sha256:`加64位小写十六进制。代理在预检及候选准备时核对目标镜像实际ID及`sub2api.browser-runtime.schema=1`、`sub2api.browser-runtime.base-image=<原镜像完整ID>`标签；目标缺失、标签不符或源镜像未登记即拒绝。空映射维持原行为；已运行目标镜像会继续使用同一目标，不重复替换。映射同时用于API与worker候选，仅替换Image；没有授予容器写根文件系统或安装系统包的权限。

`Dockerfile.browser-runtime`适用于既有Alpine基础镜像，仅增加`chromium xvfb`，安装后使用`1000:1000`。在拥有固定基础镜像的每个节点，使用本机Docker镜像存储构建；`UPDATER_SOURCE`指向本目录，`BASE_IMAGE_ID`必须由已核验的候选规格取得。以下步骤仅准备镜像，不部署应用：

Chromium启动包装器会读取镜像内`/etc/chromium/99-pk-runtime.conf`。该配置仅在浏览器参数包含绝对路径`--user-data-dir=/...`时，将该浏览器进程的XDG配置与缓存目录放到现有临时profile的`.config`／`.cache`子目录，随profile一同清理，避免只读HOME导致crashpad启动失败。没有绝对profile参数时（例如`--version`）不改XDG环境；不改应用全局环境、运行用户或根文件系统权限。

```sh
set -eu
: "${UPDATER_SOURCE:?set updater source directory}"
: "${BASE_IMAGE_ID:?set pinned base sha256 image ID}"
printf '%s\n' "$BASE_IMAGE_ID" | grep -Eq '^sha256:[0-9a-f]{64}$'
test "$(docker image inspect --format '{{.Id}}' "$BASE_IMAGE_ID")" = "$BASE_IMAGE_ID"
BASE_TAG="sub2api-browser-base:${BASE_IMAGE_ID#sha256:}"
RUNTIME_TAG="sub2api-browser-runtime:${BASE_IMAGE_ID#sha256:}"
docker tag "$BASE_IMAGE_ID" "$BASE_TAG"
test "$(docker image inspect --format '{{.Id}}' "$BASE_TAG")" = "$BASE_IMAGE_ID"
BUILD_CONTEXT=$(mktemp -d)
trap 'rmdir "$BUILD_CONTEXT"' EXIT
docker build --pull=false --file "$UPDATER_SOURCE/Dockerfile.browser-runtime" \
  --build-arg BASE_IMAGE="$BASE_TAG" --build-arg BASE_IMAGE_ID="$BASE_IMAGE_ID" \
  --tag "$RUNTIME_TAG" "$BUILD_CONTEXT"
BROWSER_IMAGE_ID=$(docker image inspect --format '{{.Id}}' "$RUNTIME_TAG")
test "$(docker image inspect --format '{{index .Config.Labels "sub2api.browser-runtime.schema"}}' "$BROWSER_IMAGE_ID")" = 1
test "$(docker image inspect --format '{{index .Config.Labels "sub2api.browser-runtime.base-image"}}' "$BROWSER_IMAGE_ID")" = "$BASE_IMAGE_ID"
printf 'base=%s\nbrowser=%s\n' "$BASE_IMAGE_ID" "$BROWSER_IMAGE_ID"
```

不要把裸`sha256:镜像ID`直接放在FROM中；BuildKit可能将其当作镜像仓库名称拉取。上例先将已核验ID标记为本机唯一哈希标签，再核对标签指向并使用`--pull=false`。构建不挂载业务数据、配置、令牌或Docker socket；apk依赖从软件源下载，最终以实际生成的完整镜像ID登记，不将可移动标签写入代理映射。镜像需先在无业务挂载、相同用户与只读限制下验证Chromium／Xvfb能启动，再更新私有映射并执行正常逐台更新。构建或单次启动成功不等于完成账号授权验收。

本次线上验证已完成：两节点构建的运行镜像在只读、用户1000下均通过Chromium 136的headless与后台CDP启动检查；更新器按副节点、主节点及worker顺序切换到候选代次8，两个API就绪，仅主数据库writer上的worker运行。控制器状态已恢复为`in_sync`。授权代理106随后真实检查返回成功，数据库状态为`passed`，消息为`chatgpt.com GET预检通过`。本次未修改数据库结构、账号凭据或正式Release。
