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

控制器配置包含 `root`、`rollout_enabled`、`auth_port`、`auth_host`、`allowed_origins`、`listen_port`、`poll_interval_seconds`、`github_token_file`、`overlay_dir`、`work_dir`、`nodes`。节点代理配置包含 `schema=1`、`node`、`root`、`gate_runtime_dir`、`worker_runtime_dir`、`edge_rollout_module`、`candidate_root`。这些文件及密钥需要 root 私有权限。

HA 补丁源由 `prepare_overlay.py` 从固定私库提交与已校验源码生成。更新器不读取开发者未提交的工作区改动。历史 ordinary-secondary 更新器保持停用发布能力。

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

第一步只从固定Git提交导出并校验源码；第二步逐文件核对后生成三方合并输入与迁移基线映射。当前清单包括94份HA差异文件及348份迁移目录文件，其中324份SQL。把输出目录作为私有控制器配置中的 `overlay_dir`；再次生成时使用新的空输出目录。上述命令不安装服务、不更新应用，也不需要复制现有站点数据。
