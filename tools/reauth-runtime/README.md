# 登录运行包部署验证

`build.sh` 生成 release 使用的便携 Python / Node / toSub2 运行包。构建验证覆盖 Alpine 和 Ubuntu 24.04；Ubuntu 中经 `current` 符号链接启动 Node → Python → curl_cffi，以及 Python → Node 加密恢复日志，防止仅在构建容器中可用。

```bash
VERSION=0.2.19-pk.66 ARCH=amd64 SOURCE_COMMIT=<完整已审阅提交> bash tools/reauth-runtime/build.sh
```

正式发行必须先构建此包，再用同一包的 SHA256 编译普通和 HA 主程序。构建参数为
`-X github.com/Wei-Shaw/sub2api/internal/reauthruntime.ReleaseVersion=<正式版本>`、
`-X github.com/Wei-Shaw/sub2api/internal/reauthruntime.ArchiveSHA256=<包的SHA256>`、
`-X github.com/Wei-Shaw/sub2api/internal/reauthruntime.ArchiveArchitecture=<amd64或arm64>`。
主程序版本即使附加 HA 后缀，也使用上述显式运行包版本。缺少绑定时不会尝试下载或执行运行包。

运行包必须作为固定仓库 `yb1320057104/sub2api` 同版本 Release 资产发布。
可在更新前将同一包预置于 `<DATA_DIR>/credential-worker/packages/`；包名保持
`sub2api-reauth_<正式版本>_linux_<架构>.tar.gz`。预置包存在但校验失败时停止准备，不自动换源。
下载后也会保留已校验的归档，用于每次进程启动核对已安装脚本及全部动态库。
`manifest.json` 和 `integrity.json` 记录版本、来源提交与文件摘要；包自身摘要由发行流程外部记录，避免循环哈希。

正常停止使用 SIGTERM：主管进程停止领取，等待所有在途授权及回调完成后退出。
托管进程意外失去父进程使用独立 SIGUSR1 清理其进程组。HA 必须先停止授权生产者、等待该进程退出，再关闭内部回调 HTTP 服务。

## 本地 API 与数据库验收

`test_deployment.py` 只能连接回环地址。使用隔离 PostgreSQL / Redis、测试管理员和测试 Worker token，应用初始化及迁移不得连接生产数据。API 的 `OPENAI_REAUTH_WORKER_TOKEN` 必须与脚本相同，以便独立运行包报到。后台不要同时启动其他 Worker；脚本会主动创建虚构 OAuth 账号及 2FA 任务。

先在应用持久目录中创建 `data/` 并启动本地 API；再解压完整运行包，保留包内 Python、Node、toSub2 及动态库。把运行包目录链接为 staging 根目录下的 `current`：

```bash
mkdir -p /tmp/sub2api-totp-staging/releases/test
tar -xzf reauth-output/sub2api-reauth_0.0.0-test_linux_amd64.tar.gz \
  -C /tmp/sub2api-totp-staging/releases/test
ln -s releases/test /tmp/sub2api-totp-staging/current

STAGING_ROOT=/tmp/sub2api-totp-staging \
STAGING_API_URL=http://127.0.0.1:18083 \
STAGING_ADMIN_EMAIL=admin@example.test \
STAGING_ADMIN_PASSWORD='Staging-local-Only-938!' \
OPENAI_REAUTH_WORKER_TOKEN=local-staging-worker-token-00000000000000000000000 \
PYTHONDONTWRITEBYTECODE=1 \
python3 tools/reauth-runtime/test_deployment.py
```

脚本使用真实 API、PostgreSQL 迁移/任务状态、凭据加密和运行包恢复日志。上游登录、enroll/activate 与新旧密钥验证使用 mock，不调用真实 OpenAI MFA。覆盖成功换绑、导出不含密码、双份持久保存先于激活、激活响应丢失进入待确认、阻止重复换绑、重开日志后恢复已有候选且不重新 enroll。结果写到 staging 根目录的 `verification.json`。

CI `Re-login runtime` 在两种架构构建后，将 amd64 包部署到 Ubuntu 24.04，并用独立 PostgreSQL / Redis 执行同一脚本。该结果不能证明 OpenAI 非公开网页协议在真实账号上可用。

## 已有独立 Worker

主程序网页升级不会替换外部 systemd Worker。已设置 `OPENAI_REAUTH_WORKER_TOKEN` 的实例须同步升级独立 Worker 脚本及运行依赖，并配置绝对路径 `OPENAI_TOTP_JOURNAL_DIR`。沿用原恢复目录和加密 key；目录 0700、key/档案 0600。旧 Worker 或不可用的恢复存储不会报到 2FA 能力，API 将拒绝新任务，不能靠反复点击更换按钮解决。

Worker 切换前排空活跃任务，保留原 `current` 和配置，按部署流程执行预检、切换和失败回滚。运行包的修复不改变 MFA 协议、任务状态或已有恢复档案格式。

标准密码/TOTP sidecar 可用 [托管迁移工具](../../deploy/MANAGED_WORKER.md) 一次转入主程序自动管理。该迁移保留原 key/恢复记录并预校验当前版本运行包；以后主程序升级会自动选择配套 Worker。自定义邮箱协议继续使用外部模式。
