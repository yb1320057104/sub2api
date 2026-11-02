# v0.2.19-pk.42 私库来源迁移

用户在本轮糖果分组/授权配置任务中追加要求：将正式仓库改为 `https://github.com/yb1320057104/sub2api.git`，生产版本更新也使用该仓库。功能修改继续保留，版本仍为唯一在途 `v0.2.19-pk.42`。

## 已核对的迁移边界

- 新仓库 API 返回 private=true，初始 size=0、无分支/标签；现有 GitHub 凭据对新仓库有管理及推送权限。
- 本地 `origin` 指向新库；旧仓库保留为 `previous-origin`。旧远端权威标签先读取到独立引用，复制时保留原Git对象，避免本地历史标签差异造成覆盖或重标。
- 已原样迁入旧远端 52 个分支、81 个标签和 1 个历史验证 notes 引用，读取新远端逐项比对对象 ID 全部一致；未修改旧仓库引用。本轮新代码和标签另行提交。
- 现行源码、Release及镜像统一新命名空间。上游原作者仅继续作为合并输入，Go模块路径、作者署名及历史审查记录不重写。
- 旧正式标签和封存制品的原repository、profile、哈希与报告保留；新库内迁入Git标签不等于迁入Release资产，也不等于该旧制品由新仓库构建。

## 源码及流水线

- `update_service.go`固定新库，版本检测、在线回退、缓存repository校验和GitHub资源URL允许范围随同切换；不开放任意更新源，不放宽认证重定向或下载校验。
- Jenkinsfile、Job初始化模板、controller工具、镜像source label、Compose和安装器指向新库。新发行仍严格检查私有仓库、正式标签与main归属、未占用版本、同一二进制及十一项制品运行时验收。
- `repository_policy.py`只允许固定当前源与显式旧封存验证。`prepare`、新制品封存及正式发布强制当前源；旧包读取/验证按原manifest来源核对镜像label、profile和完整证据，不能改identity后发布到新库。
- 前端版本操作指引和现行维护文档同步新库；历史运行证据保留真实旧源。
- 新GHCR包首次API查询为404。发行流程继续要求包已存在且private，不能为首次发行直接绕过该门禁。当前凭据不具备 packages scope，本次不向 GHCR 发布；改为私库 Release 同时发布镜像归档，通过 docker load 部署。将来若启用 GHCR，仍须单独核实包权限、初始化和 private 状态。

## 生产现场只读核查

- 早前只读记录显示主应用曾为 pk.18；切换前重新核对的实际基线为 pk.41（提交 `7a5f37b60f267356996dab368ce28d60ab9fb20c`），运行程序 SHA256 为 `5064b88aab2a56bb98a82c170b12efff48befc88c17264239f5f104c3e15c7aa`，原镜像为 `sha256:1e975dcc8675bcda8c73587afcabb53e4a186b4294f17d01dfe79e40ba5dba21`；网页更新留下的镜像程序/seed SHA256 为 `28f9231e334f5086d8a0f8c217a9053e28b107d4bf40272716b03f528ee43ea3`，与运行程序分开核验。
- 当前程序内仍固定旧仓库，没有通过环境变量热切换仓库的入口；只修改origin或UPDATE_GITHUB_TOKEN不会改变运行中的更新源。
- 生产现有更新凭据读取新私库metadata及releases均返回HTTP200，无需将聊天中的凭据或新的高权限凭据写入应用。
- 生产源切换须安装由新库发行、内置新源的程序，首跳需通过受保护的运维部署路径，旧程序不会下载被新URL白名单拒绝的包。
- 服务器身份、凭据、专属部署路径仅保存在受限且Git忽略的本机配置中；本文不包含这些信息。

## 验证说明

源码阶段只执行格式化、语法解析、调用链与差异静态核对；没有用历史源码报告替代本轮测试。GitHub权限/API以及生产源和token读取权限检查只是相应只读实证，不是构建、完整回归、制品验收或已部署的证明。

本轮已核对新库 main/版本分支/正式标签、合格制品、生产运行程序和更新源。Jenkins 与私有 GHCR 保留为历史可选路径，本次未使用。

用户后续确认无 Jenkins，当前不配置线上 Jenkins 或 GHCR；本轮实际交接项改为新库源码/标签、私库 Release 归档与证据、隔离制品验收、生产运行程序及更新来源。历史 Jenkins/GHCR 流程仍作为可选能力保留。

## 无 Jenkins 的本轮构建与发行

- 用户确认没有 Jenkins，采用此前已使用的本地编译、服务器独立 TLS Docker daemon 验收路径。前端冻结依赖构建，后端以固定 commit/tree 和 release 标识交叉编译 linux/amd64。
- 新增 `deploy/local-release.py`，复用现有 manifest、哈希、十一项报告、正式标签/main 和私有仓库校验；只接受新源、真实本机编译与服务器隔离验收的合格制品。
- 程序更新归档和 Docker 镜像归档含同一二进制。先创建私库 draft，上传并核对所有资产后转正式 Release；失败保留草稿，不覆盖旧发行。该路径不向 GHCR 写入，也不改变既有 Jenkins 发布门禁。
- 生产部署只操作该项目，保持现有 CF、UPDATE_GITHUB_TOKEN、JWT、加密密钥及数据，排空、备份后切换；首次切源使用运维路径，后续网页更新由新库获取。
- 构建准备发现 follow-redirects 1.16.1 新高危公告，按现有审计策略升级至修复版，不新增豁免；原 xlsx 例外保持。
- 本轮已完成本机构建、隔离制品验收、私有 Release 发布和生产切换，以下记录为实际结果。

## 本轮实际构建记录

- 前端 Node v22.18.0 / pnpm 10.14.0，冻结安装完成；`pnpm build` 内置语言键检查3/3、`vue-tsc -b`及 Vite生产构建通过。
- follow-redirects 仅升级1.16.1→1.16.2；锁文件SHA256为`1e73d5508cbdecd5273421d319199d5e5852cbfb424417ea8f85767209cedbc9`。
- 官方npm生产依赖审计0 critical、2 high、12 moderate、2 low；2项high均为既有xlsx有效例外，项目例外检查器exit 0，没有新增豁免。
- 前端构建日志SHA256为`ee1384ba69789f1e5c6223efe61a2c3701d74ec6e7345db0b4a59c11ba5eb6f8`。首次沙箱执行曾因依赖缓存ACL报ERR_MODULE_NOT_FOUND，最终在批准的构建环境执行成功；不把此前失败记录改写成通过。
- 后端使用 Go `go1.27.3 windows/386` 工具链交叉编译 `linux/amd64`，程序 SHA256 为 `8322c4dec98f1aefa853a059dcfaef9748aeda9a6c5df1fac7c3bef667add831`；package 为 `1793587600-b955db599e28`，commit 为 `b955db599e2865267f5a8391094e5cb72dbfa3f4`，tree 为 `64389c20ebdc9567ca6300d4af72233244a48603`。服务器隔离十一项验收和 pk.41→pk.42→pk.41 七项 smoke 均通过，既有 254/255 保留，仅新增 256（checksum `a686231d42e7c66868fbfb29e055acf75ed7397efea41e392ca7f9e1cebeab7a`）。
- 正式 Release `v0.2.19-pk.42` 已在新私库发布，未向 GHCR 推送；生产现运行 `pk.42`，来源为 `yb1320057104/sub2api`。入口排空、备份、CF/环境/业务数据保持和更新源核验均通过。本轮未执行完整源码回归或真实账号/模型请求。
