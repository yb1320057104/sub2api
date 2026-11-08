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

控制器配置包含 `root`、`rollout_enabled`、`auth_port`、`auth_host`、`allowed_origins`、`listen_port`、`poll_interval_seconds`、`github_token_file`、`overlay_dir`、`work_dir`、`nodes`。节点代理配置包含 `schema=1`、`node`、`root`、`gate_runtime_dir`、`worker_runtime_dir`、`edge_rollout_module`、`candidate_root`，可选 `worker_unit` 与 `github_token_file`。这些文件及密钥需要 root 私有权限。

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
