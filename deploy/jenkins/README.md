# Jenkins 私有打包与发布

本目录把 PK-017 的打包迁到 Jenkins。GitHub 继续保存私有源码、正式 Release 和 GHCR 镜像；GitHub Actions 不再执行打包或测试。正式更新包仍使用原名称、SHA256 校验和正式 Release 属性，后台网页更新路径保持不变。

2026-10-21 用户最新授权取消全套源码测试和源码 notes 对提交、合入、正式标签交接及新 release 的前置，取代 2026-10-08/19 的对应要求。默认完成保护功能和差异核对后，直接提交、推送 main 与唯一正式标签，由用户点击 `ACTION=release`；Jenkins 一次完成正式构建、十一项同制品运行时验收和私库发布。不自动运行全套源码测试，未测不写成通过，发布不自动上线。

## 服务与任务

控制器不执行构建，任务固定运行在 `pk-builder`。构建 agent 使用专用 Docker daemon，`DOCKER_HOST` 必须是独立 daemon 的 TCP 地址；不挂载生产主机 Docker socket。Agent 与专用 daemon 必须以相同绝对路径共享 `/home/jenkins/agent`，因为编译容器只挂载任务的 `source` 子目录。

Jenkins 的 Pipeline from SCM 已在 2026-10-08 合入后切换到私库已审阅的 `main`，读取根目录 `Jenkinsfile`，对应任务为 `sub2api-package`。历史维护分支不再作为活动依赖，初始化配置同样使用 `main`。不能将流水线 SCM 分支改成用户选择的 `SOURCE_REF` 或填写的 `COMMIT_SHA`；这些参数只选择被打包的源码。有权修改任务或其可信 SCM 分支的人等同拥有该任务的发布权限。正式发行标签仍必须属于私库 `main` 历史。

后续工作分支按项目约束第 6 节统一为不带任何附加前缀的 `vX.Y.Z-pk.N`，同一未发布版本复用一个分支与 PR。Jenkins 的 `SOURCE_REF` 选择 `origin/vX.Y.Z-pk.N` 来打候选包；版本分支仍是候选，只有正式标签才走标签包校验。不因打包、修复或重试新建前缀分支或重复版本分支。

2026-10-21 用户要求工作分支不带任何附加前缀，当前目标为 `v0.2.17-pk.4`，继承 pk.2、pk.3 全部已有功能。`PT_BRANCH_TAG` 继续显示候选分支和正式标签：`origin/v0.2.17-pk.4` 是分支，裸 `v0.2.17-pk.4` 是正式标签。参数默认不选来源（`defaultValue=''`、`selectedValue=NONE`），release/package 须显式选择，publish 不使用来源参数；正式 release 选择裸标签，`COMMIT_SHA`、`PACKAGE_ID` 留空。不要通过改名分支隐藏 `origin/` 来冒充正式标签。

需要 Git、Git Parameter、Pipeline、Credentials Binding 插件及 agent 的 Python 3.11+、Bash、Git、Docker CLI、gzip/tar。Node 24、pnpm 9.15.9 和 `backend/go.mod` 指定的 Go 工具链由隔离编译容器提供。编译容器不挂载凭据、流水线代码、历史产物、测试证据或生产数据，也不挂载 Docker socket。默认每个编译容器最多 2 CPU / 4 GiB，可通过 agent 的 `PK_BUILD_CPUS`、`PK_BUILD_MEMORY` 调整。

源码 `go test`、Vitest、lint 和安全审计仅按用户要求或实际排查需要执行，不是新正式包的前置，不自动启动全套。可选检查使用隔离环境，不连接生产数据/服务，真实结果见原报告。Jenkins 编译阶段仍运行 `vue-tsc -b`、`vite build` 和 Go 发行构建，不执行 `pnpm run build` 内置的 Vitest；这些必需编译步骤失败仍使构建失败。release 随后使用固定可信 pipeline checkout 的 `verify-runtime.py` 在专用 daemon 验收同一制品，容器不连接生产、不挂载源码或发布凭据，不能由参数所选源码替换验收脚本。

## 依赖与编译缓存

`package.sh` 将 npm/npx 缓存、pnpm 内容存储、Go module 缓存和 Go 编译缓存分别挂载为专用 Docker daemon 的命名卷。此前这些内容位于 `--rm` 容器的 `/tmp`，每次容器删除后都要重新下载；Docker 镜像层本来就由专用 daemon 的 `docker-data` 卷保留，与语言依赖缓存不同。

缓存名称以 `pk-sub2api-build-cache-v1-` 开头，区分 `release`（正式标签与同制品验收流程）、`manual-release`（兼容手动正式标签包）和 `candidate`（分支/SHA 候选）。三个域不共享缓存；名称还包含平台、UID/GID、Node/pnpm 或准确 Go 工具链版本。只接受带匹配用途/域/名称标签、无挂载选项的本地卷，不允许源码指定任意路径或外部卷。

首次缓存为空时仍需下载；以后未变化的包和 Go 编译结果可以复用，新增依赖仍会下载，代码或构建参数变化仍会触发必要编译。每次 checkout 和项目 `node_modules` 保持独立，不缓存发行包或测试结论。pnpm 保留 frozen lockfile 和存储完整性检查，使用复制方式导入并禁用 native/postinstall 结果缓存；Go 下载后执行 `go mod verify`，发行编译使用 `-mod=readonly`。缓存不能代替依赖完整性、制品验收或 SHA256 校验，也不是源码测试通过的证据。

初始化仅用无网络、只读根文件系统且仅有 CHOWN 能力的辅助容器修改四个卷根目录的属主，不递归遍历缓存；该容器不挂源码、凭据或 Docker socket。编译进程继续以非 root 用户运行。命名缓存卷不随工作区清理或 `--rm` 删除；工具链升级会使用不同卷，旧卷按实际需要在无构建运行时逐个检查、清理，不能用全局 `docker system prune --volumes` 代替定向维护。容器、证书、产物及生产卷不属于依赖缓存。

打包结束只恢复 HEAD 确实跟踪的 `backend/internal/web/dist/.keep`。没有该占位文件的源码可以正常打包；真正的 Git 恢复失败仍会终止。`seal` 继续检查完整源码工作区，不能借缓存或清理步骤掩盖其他源码修改。

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

上述 2026-10-03 的服务部署、候选打包和凭据核验仅是历史结果。2026-10-08 用户因本机缺 WSL Linux 发行版和 Docker 改用服务器隔离源码验证；该历史流程的源码前置已于 2026-10-21 取消。后续按当前流程审阅并提交可信 SCM，由用户启动构建和制品验收；配置完成不等于测试或发布通过。

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
| `pk-github-read` | SSH username/private key，用户名 `git` | 仅此私库的只读 deploy key，加载可信流水线与目标源码；源码 notes 仅作可选历史证据 |
| `pk-github-publish` | Secret text | 仅此私库 `Contents: write` 的发布令牌；用于发布前私库检查及正式发布，不传入编译/运行时容器 |
| `pk-ghcr-publish` | Username/password | 用户名与 GHCR 包读写令牌；用于镜像发布，不传入编译/运行时容器 |

发布凭据未配置不影响 package。不要用生产服务器密码或开发者宽权限令牌替代它们。GitHub SSH 主机密钥按上述步骤在 controller 和 agent 两侧预先核验和配置。

- `/srv/jenkins-packages/sub2api`：只由 Jenkins 控制脚本写入，seal 后文件只读，按 `构建号-提交前12位` 存放，现有 ID 不覆盖。新 release 同包保全运行时报告及制品校验值；旧包按原 profile 继续保全所需源码报告与 notes 出处。
- `refs/notes/pk-source-validation`：私库中的可选源码验收记录，由源码检查入口按真实 commit/tree 绑定后发布；新 release 不要求 fetch 或提供该引用。已有报告不能挪作另一 tree 的通过证据，旧封存包的源码证据按原 profile 继续核验。
- `/srv/jenkins-evidence/sub2api`：仅旧手动流程由管理员上传 `pk-local-v1` 同包报告，命名 `{package_id}.json`；agent 只读，编译/运行时容器不可见。

证据 JSON 和 Git notes 是可信验证记录，不是密码学签名；源码报告带日志 SHA256，但仍依赖私库写入权限及原始日志保管。只有获准执行验证的人发布 notes 或上传旧报告。历史产物保留到管理员按实际发布/回退需要清理；Jenkins 自身保留最近 30 次记录、10 次界面产物，不会自动删除独立产物仓库。

## 打包、验收和发布

### 交给用户正式构建前（2026-10-21 最新授权）

先完成保护功能、权限和最终差异核对，同步二开清单，直接提交、合入并推送私库 main 与唯一正式 `vX.Y.Z-pk.N` 标签。源码全套测试及 notes 不再是前置，不因缺少它们停止交接；实际检查范围和结果仍须如实说明。

- 核对私库 main 已包含目标提交，保留 pk.2、pk.3 已有功能和历史；一个目标版本只使用一个分支和一个 PR。
- 核对正式标签、Release 和镜像版本未被占用，再创建并推送指向已审阅提交的正式标签。已推送标签绝不移动、重建或覆盖；只推工作分支不算正式交接。
- 从远端核验 main 和标签剥离后的 commit，确认目标提交属于 main 历史。附注标签对象 SHA 不是 commit；同名分支/标签使用完整 refs 区分。
- 交付 `ACTION=release`、`SOURCE_REF=vX.Y.Z-pk.N`，`COMMIT_SHA`/`PACKAGE_ID` 留空。用户自行点击构建，助手不默认触发 Jenkins 或上线。列表缺少裸标签时先检查远端并刷新参数页，保持可信 SCM 为 main。

**历史示例（2026-10-19）：** 正式标签 `v0.2.17-pk.1` 剥离后为 `0557923542d291ffd4c4e5fc25487777ef3918aa`，与当时 main 一致；当时按旧流程完成源码检查和 notes。该记录不构成新版本的源码前置，不改写已有标签或旧报告。

### 新正式包执行流程

1. **显式选择正式标签。** `ACTION=release` 只接受 main 历史内、版本未占用的 `refs/tags/vX.Y.Z-pk.N`；裸版本参数会规范化为该引用。分支/SHA 和 `COMMIT_SHA` 覆盖均拒绝，不改变可信流水线 SCM。
2. **记录来源并构建。** 新包使用 `validation_profile=pk-artifact-only-v1`，不要求源码报告或 notes。一次 build 的同一二进制进入镜像与更新归档，inventory 记录 commit/tree、package_id 与归档、二进制和镜像哈希。
3. **同制品运行时验收。** 专用 daemon 执行 `pk-artifact-v2` 十一项检查，覆盖完整性、镜像来源、二进制身份、非 root 与可写卷、同种子保留、backup 恢复、种子切换、原镜像恢复和夹具清理。报告必须绑定本次 package_id、commit/tree 及制品哈希；任何必需项失败、缺失或绑定不符均不能发布。
4. **封存并发布原包。** 顺序仍为 build → inventory → runtime → seal → publish。封存制品及报告，再核对私库/GHCR 私有性、tag 指向、main 归属、版本未占用与 SHA256，发布同一镜像和归档，不重编译。Release 先以 draft 上传，全部成功后转为正式、非 prerelease、latest，再更新同一镜像的 latest。
5. **按授权更新。** Release 必须包含 `sub2api_版本_linux_amd64.tar.gz` 和 `checksums.txt`，网页更新器随后可发现该正式版本。Jenkins 不连接、重启或升级生产应用；实际网页更新按用户授权执行。

### 可选源码检查

仅在用户要求或实际排查需要时运行源码检查。完整入口为 `scripts/test-server.py`，可选本机 Linux/WSL 入口为 `scripts/test-local.py`/`.ps1`；环境、隔离、报告和清理细节见 [维护文档](../../PK_MAINTENANCE.md#可选源码检查与历史证据)。未执行如实写“未执行”，针对性检查不冒称完整九套件通过。

若选择生成完整 `pk-source-v2` 通过报告或发布 notes，仍须满足原报告的九套件/preflight、准确 commit/tree、原始日志 SHA256、真实环境和禁止覆盖规则。新正式包不等待该报告，也不从其他提交的报告推断源码已通过；旧封存包的证据要求如下。

### 兼容手动 package / publish 与旧包

显式 `ACTION=package` 保留原打包行为与六项基础制品，不自动发布。`PT_BRANCH_TAG` 可选 `origin/分支名` 或正式裸标签，40 位小写 `COMMIT_SHA` 仅在 package 覆盖选择。分支/SHA 候选不能转为正式发布；正式标签包仍须属于 main 历史。记录 package_id 并保留同次 manifest、归档与校验文件。

`ACTION=publish` 按封存 manifest 的原 profile 验证，不能删除旧证据或改 profile 来套用较少要求：

| 封存包 profile | 必需证据 |
|---|---|
| `pk-artifact-only-v1` | 同包 `pk-artifact-v2` 十一项通过报告、准确来源和全部制品哈希；不要求源码 notes |
| `pk-source-artifact-v2` | 原先封存的源码报告、notes 出处和同包运行时报告全部有效，commit/tree、package_id、哈希一致 |
| 旧手动路径（`pk-local-v1` 报告） | 原九个源码套件加 artifact-runtime 的十套件报告，`publish_eligible=true`，package_id、二进制/镜像哈希一致 |

旧手动报告由 `-ArtifactDirectory` / `--artifact-dir` 入口产生并上传 `/srv/jenkins-evidence/sub2api/{package_id}.json`；agent 只读。新旧包均只发布已验收原制品，恢复时读取原封存证据，不用可变 notes 替换同包报告。

若镜像已推送但 Release 上传失败或留下草稿，先核查不完整版本和日志，保留审计记录，不盲目覆盖重发。正式 Release 已成功而镜像 latest 更新失败时，先核实原制品后处理移动标签，不能重新发布已占用的正式版本。

package 成功只说明打包结果；缺少对应 profile 必需的运行时/历史证据或专用发布凭据时，不得称为正式发布通过。新流程缺少源码报告本身不阻塞发布，publish 也不承诺自动续传或覆盖已有版本。
