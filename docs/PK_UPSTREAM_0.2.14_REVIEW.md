# v0.2.14 合并核对（2026-10-08）

## 来源、范围与分支

- 合并前私库：`3902278043ab5edb1bf590cd755b87e043be4d49`；当时 `origin/main` 为 `91cbc5165e84130225cf968f68b8a3b0226f48cc`，已包含在工作分支中。
- 上游正式发行：[Wei-Shaw/sub2api v0.2.14](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.14)，2026-10-07 发布。固定 commit 为 `0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d`，annotated tag 对象为 `1400a7b482974d98db5b284a8b2afbe3eaf9aaef`。
- 共同祖先为上次上游 `v0.2.12`：`5106065716e494204fc0e8db16f68f6e9d576be0`。本次上游输入为其后的 18 个提交、31 个文件，不跟随标签之后的 `main`。
- 开始时工作区干净；已核对本地及私库远端分支、标签、正式 Releases 和在途 PR。源码仓库 API 确认为 private；最近私版发行是 `v0.2.12-pk.2`。
- 唯一在途为历史分支 `codex/jenkins-build` 的[草稿 PR #7](https://github.com/pengkang0322/sub2api/pull/7)。按项目约束第 6 节第 5 条延续已有未完成分支，复用该 PR，保留 Jenkins 固定 SCM 引用；本次没有新建、删除、改名分支或改写历史。后续新工作分支仍须采用版本命名。
- 规划目标为 `v0.2.14-pk.1`，未创建标签或 Release。上游标签内 VERSION 仍是 `0.2.13`，本分支将源码 VERSION 对齐 `0.2.14`，候选标识继续由现有 Jenkins 流程生成。
- 本次只合并源码并更新原草稿 PR，不合入正式 `main`，不打包、发布或部署。使用双父 merge commit 保留上游关系；交付 PR 记录最终完整 commit/tree。
- 无新增 PK ID。更新 PK-003 的删除 Key 结算边界、PK-017 的初始化/依赖/审计说明、PK-021 的上游完整吸收与豁免范围、PK-022 的普通支付隔离说明，以及本清单合并基线。
- 用户在本次任务明确要求完整合入上游全部新功能，特别是易支付漏洞修复，并将“不扩大豁免”限定为二开功能。已同步 AGENTS、项目约束、维护文档及仓库/安装版维护 skill；上游原有功能的审计策略随固定版本同步，不能套用到二开特有风险。原本地禁令和正式合入/发布门禁保留。

## 文本冲突与行为冲突处理

1. 三语 `README.md` / `README_CN.md` / `README_JA.md`：上游管理员说明落在私版已经替换的公开安装章节内。保留私库与 Jenkins 部署入口，将随机管理员邮箱、密码和 8–72 字节校验移入私版说明；同时纠正“管理员仅能由 Web 向导创建”和已失效的第 5 步引用，说明 Web/CLI/AUTO_SETUP 及已有用户跳过创建。
2. `deploy/README.md`：保留私版完整 Compose、overlay 和凭据说明，接入上游随机登录名/首次日志以及密码限制。`deploy/PK_DEPLOYMENT_CN.md` 同步说明，并纠正遗留的“Actions 构建”为当前 Jenkins 流程。
3. `deploy/docker-deploy.sh`：保持既有停用脚本逐字不变，不能重新引入上游下载、生成数据库/JWT 密钥和覆盖配置。新初始化安全修复由后端 setup、环境示例及四份 Compose 接入，停用脚本无需重新启用。
4. `backend/cmd/server/VERSION`：手动设为 `0.2.14`，不把上游标签内滞后的 `0.2.13` 当作本候选版本。
5. `.github/audit-exceptions.yml` 按用户明确的上游完整吸收要求，与 v0.2.14 原样一致：两条 xlsx 例外的理由、缓解措施及到期日 `2027-01-06` 均同步。实际代码仅在管理员 `UsageView.vue` 动态 import xlsx 后导出；PK-023 复用该页未增加读工作簿入口，PK-010 字段映射未增加解析风险。该上游例外不用于二开新增的代码、权限或不可信文件处理；并不等于漏洞审计通过。
6. 新 `.github/SECURITY.md` 保留上游报告渠道，并注明二开问题向私库维护者私下报告、不得把私版代码/凭据/用户数据交给上游；不改变正式更新和恢复来源。

所有文本冲突均已解决。上游后端安全/结算/setup、Codex 配置修复及前端依赖文件与固定上游版本一致；私版转换、首字、币安和排行榜实现保持原有差异。无新增数据库迁移，也未修改既有 PK 迁移。

上游31个变更文件另作完整吸收核对：19个文件与 v0.2.14 完全一致；另外12个是 SECURITY 的私版说明、三语README、部署README/Apple文档/环境示例、三份正式Compose、已停用docker-deploy.sh及VERSION。后者仅作私源、流程或版本适配，管理员初始化与安全修复仍完整接入；未发现遗漏的上游功能。

## 全部有效二开核对

下表的“已保留 / 无影响”仅表示源码、配置和调用链的静态结论；受影响项的动态验收另列，不能作为合入正式分支或发布的通过证据。Vue 的公共依赖变化同时影响现有页面，页面源码不变不代表已通过运行验收。

| PK ID | 静态结论 | 代码/配置依据 |
|---|---|---|
| PK-001 | 已保留 | `account_cache_conversion_validation.go`、`admin_account.go` 和创建/编辑页未变；OpenAI 限制、默认 false/1、显式 false/0、缺字段保留、批量目标预检及 extra 合并保留。Vue 升级后设置往返待验证。 |
| PK-002 | 无影响 | `cacheconversion.ConvertReadTokens` 的 decimal/Floor/int32 检查和 `convertOpenAICacheCreationToRead` 的总量及 5m/1h 清零均未变。 |
| PK-003 | 已保留 | `openai_gateway_usage.go` 先拆原输入、转换一次；`gateway_usage_billing.go` 继续统一实扣口径。`usage_billing_repo.go` 只忽略删除 Key 的 `ErrAPIKeyNotFound`，余额/订阅/账号与幂等事务继续，真实数据库错误仍回滚；新组合行为待验证。 |
| PK-004 | 无影响 | `UsageTokens.InputContextTokens()`、`OriginalContextTokens` 与 `applyAccountStatsCost(rawTokens, cost.TotalCost, …)` 分支未变，原始上下文选档和两种账号统计口径保留。 |
| PK-005 | 无影响 | JSON/SSE usage 转换、别名/显式 0、原始 usage 采集和 Content-Length 清理未变；下游序列化副本不污染内部账单。 |
| PK-006 | 已保留 | Responses/Chat/Messages、桥接、直通、WS 文本、已接入图片及 embeddings 入口未变；TypeSafe 仅增加管理侧探测支持，没有新增模型转发入口。共享结算的运行验证同 PK-003。 |
| PK-007 | 已保留 | `setting_gateway_runtime/setting_parse/setting_update`、设置 handler/DTO/API/UI 仍保留三模式、默认关闭/comment/1000–2000ms、显式非法值拒绝及省略保留，成功保存后才刷新缓存。Vue 升级后配置 UI 待验证。 |
| PK-008 | 无影响 | OpenAI Responses/Chat/Messages 的既有 handler 到 heartbeat/space writer 链未变；Chat 三帧、真实 Responses ID、完整 item/content/text 生命周期和终态补齐保留；Messages/结构化输出/input 兼容回退及排除接口保留。 |
| PK-009 | 无影响 | 原子首次非空 body 计时、真实 0 与 null 区分、`CopyFirstResponseTiming`、SQL 全部插入/select/scan 和迁移241未变。TypeSafe 仍在异步入队前快照渠道字段，不在闭包读 Gin context。 |
| PK-010 | 已保留 | 普通 DTO 仅映射 `FirstResponseMs`，管理员 DTO/表格/导出保留两个计时；公开 channel monitor 的所有聚合路径清除 TTFT 并重算分数。Vue 升级后展示/导出待验证。 |
| PK-011 | 无影响 | `EarlySSEAdjustedWrittenSize`、队列/周期注释扣除和 `PrepareEarlySSESpaceRetry` 未变；同锁取消未发占位、空格已提交拒绝重试，handler/service/Grok transport 门控完整，Written 仍表示真实 HTTP 提交。 |
| PK-012 | 无影响 | Header 克隆、写锁、Stop 等待/还原、短写、取消/Hijack、协议对应的晚到 SSE 错误和 Ops 标记未变；Server-Timing 仍限制真实 `/api/v1/`。 |
| PK-013 | 已保留 | `VersionBadge.vue` 保留 PK出版；更新器 pk 数值排序、15个 Release/最多3个回退及目标授权未变。VERSION 对齐0.2.14；Vue 升级后的版本菜单待验证。 |
| PK-014 | 已保留 | 更新器固定私库、缓存隔离、通用 GitHub 元数据客户端和正式 Compose 私有镜像/token 未变；私库可访问且 API 返回 private。本次未查询 GHCR 的实时状态，也未进行镜像操作。 |
| PK-015 | 无影响 | 更新器及 GitHub release 客户端没有变化，资产 API、跨域去授权、SHA256、固定安装路径、备份及替换链保留。 |
| PK-016 | 已保留 | Dockerfile/entrypoint 未变；Compose 仅修改管理员邮箱默认值，普通用户、数据卷程序、镜像种子和网页更新环境选项保留。新初始化与容器运行待验证。 |
| PK-017 | 已保留 | Jenkins、私库发布 helper、本地十套件入口未变；保留 Axios 1.20.0，合入 Vue/source-map-js 和锁文件。停用脚本保留，部署说明同步初始化修复，上游 xlsx 例外原样同步且不扩大到二开特有风险；构建、安全及运行验收均待验证。 |
| PK-018 | 无影响 | concurrency provider 保留只跳过全局启动清理、继续过期槽位 worker 的分支，没有新增 provider。 |
| PK-019 | 无影响 | 19项后台启动门、普通文件激活、一次启动、停止等待以及 wire/wire_gen 顺序未变；BatchImageCleanup 保留局部 done 捕获和未启动可停止。无新增后台服务。 |
| PK-020 | 已保留 | 维护文档保留独立数据副本、真实连接/异步计费排空、后台交接、原密钥和私源回退。未操作服务器或生产数据。 |
| PK-021 | 已保留 | AGENTS、项目约束、唯一清单、PR模板和维护 skill 保留；按用户要求明确上游完整吸收与二开豁免范围，仓库/安装版skill同步。新增本记录，延续唯一历史分支/草稿PR，不降低门禁。 |
| PK-022 | 已保留 | 币安独立 routes/client/service 和迁移242/243未变；倍率快照、五位匹配、八位舍入、UID去重、事务入账/累计充值/流水及失效缓存保留。EasyPay修复不进入币安路径；Vue升级后页面待验证。 |
| PK-023 | 已保留 | `AdminDailyUsage` 嵌入 `UsageView`，今日全用户、完成后再排5秒刷新、隐藏/卸载停止、管理员API及双计时权限保留；Vue升级后刷新生命周期待验证。 |
| PK-024 | 已保留 | `Bills` 只读且不调用 acceptReceipt；AdminAuth、原精度、100条提示、租约token/独立冷却/到期订单优先/失败退避均未变；Vue升级后账单页面待验证。 |
| PK-025 | 无影响 | 代理配置仅注入 Binance client；History/Bills/Probe 每次 beginOperation 后校时，固定 CONNECT token、15秒预探测、香港/币安双检查和失败关闭未变。 |
| PK-026 | 已保留 | `request_type=4`、Unicode前三字服务端脱敏、普通GET/管理员写权限、10分钟冻结预览、唯一认领、用户锁及余额不足整笔回滚未变；迁移244独立扣费证据保留。Vue升级后排行页面待验证。 |
| PK-027 | 无影响 | 默认关闭/阈值10/手动解禁重计true、迁移245父表实际插入触发器、管理员豁免、独立累计、用户锁及既有鉴权outbox未变；不新增后台任务。 |

## 首字整组与新增入口

PK-007～012 按配置→handler→writer→detached usage→SQL→DTO/UI/导出的链路整体核对，而非仅确认下拉框存在。comment/ping 不改正文和原安全换号；space 只增加登记的一次 U+0020，保留真实 Responses ID、模型正文、工具调用和原 usage，空格已写出后阻止重放。真实首包从第一次成功非空写出测量，header-only flush 不计，关闭提前发送仍测量；未测量值保持 null/`-`。本次没有新增网关路由或流式适配器。

新/变更调用入口另外核对如下：

- `bootstrapAdminUser` 先查询用户/管理员并执行既有跳过判定，再调用 `prepareAdminCredentials`。已有用户时不重新校验/修改初始化凭据；空库才随机生成或校验邮箱、密码。CLI 与 Web 复用校验，Compose 不再注入可猜测的默认邮箱。
- `applyUsageBillingEffects` 保留用户/订阅结算与账号额度事务；只在 Key 的查询无行/更新零行时忽略 `ErrAPIKeyNotFound`，没有把真实 SQL 错误吞掉，也没有使用预留估算替代转换实扣。
- TypeSafe 的 `IsUpstreamBillingProbeIdentity` 和官方域名列表扩展复用管理探测；官方 `typesafe.ai` 返回 unsupported，不发送探测请求。`/v1/systemone` 非流式 handler 及已有异步上下文修复未变。
- `CanonicalizeReturnURL` 清理用户 query 后，由 `buildPaymentReturnURL` 添加本站参数；EasyPay notify 只允许标准参数。普通 `payment_orders` 路径与币安签名账单、独立充值事务隔离。
- `UseKeyModal.vue` 仅在远程 Codex 模型目录配置中增加 `api_key_model_discovery`，不改缓存转换、首字、客户端 Key 权限或网关计费入口。

## 验证、未覆盖范围与交付门禁

- 本次用户明确禁止本地构建和测试。实际仅进行 Git 历史/双侧 diff、代码与调用链、文档、配置/锁文件的静态审查，以及 `git diff --check`、冲突标记和未合并索引检查；没有执行 Go、Vitest、lint、类型检查、pnpm audit、Docker 或任何应用运行验收。
- 本次没有本地测试报告；没有可声称通过的测试 commit/tree 或套件。没有执行 Jenkins 构建，故构建号、package_id、制品SHA256及正式发布验收均不适用。PR 原有 Jenkins #3 是旧源码的历史打包证据，不证明本次合并通过。
- 新上游 EasyPay 安全回归文件及现有 setup/return URL/删除Key/TypeSafe 回归全部保留，没有生成临时测试文件。它们不是本次自编临时夹具；不为清理要求删除上游已有回归。原 PK 断言未被删除或弱化。
- `scripts/test-local.py` 的 backend unit/integration 使用 `./...`，后续获准执行时会包含上述回归。前端关键 suite 保留首字、使用记录/导出、币安和排行榜的既有覆盖；缓存转换专用前端回归仍是清单中的已知缺口，没有把未执行或未覆盖项写成通过。
- **受影响动态待验证项**：PK-003（并关联PK-006）的转换倍率×删除Key×余额/订阅/账号额度组合；PK-001/007/010/013/022/023/024/026 的 Vue 升级后页面行为；PK-016/017 的新空库初始化、已有用户跳过、容器运行、依赖、安全及制品验收。上游新增余额软删除integration/sqlmock仅覆盖代表路径，不能代替上述组合。
- 两条 xlsx 例外按上游同步至 `2027-01-06`；security suite 未执行，仍须对实际 audit 结果、适用路径和期限作检查，不豁免二开特有风险。静态结果不声称实际审计成功或失败。
- 首字三模式/race/协议终态/权限/持久化、币安真实转账/账单/香港出口、排行并发扣费/自动禁用outbox等本次均未动态验证；PK清单已有OAuth图片、WS二进制和下游SDK/代理等边界保持。
- 因以上待验证项，继续保留草稿PR，不合入正式main、不创建正式Release；没有触发GitHub Actions打包/测试，没有线上变更。线上实际版本未核实。

- [x] 全部27个有效PK ID已逐项静态核对，新增/受影响说明已同步清单，无新增ID。
- [x] 复用唯一在途历史分支和PR，保护Jenkins SCM引用，保留双父合并历史。
- [x] 遵守本次本地构建/测试禁令，未执行项如实记录，既有回归保留。
- [ ] 受影响项完成动态验收；当前仍待验证，不能合入正式分支或发布。
- [ ] 正式发行同提交、同制品的完整本地报告及Jenkins发布验证（本次未请求/未执行）。
