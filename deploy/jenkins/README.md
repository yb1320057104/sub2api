# Jenkins 私有打包与发布

本目录把 PK-017 的打包迁到 Jenkins。GitHub 继续保存私有源码、正式 Release 和 GHCR 镜像；GitHub Actions 不再执行打包或测试。正式更新包仍使用原名称、SHA256 校验和正式 Release 属性，后台网页更新路径保持不变。

本次 Jenkins 迁移不运行任何本地构建或测试。下文的本地验证步骤用于后续明确授权的任务；未来流程不能覆盖当次禁令，未执行的测试不得记为通过。

## 服务与任务

控制器不执行构建，任务固定运行在 `pk-builder`。构建 agent 使用专用 Docker daemon，`DOCKER_HOST` 必须是独立 daemon 的 TCP 地址；不挂载生产主机 Docker socket。Agent 与专用 daemon 必须以相同绝对路径共享 `/home/jenkins/agent`，因为编译容器只挂载任务的 `source` 子目录。

Jenkins 的 Pipeline from SCM 当前固定从私库已审阅的维护分支 `codex/jenkins-build` 读取根目录 `Jenkinsfile`，对应任务为 `sub2api-package`。本变更合入 `main` 后，再将任务的 SCM 分支切换到已审阅的 `main`。不能将流水线 SCM 分支改成用户选择的 `SOURCE_REF` 或填写的 `COMMIT_SHA`；这些参数只选择被打包的源码。有权修改任务或其可信 SCM 分支的人等同拥有该任务的发布权限。正式发行标签仍必须属于私库 `main` 历史，与流水线暂用维护分支无关。

需要 Git、Git Parameter、Pipeline、Credentials Binding 插件及 agent 的 Python 3.11+、Bash、Git、Docker CLI、gzip/tar。Node 24、pnpm 9.15.9 和 `backend/go.mod` 指定的 Go 工具链由隔离编译容器提供。编译容器不挂载凭据、流水线代码、历史产物、测试证据或生产数据，也不挂载 Docker socket。默认每个编译容器最多 2 CPU / 4 GiB，可通过 agent 的 `PK_BUILD_CPUS`、`PK_BUILD_MEMORY` 调整。

任务不运行 `go test`、Vitest 或应用程序。前端执行 `vue-tsc -b` 和 `vite build`，避免 `pnpm run build` 内置的 Vitest；相应翻译回归保留在本地测试套件。

## 当前服务器部署

2026-10-03 的安装目录为 `/opt/pk-jenkins`。基础设施源文件为本目录的 `compose.yaml`、`Dockerfile.controller`、`Dockerfile.agent`、`plugins.txt` 和 `bootstrap.groovy`，随私库保存。

| 组件 | 当前配置 |
|---|---|
| Jenkins controller | `2.580.1` / Java 21，执行器数为 0，禁止匿名访问 |
| Jenkins 界面语言 | 简体中文 `zh_CN`，Locale + 简体中文语言包，忽略浏览器语言偏好 |
| 管理入口 | 仅监听服务器 `127.0.0.1:18081`，不开放公网 Jenkins 端口 |
| 构建 agent | `pk-builder`，1 个执行器，通过 WebSocket 连接 controller |
| 专用 Docker daemon | `docker:29.8.1-dind`，内部 TLS `2376`，独立构建网络及 Docker 数据卷 |
| controller 持久化 | `/opt/pk-jenkins/controller` → `/var/jenkins_home` |
| agent 工作区 | `/opt/pk-jenkins/agent` → `/home/jenkins/agent`，与专用 daemon 共享同一路径 |
| 管理员凭据 | 用户名 `admin`；密码仅保存在服务器 `/opt/pk-jenkins/bootstrap/admin-password`，不进入仓库或日志 |

本次配置结果：`sub2api-package #3` 已成功，使用私库 `main` 的 `5273f30cb2ee5903c9c6acbd504755c7b0b0505c`，产物 ID 为 `3-5273f30cb2ee`（`0.2.12-pk.candidate.3`）。二进制归档、镜像归档、SHA256 和 manifest 已归档并保存到独立产物目录。没有创建正式发行版本。

界面语言配置保存到 controller 数据目录的 `locale.xml`，刷新和重启后保留；可在「系统管理 → 外观 → Locale」调整。`plugins.txt` 固定语言插件版本，`bootstrap.groovy` 为新安装配置同样默认值。部分第三方插件未提供中文翻译的文本仍会显示英文。

三项凭据均已配置。Release 令牌仅授权此私库的 Contents 读写；GHCR 令牌只有 `write:packages`（包含读取），没有 `repo` 权限。服务器端已核验私库文件读取、镜像包私有状态和 GHCR 认证。两项发布令牌均在北京时间 **2027-01-01** 到期；到期前应在原凭据 ID 下替换。认证核验不等于正式发布的完整动态验证。

用户配置反代时，上游填 `http://127.0.0.1:18081`，保留 Host、X-Forwarded-Proto/For 并启用 WebSocket。当前服务器的 OpenResty 使用 host 网络，可直接连接该地址。配置域名后，将 Jenkins「Manage Jenkins → System → Jenkins URL」改成最终 HTTPS 域名；不需要开放 `18081` 公网端口。

从本机通过 SSH 转发访问管理页，保持该 SSH 会话运行，然后打开 `http://127.0.0.1:18081`：

```sh
ssh -N -L 18081:127.0.0.1:18081 root@<服务器IP>
```

服务部署和候选打包不等于完整发布验证；配置发布凭据也不代表已通过本地验收。本次迁移遵守本地构建/测试禁令，没有生成本地完整测试证据；只有后续按当次明确授权完成所需验收并满足发布凭据要求，才能执行正式发布。

## 首次安装与后续维护

以下首次安装步骤只用于空的 Jenkins 环境；已有安装应使用下方维护步骤，保留账号、凭据、任务和数据。所有命令在服务器执行，不在本机进行基础设施或应用构建。

1. 从可信私库检出上述基础设施文件，将 Compose、两个 Dockerfile 和 `plugins.txt` 放到 `/opt/pk-jenkins`。构建目录的 `.dockerignore` 须只允许 Dockerfile 与 `plugins.txt` 进入构建上下文，排除 bootstrap、controller、agent 等数据目录。建立 controller、agent、bootstrap 和两个产物/证据目录；controller 与 agent 由容器用户 `1000:1000` 读写，证据目录由管理员写入、容器用户只读。
2. 在服务器安全配置 `bootstrap/admin-password` 和 `bootstrap/github-read`（仅此私库的只读 deploy key），文件设为 `1000:1000`、`0400`；bootstrap 目录须允许该用户遍历。把 `bootstrap.groovy` 安装到 `controller/init.groovy.d/`。不把真实凭据放入构建上下文或 Git。
3. 将已核对 GitHub 官方主机指纹的 `known_hosts` 分别安装到 `controller/.ssh/known_hosts` 和 `agent/.ssh/known_hosts`，目录 `0700`、文件 `0600`、属主 `1000:1000`。Compose 将 agent 的 `.ssh` 额外挂载到 `/var/jenkins_home/.ssh`，以匹配镜像中 SSH 使用的用户主目录；只放在 agent 工作目录而缺少此挂载会导致主机密钥验证失败。
4. 先启动 controller 和专用 daemon，等待首次初始化生成 `controller/.pk-agent-secret`，再由 root 安装 agent 所需的只读 secret，最后启动 agent：

```sh
cd /opt/pk-jenkins
docker compose -f compose.yaml build controller agent
docker compose -f compose.yaml up -d controller docker
# 确认 controller 初始化成功且以下源文件已生成，再执行：
test -s /opt/pk-jenkins/controller/.pk-agent-secret
install -o 1000 -g 1000 -m 0400 \
  /opt/pk-jenkins/controller/.pk-agent-secret \
  /opt/pk-jenkins/bootstrap/agent-secret
docker compose -f compose.yaml up -d agent
docker compose -f compose.yaml ps
```

确认管理页要求登录、controller 执行器为 0、`pk-builder` 在线，且任务从固定可信维护分支加载流水线。首次 bootstrap 通过 `controller/.pk-bootstrap-complete` 标记防止重复初始化；不要删除该标记来修改现有账号、凭据或任务。首次生成的 controller 内部 agent secret 和 provisioning token 同样按敏感文件保管。

首次配置完成后应撤销管理员的 `local-provisioning` 临时 API token 并删除 `.pk-provisioning-token` 明文文件；保留实际 agent 使用的 secret。临时本机管理隧道在收尾时关闭，不作为持久服务。

后续重建先等待当前 Jenkins 任务结束，备份 controller、bootstrap、产物仓库和本地测试证据，并保留专用 daemon 的命名数据卷及 TLS 证书卷。从私库复制已审阅的基础设施文件后，在服务器执行：

```sh
cd /opt/pk-jenkins
docker compose -f compose.yaml build controller agent
docker compose -f compose.yaml up -d --no-deps controller agent
docker compose -f compose.yaml ps
```

需要升级专用 daemon 时，另行核对 `compose.yaml` 中的 Docker 版本并在无构建运行时更新 `docker` 服务，随后确认 agent 的 TLS 连接恢复。普通重建不重新初始化 Jenkins、不重建 deploy key，也不复制新的管理员密码覆盖现有账号。若更换了 controller 数据导致 agent 身份重新生成，按首次安装中的 root 安装步骤更新 `bootstrap/agent-secret` 后重建 agent。不要执行 `down -v` 或删除 controller/产物目录作为升级步骤；这些操作会破坏可恢复状态。本 Compose 只管理 Jenkins 基础设施，不能替代或操作生产应用的 Compose。

## 凭据和目录

| Jenkins 凭据 ID | 类型 | 用途 |
|---|---|---|
| `pk-github-read` | SSH username/private key，用户名 `git` | 仅此私库的只读 deploy key，加载流水线和目标源码 |
| `pk-github-publish` | Secret text | 仅此私库 `Contents: write` 的发布令牌；仅 publish 阶段读取 |
| `pk-ghcr-publish` | Username/password | 用户名与 GHCR 包读写令牌；仅 publish 阶段读取 |

发布凭据未配置不影响 package。不要用生产服务器密码或开发者宽权限令牌替代它们。GitHub SSH 主机密钥按上述步骤在 controller 和 agent 两侧预先核验和配置。

- `/srv/jenkins-packages/sub2api`：只由 Jenkins 控制脚本写入的产物仓库，seal 后文件只读，按 `构建号-提交前12位` 存放，现有 ID 不覆盖。
- `/srv/jenkins-evidence/sub2api`：由管理员上传本地测试结果，挂载到 agent 时只读。命名为 `{package_id}.json`，编译容器不可见。

证据 JSON 是可信本地验证记录，不是密码学签名。只有获准执行本地验证和上传证据的人可以写此目录。历史产物保留到管理员按实际发布/回退需要清理；Jenkins 自身保留最近 30 次记录、10 次界面产物，不会自动删除独立产物仓库。

## 打包、验证和发布

1. 选择 `ACTION=package`，在 `SOURCE_REF` 的可搜索下拉列表中选择分支或标签。该列表使用 Git Parameter 的 `PT_BRANCH_TAG`，默认 `origin/main`；分支显示为 `origin/分支名`，正式 PK 标签显示为 `vX.Y.Z-pk.N`，无需手填完整 ref。日常将 `COMMIT_SHA` 留空；仅需构建固定提交时，填写 40 位小写提交 SHA，它会覆盖下拉选择并生成 `candidate`。分支也生成 `candidate`；正式 PK 标签须位于私库 `main` 历史，生成 `BuildType=release` 的待验证包。流水线将分支/标签选择规范化为 `refs/heads/...` / `refs/tags/...` 后交给现有校验，标签打包本身不发布。
2. Jenkins 显示 `PACKAGE_ID` 并归档 `output/`。下载此目录的文件：`manifest.json`、二进制 `.tar.gz`、Docker `.docker.tar.gz`、`checksums.txt`。Docker 镜像和更新归档来自同一次 Go 编译。
3. 后续获准执行本地验证时，在本地电脑对同一 commit/tree 运行 `scripts/test-local.ps1` 或 `scripts/test-local.py`，将上述目录作为 `--artifact-dir`。本地执行源码完整套件和原镜像持久化验收；测试后清理临时文件和容器。
4. 将通过的证据 JSON 上传为 `/srv/jenkins-evidence/sub2api/{package_id}.json`。JSON 须使用 `schema_version=1`、`tests_profile=pk-local-v1`、完整 commit/tree，并包含 `backend-unit`、`backend-integration`、`backend-race`、`backend-lint`、`frontend`、`shell`、`binance-proxy`、`release-helpers`、`security`、`artifact-runtime` 全部通过的记录。`artifact` 的 `package_id`、`binary_sha256`、`image_archive_sha256` 必须匹配。
5. 选择 `ACTION=publish`，填写已有 `PACKAGE_ID`。系统复核产物校验、本地证据、私库状态、标签当前指向、main 祖先关系、未占用 Release/镜像版本和 GHCR 私有状态，然后发布原封不动的已测试镜像及二进制包，不重新编译。
6. Release 先以 draft 上传资产，全部成功后才转为正式、非 prerelease、latest；随后推送同一镜像的 `latest`。正式 Release 保留 `sub2api_版本_linux_amd64.tar.gz` 与 `checksums.txt`，现有网页更新器可直接使用。发布不连接、重启或升级生产应用。

若镜像已推送但 Release 上传失败，或留下草稿，流水线会停止，不覆盖已占用版本。由管理员核查不完整版本，保留审计记录后选择新 PK 修订；不要盲目重试并覆盖已有制品。若正式 Release 已成功而 `latest` 镜像推送失败，版本镜像及网页更新包仍存在，应核查本次日志再修复移动标签。

首轮只配置 package 也可以完整验证 Jenkins 编译路径。缺少专用发布令牌或本地同产物验收时，不能宣称正式发布功能已完成动态验证。
