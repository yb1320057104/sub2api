# Jenkins 私有打包与发布

本目录把 PK-017 的打包迁到 Jenkins。GitHub 继续保存私有源码、正式 Release 和 GHCR 镜像；GitHub Actions 不再执行打包或测试。正式更新包仍使用原名称、SHA256 校验和正式 Release 属性，后台网页更新路径保持不变。

## 服务与任务

控制器不执行构建，任务固定运行在 `pk-builder`。构建 agent 使用专用 Docker daemon，`DOCKER_HOST` 必须是独立 daemon 的 TCP 地址；不挂载生产主机 Docker socket。Agent 与专用 daemon 必须以相同绝对路径共享 `/home/jenkins/agent`，因为编译容器只挂载任务的 `source` 子目录。

Jenkins 的 Pipeline from SCM 固定从私库已审阅的 `main` 读取根目录 `Jenkinsfile`。不能将任务的流水线 SCM 分支改成用户输入的 `SOURCE_REF`。有权修改任务或私库 `main` 的人等同拥有该任务的发布权限。

需要 Git、Pipeline、Credentials Binding 插件及 agent 的 Python 3.11+、Bash、Git、Docker CLI、gzip/tar。Node 24、pnpm 9.15.9 和 `backend/go.mod` 指定的 Go 工具链由隔离编译容器提供。编译容器不挂载凭据、流水线代码、历史产物、测试证据或生产数据，也不挂载 Docker socket。默认每个编译容器最多 2 CPU / 4 GiB，可通过 agent 的 `PK_BUILD_CPUS`、`PK_BUILD_MEMORY` 调整。

任务不运行 `go test`、Vitest 或应用程序。前端执行 `vue-tsc -b` 和 `vite build`，避免 `pnpm run build` 内置的 Vitest；相应翻译回归保留在本地测试套件。

## 凭据和目录

| Jenkins 凭据 ID | 类型 | 用途 |
|---|---|---|
| `pk-github-read` | SSH username/private key，用户名 `git` | 仅此私库的只读 deploy key，加载流水线和目标源码 |
| `pk-github-publish` | Secret text | 仅此私库 `Contents: write` 的发布令牌；仅 publish 阶段读取 |
| `pk-ghcr-publish` | Username/password | 用户名与 GHCR 包读写令牌；仅 publish 阶段读取 |

发布凭据未配置不影响 package。不要用生产服务器密码或开发者宽权限令牌替代它们。GitHub SSH 主机密钥在 agent 的 known_hosts 中预先核验和配置。

- `/srv/jenkins-packages/sub2api`：只由 Jenkins 控制脚本写入的产物仓库，seal 后文件只读，按 `构建号-提交前12位` 存放，现有 ID 不覆盖。
- `/srv/jenkins-evidence/sub2api`：由管理员上传本地测试结果，挂载到 agent 时只读。命名为 `{package_id}.json`，编译容器不可见。

证据 JSON 是可信本地验证记录，不是密码学签名。只有获准执行本地验证和上传证据的人可以写此目录。历史产物保留到管理员按实际发布/回退需要清理；Jenkins 自身保留最近 30 次记录、10 次界面产物，不会自动删除独立产物仓库。

## 打包、验证和发布

1. 选择 `ACTION=package`。`SOURCE_REF` 填完整 `refs/heads/...`、40 位提交 SHA 或 `refs/tags/vX.Y.Z-pk.N`。分支/SHA生成 `candidate`；正式 PK 标签须位于私库 `main` 历史，生成 `BuildType=release` 的待验证包。标签打包本身不发布。
2. Jenkins 显示 `PACKAGE_ID` 并归档 `output/`。下载此目录的文件：`manifest.json`、二进制 `.tar.gz`、Docker `.docker.tar.gz`、`checksums.txt`。Docker 镜像和更新归档来自同一次 Go 编译。
3. 在本地电脑对同一 commit/tree 运行 `scripts/test-local.ps1` 或 `scripts/test-local.py`，将上述目录作为 `--artifact-dir`。本地执行源码完整套件和原镜像持久化验收；测试后清理临时文件和容器。
4. 将通过的证据 JSON 上传为 `/srv/jenkins-evidence/sub2api/{package_id}.json`。JSON 须使用 `schema_version=1`、`tests_profile=pk-local-v1`、完整 commit/tree，并包含 `backend-unit`、`backend-integration`、`backend-race`、`backend-lint`、`frontend`、`shell`、`binance-proxy`、`release-helpers`、`security`、`artifact-runtime` 全部通过的记录。`artifact` 的 `package_id`、`binary_sha256`、`image_archive_sha256` 必须匹配。
5. 选择 `ACTION=publish`，填写已有 `PACKAGE_ID`。系统复核产物校验、本地证据、私库状态、标签当前指向、main 祖先关系、未占用 Release/镜像版本和 GHCR 私有状态，然后发布原封不动的已测试镜像及二进制包，不重新编译。
6. Release 先以 draft 上传资产，全部成功后才转为正式、非 prerelease、latest；随后推送同一镜像的 `latest`。正式 Release 保留 `sub2api_版本_linux_amd64.tar.gz` 与 `checksums.txt`，现有网页更新器可直接使用。发布不连接、重启或升级生产应用。

若镜像已推送但 Release 上传失败，或留下草稿，流水线会停止，不覆盖已占用版本。由管理员核查不完整版本，保留审计记录后选择新 PK 修订；不要盲目重试并覆盖已有制品。若正式 Release 已成功而 `latest` 镜像推送失败，版本镜像及网页更新包仍存在，应核查本次日志再修复移动标签。

首轮只配置 package 也可以完整验证 Jenkins 编译路径。缺少专用发布令牌或本地同产物验收时，不能宣称正式发布功能已完成动态验证。
