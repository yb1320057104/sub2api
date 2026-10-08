# Jenkins 私有打包与发布

本目录把 PK-017 的打包迁到 Jenkins。GitHub 继续保存私有源码、正式 Release 和 GHCR 镜像；GitHub Actions 不再执行打包或测试。正式更新包仍使用原名称、SHA256 校验和正式 Release 属性，后台网页更新路径保持不变。

2026-10-08 用户最新授权取代早前本次测试禁令：先完整源码测试通过，再提交和推送；源码验证默认在用户指定服务器的隔离环境执行，本机 Linux/WSL 仍可用。Jenkins 默认 `ACTION=release` 一次完成正式打包、制品运行时验收和私库发布。本文描述目标流程，实际是否通过以报告为准，不能把服务器验证写成本机通过。环境不可用或失败时不提交、合入或发布。

## 服务与任务

控制器不执行构建，任务固定运行在 `pk-builder`。构建 agent 使用专用 Docker daemon，`DOCKER_HOST` 必须是独立 daemon 的 TCP 地址；不挂载生产主机 Docker socket。Agent 与专用 daemon 必须以相同绝对路径共享 `/home/jenkins/agent`，因为编译容器只挂载任务的 `source` 子目录。

Jenkins 的 Pipeline from SCM 当前固定从私库已审阅的维护分支 `codex/jenkins-build` 读取根目录 `Jenkinsfile`，对应任务为 `sub2api-package`。本变更合入 `main` 后，再将任务的 SCM 分支切换到已审阅的 `main`。不能将流水线 SCM 分支改成用户选择的 `SOURCE_REF` 或填写的 `COMMIT_SHA`；这些参数只选择被打包的源码。有权修改任务或其可信 SCM 分支的人等同拥有该任务的发布权限。正式发行标签仍必须属于私库 `main` 历史，与流水线暂用维护分支无关。

后续新工作分支按项目约束第 6 节统一为 `codex/vX.Y.Z-pk.N`，同一未发布版本复用一个分支与 PR。Jenkins 的 `SOURCE_REF` 可选择对应的 `origin/codex/vX.Y.Z-pk.N` 来打候选包；选择版本命名的分支仍是候选，只有正式标签才走标签包校验。当前 `codex/jenkins-build` 是既有迁移引用，切换完成前不得为整理分支而删改它；不因一次打包或重试新建分支。

需要 Git、Git Parameter、Pipeline、Credentials Binding 插件及 agent 的 Python 3.11+、Bash、Git、Docker CLI、gzip/tar。Node 24、pnpm 9.15.9 和 `backend/go.mod` 指定的 Go 工具链由隔离编译容器提供。编译容器不挂载凭据、流水线代码、历史产物、测试证据或生产数据，也不挂载 Docker socket。默认每个编译容器最多 2 CPU / 4 GiB，可通过 agent 的 `PK_BUILD_CPUS`、`PK_BUILD_MEMORY` 调整。

源码 `go test`、Vitest、lint 和安全审计在提交前的独立验证环境完成，默认是用户指定服务器的受控临时容器，不连接生产数据/服务。Jenkins 编译阶段运行 `vue-tsc -b`、`vite build` 和 Go 发行构建，不重复执行 `pnpm run build` 内置的 Vitest。默认 release 随后在专用 daemon 隔离容器执行同制品运行时检查；容器不连接生产、不挂载源码或发布凭据。该检查使用固定可信 pipeline checkout 的 `verify-runtime.py`，不能执行参数所选源码中的替代验收脚本。

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

上述 2026-10-03 的服务部署、候选打包和凭据核验仅是历史结果，不等于新流程通过。2026-10-08 因本机缺 WSL Linux 发行版和 Docker，用户指定服务器隔离验证；是否完成以本次真实报告为准。源码验证、提交及 notes 推送完成后，再按授权更新可信 SCM 并验收，不能把配置完成称为测试通过。

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
| `pk-github-read` | SSH username/private key，用户名 `git` | 仅此私库的只读 deploy key，加载流水线、目标源码及源码验收 notes |
| `pk-github-publish` | Secret text | 仅此私库 `Contents: write` 的发布令牌；用于发布前私库检查及正式发布，不传入编译/运行时容器 |
| `pk-ghcr-publish` | Username/password | 用户名与 GHCR 包读写令牌；用于镜像发布，不传入编译/运行时容器 |

发布凭据未配置不影响 package。不要用生产服务器密码或开发者宽权限令牌替代它们。GitHub SSH 主机密钥按上述步骤在 controller 和 agent 两侧预先核验和配置。

- `/srv/jenkins-packages/sub2api`：只由 Jenkins 控制脚本写入，seal 后文件只读，按 `构建号-提交前12位` 存放，现有 ID 不覆盖。默认 release 同包保全源码报告、notes 出处、运行时报告及制品校验值。
- `refs/notes/pk-source-validation`：私库中的源码验收记录，由源码验证入口绑定最终 commit 后发布；Jenkins 使用只读 deploy key 显式 fetch，不从另一提交凭 tree 相同推断通过。
- `/srv/jenkins-evidence/sub2api`：仅旧手动流程由管理员上传 `pk-local-v1` 同包报告，命名 `{package_id}.json`；agent 只读，编译/运行时容器不可见。

证据 JSON 和 Git notes 是可信验证记录，不是密码学签名；源码报告带日志 SHA256，但仍依赖私库写入权限及原始日志保管。只有获准执行验证的人发布 notes 或上传旧报告。历史产物保留到管理员按实际发布/回退需要清理；Jenkins 自身保留最近 30 次记录、10 次界面产物，不会自动删除独立产物仓库。

## 打包、验证和发布

1. **提交前源码验证。** 完成代码和 PK 清单核对，检查范围后将待测源码全部暂存，无未暂存改动/非忽略未跟踪文件。默认在用户指定服务器受控隔离环境运行完整源码套件，或选择本机 `scripts/test-local.ps1`（WSL）/`python3 scripts/test-local.py`。`local-validation.json` 须为 `schema_version=2`、`tests_profile=pk-source-v2`，preflight 与九套件全部通过，`source_eligible=true`；前后 tree/快照一致，保留原日志及真实执行环境。不完整诊断或把服务器结果标作本机通过均不合格。
2. **提交和绑定。** 测试通过后提交相同 tree，在干净最终提交上运行 `python3 scripts/test-local.py --bind-commit HEAD --report /path/to/local-validation.json --publish-evidence`；Windows 控制端可直接用 `python -B scripts/test-local.py --bind-commit HEAD --report ... --publish-evidence`，无需 WSL；若使用 WSL 包装入口，其对应参数为 `-BindCommit HEAD -Report ... -PublishEvidence`。代码和 `refs/notes/pk-source-validation` 都须推送私库。合入/正式 tag 指向的最终 commit 必须有自己的绑定报告；新 commit 重新绑定，tree 改变则重新测试，不能仅复制其他 commit 的 note。
3. **一次正式 release。** 默认 `ACTION=release`，`SOURCE_REF` 选择正式 `vX.Y.Z-pk.N` 标签（规范化为 `refs/tags/...`），`COMMIT_SHA`、`PACKAGE_ID` 留空。仅支持属于私库 main 历史、未占用版本的正式 tag；分支/裸 SHA 或 COMMIT_SHA 覆盖直接拒绝。源码选择不改变可信流水线 SCM。
4. **源码门禁和构建。** Jenkins 显式 fetch notes，校验最终 commit/tree、base 祖先、schema/profile 和九套件，保全 `source-validation.json`、报告 SHA256 与 notes 出处。通过后执行一次 build，镜像与更新归档使用同一二进制；inventory 计算制品清单和哈希。
5. **同制品运行时验收。** 专用 daemon 执行 `pk-artifact-v2` 十一项检查，覆盖归档/镜像校验、同一二进制、非 root 执行与可写卷、持久化程序、同种子保留网页更新、backup 恢复、换种子和恢复原镜像等。容器无生产数据及发布凭据；仅生成可清理的隔离夹具。报告绑定本次 package_id、commit/tree 和归档/二进制/镜像哈希，失败不发布。
6. **封存并发布原包。** 完整顺序为 build → inventory → runtime → seal → publish；Jenkins 封存制品及证据，再检查私库状态、tag 指向、main 归属和版本未占用，发布同一镜像/归档，不重编译。Release 先以 draft 上传资产，全部成功后转为正式、非 prerelease、latest，再推同一镜像的 `latest`。保留 `sub2api_版本_linux_amd64.tar.gz` 和 `checksums.txt`，网页更新器可直接发现该正式版本；发布不连接、重启或升级生产应用。

### 兼容手动 package / publish

显式 `ACTION=package` 保留原打包行为与六项基础制品，不自动发布：Git Parameter `PT_BRANCH_TAG` 可选择 `origin/分支名` 或正式 `vX.Y.Z-pk.N`，40 位小写 `COMMIT_SHA` 只在该动作覆盖选择并生成候选。分支/SHA 候选不能转为正式发布；正式标签须在 main 历史内，生成待验收的 release 包。记录 `PACKAGE_ID`，下载同次 `manifest.json`、二进制/镜像归档、校验文件等。

该手动包仍使用旧验收：在干净的相同 commit/tree 运行 `-ArtifactDirectory` / `--artifact-dir`，得到 `schema_version=1`、`tests_profile=pk-local-v1` 的十套件同包报告。九个源码套件加 `artifact-runtime` 全部通过，`publish_eligible=true`，package_id、binary_sha256、image_archive_sha256 匹配后，上传 `/srv/jenkins-evidence/sub2api/{package_id}.json`。随后 `ACTION=publish` 指定该 PACKAGE_ID，复核并发布原包。新 release 流程已通过源码与运行时验收、封存后尚未对外发布的包也可用 publish；恢复时读取同包保全证据，不凭可变 notes 代替已验收产物。

若镜像已推送但 Release 上传失败，或留下草稿，流水线会停止，不覆盖已占用版本。由管理员核查不完整版本，保留审计记录后选择新 PK 修订；不要盲目重试并覆盖已有制品。若正式 Release 已成功而 `latest` 镜像推送失败，版本镜像及网页更新包仍存在，应核查本次日志再修复移动标签。

只要求试打包时使用 package，成功仅证明编译路径。缺源码证据、运行时验收或专用发布凭据时，不得称为正式发布通过；部分对外发布失败后的版本阻挡须先核查，publish 不承诺自动续传或覆盖已有版本。
