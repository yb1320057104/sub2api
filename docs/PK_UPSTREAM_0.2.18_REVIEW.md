# v0.2.18 上游合并核对

## 来源、范围与交接

- 日期：2026-10-26。用户要求合并上游 0.2.18；按既定授权直接合入私库 main 并准备正式标签，不执行构建、发布或生产操作。
- 合并前私库：`pengkang0322/sub2api`，`main` / `v0.2.17-pk.16`，commit `f992259daaa56f4719bc7fc38420edde4ae09ee7`。开始时工作区干净，私库为 private，无开放 PR。
- 上游输入：`Wei-Shaw/sub2api` 的 `v0.2.18`；附注标签对象 `de86961b627e48db75d61fff8c05535d44a00cbe`，剥离后 commit `ac1b8f1271be0d674fa6f074403c1410f30a3a81`。共同祖先 `060d0160417782cd962a0c7f460f6cd4565d4916`。固定提交合并，保留双方父提交关系。
- 唯一工作分支及正式标签目标：`v0.2.18-pk.1`。创建分支前已核对本地/远端分支、标签、开放 PR；该版本未占用。采用直接提交合入方式，不另建同步分支或重复 PR。
- 用户在本轮进一步明确：保留缓存转换和 Kilo 桥接两个开关，二者关闭时计费必须与上游完全一致。按此要求检查并修复已有计费隔离缺口；比较基准是固定的 v0.2.18 源码，不修改历史账单，也不把该要求扩大为其他模型提供商价卡修正。
- 无新增二开。兼容修复更新 PK-008、PK-009、PK-028；版本与发行说明同步 PK-013、PK-017、PK-021。其他有效 ID 逐项核对如下；PK-029、PK-030 继续保持已授权移除。

## 上游变更与冲突处理

完整合入固定上游提交中的 Vertex service_account 配额、Gemini 生图门控、账号筛选持久化、调度快照进程缓存、模型映射签名优化、账号配额 SQL 优化，以及 Anthropic/Gemini 断流计费、流内错误与重试、负输入用量、Grok Fast 定价、Cloudflare 1010 分类、模型限流写时复制、Gemini 工具参数、DeepSeek 图片字段和图片转存目标限制等更新。

文本冲突共四个文件：

1. `.github/workflows/{backend-ci,release,security-scan}.yml`：保持私版已删除状态，不恢复 Actions 测试或打包。上游 Go 1.27.3 更新与私版现有 `backend/go.mod`、Dockerfile、Jenkins 工具链一致；不是丢弃上游安全补丁。
2. `frontend/package.json`：保留私版 `follow-redirects=1.16.1`，同时吸收上游 `<1.16.1` 的下限约束；`pnpm-lock.yaml` 同步，已有 DOMPurify 3.4.17 保留。未安装依赖、未增加审计豁免。

上游标签内 `backend/cmd/server/VERSION` 仍为 `0.2.17`，本次显式对齐为 `0.2.18`；正式机器版本继续由 Jenkins 正式标签注入，页面保留“PK出版”。三语 README 的上游增量保留，私库部署说明及运行时品牌入口保持。

行为交叉检查补充以下适配：

- `openai_early_sse_space.go` 将 `response.metadata` 纳入占位前元数据，避免上游新元数据提前取消空格。只从真实 `response.created` 提取响应 ID；metadata 不伪造 ID，不覆盖已有正文/结构化格式保护。原有序号、输出索引、工具参数、终态 output 和计时语义保持。
- `gateway_handler_chat_completions.go`、`gateway_handler_responses.go` 的新共用计费提交闭包在入队前保存 `ChannelUsageFields` 值，不在异步 worker 内读取可回收的 Gin context。保留成功及中断但已观察到 usage 的计费、failover 不重复计费、请求预留和 detached context。
- `scheduler_cache.go` 的精简候选投影增加已有 `kilo_bridge_enabled`，使选账号阶段的渠道模型约束、账号模型映射及 HTTP 策略与完整账号一致。只传递既有配置，不更改默认关闭、布尔读取或共享 map。原缓存转换配置仍在最终完整账号上读取。
- 旧 Redis 精简记录无 TTL，进程缓存10秒到期也不会自动补出 Kilo 字段。仅 OpenAI meta 增加 `PKProjectionVersion=1` 标记，旧格式按既有缓存未命中处理，由原数据库回源/后台重建生成新投影；其他平台和完整账号 JSON 不要求该标记。保留上游桶序号、epoch、修订号、进程缓存及共享只读母本。默认允许数据库回源；若明确关闭 `db_fallback_enabled`，升级期间旧 OpenAI 缓存会等待启动或定时重建成功后可用，不绕过关闭设置。未清理生产 Redis 或变更运行配置。
- 新旧进程混部时，旧进程仍可能重写不带标记的 OpenAI meta；新进程按同一未命中规则处理，直至新格式重新写入，不把该机制描述为升级后只发生一次。完整账号读取及其他平台不受此标记约束。
- 若同时关闭数据库回源和周期全量重建，启动重建失败后不能保证自动定时恢复；部署时应先确认后台重建成功。这是沿用显式配置边界，未在本次合并中自动开启回源或修改用户配置。
- 按用户本轮“两开关关闭时与上游完全一致”的要求，修复 PK-028 历史共享倍率缺口。`reasoningEffortBillingMultiplier` 恢复 v0.2.18 原实现，只有 Kilo 开启时为客户定价子 context 设置私有标记，并允许 ultra/persistent 原始强度倍率。该 context 覆盖基线、响应模型重定价和免费Fast重算；账号成本和普通请求保留上游 context/强度规则。旧倍率配置保留，但不开 Kilo 就不参与扩展计费；不追溯改账。

新增 `gateway_forward_compat_stream.go` 属于原生 Anthropic GatewayService；它与 Gemini/Antigravity 新断流路径不属于仅 OpenAI 平台的缓存转换扩展范围。已接入的 OpenAI→Anthropic 桥接继续传递所选账号并调用 JSON/SSE 转换。Grok 转发账号在执行前通过完整账号重读取得独立 map，未因共享候选快照猜测风险而修改其写入逻辑。

## 二开逐项核对

结论均为代码/差异静态核对，不代表本次动态测试通过。

| ID | 结论 | 代码、配置及行为依据 |
|---|---|---|
| PK-001 | 已保留 | 创建/编辑缓存转换默认 false/1、显式 0、有限非负校验及 `admin_account.go` 创建/局部更新/批量保存链保留；Vertex 配额使用独立判断。 |
| PK-002 | 已保留 | `cacheconversion/usage.go` 和 `openai_cache_conversion.go` 未改，decimal 向下取整、溢出保护及创建/5m/1h 清零保持。 |
| PK-003 | 已保留 | `openai_gateway_usage.go` 仍先拆原始输入再转换，溢出记录原 usage 且零扣费；余额、订阅、Key 额度与账号额度继续统一 Cost。上游日/周 SQL 提示不改变幂等指纹，真实数据库错误仍回滚。 |
| PK-004 | 已保留 | `InputContextTokens()` 继续以原始上下文选档；原始账号成本与客户 TotalCost 分支独立，OAuth/API Key 边界保留；新增 Grok Fast 定价及 200k 阈值完整吸收。 |
| PK-005 | 已保留 | JSON/SSE usage 改写及原始证据采集保留，未知字段/显式 0/终态 usage 不被元数据与空 delta 判定替换；Kilo 强度恢复及错误脱敏继续独立执行。 |
| PK-006 | 已保留 | Responses/Chat/Messages、直通、桥接、WS 文本、已有图片及 embeddings 转换入口保留；Anthropic 行泵签名适配仍保留 account 与转换回调。 |
| PK-007 | 已保留 | 三模式枚举、默认关闭/1000–2000ms、非法值校验、局部保存/审计/缓存、前端秒毫秒转换未变；相邻 Codex UA 文案按上游更新。 |
| PK-008 | 已保留 | 新 metadata 兼容如上；created 真实 ID、空间占位生命周期、JSON 格式/非流式/WS/图片/compact 排除及关闭时首包测量保持。 |
| PK-009 | 已保留 | 两个新计费提交入口在入队前快照渠道信息；`CopyFirstResponseTiming`、预留引用、nullable SQL select/scan/单条及批量插入链完整。 |
| PK-010 | 已保留 | 普通 DTO `first_token_ms` 映射 `FirstResponseMs`，管理员上游指标独立；用户无回填、共享表格默认权限、公开监控 TTFT 清理及健康分重算未变。 |
| PK-011 | 已保留 | comment/ping 字节扣除及周期保活保留；新 metadata/空 delta 与 Retry-After 兼容，`PrepareEarlySSESpaceRetry` 仍原子取消未发空格，已输出空格禁止重试。 |
| PK-012 | 已保留 | writer 写锁、停止等待、短写保护、晚到协议终止事件及 Server-Timing UI 范围保持；原生 Anthropic 新排水/超时处理完整吸收。 |
| PK-013 | 已保留 | VERSION 对齐 0.2.18；`VersionBadge.vue` 的 PK 标识及 `update_service.go` 的数值 pk 修订排序、回退列表限制未改。 |
| PK-014 | 无影响 | 更新器、Release 客户端、私库/私有镜像配置未改；`pengkang0322/sub2api` 唯一发行与恢复源保留。 |
| PK-015 | 无影响 | 资产授权、每跳 URL/跨域授权限制、SHA256、固定安装路径、原子替换及备份恢复代码未改。 |
| PK-016 | 无影响 | Docker entrypoint、种子哈希与持久化可执行路径未改；网页 SIGTERM 重启及正常清理链保持。 |
| PK-017 | 已保留 | Actions 构建/测试仍删除，Jenkins main 可信流水线、私库来源、正式标签/版本未占用、同二进制、十一项制品验收和 SHA256 门禁未改；依赖补丁不回退。 |
| PK-018 | 无影响 | `wire.go` 的跳过启动全局槽位清理及持续过期回收未改。 |
| PK-019 | 无影响 | 普通文件启动门、21 项后台、取消待启动回调后停止服务、智商测试独立收尾及网页正常退出链未改。 |
| PK-020 | 无影响 | 部署/恢复流程保留端口、数据、密钥、在途请求排空和后台单实例交接；本次不执行线上操作。 |
| PK-021 | 已保留 | AGENTS/项目约束/唯一清单与 PR 模板保持；新增本记录，同步目标版本和旧条目，直接合入授权与不测试要求如实记录。 |
| PK-022 | 无影响 | 币安订单倍率快照、五位金额精确入账、流水去重/订单锁/事务、加密凭据及用户订单/管理员权限未改。 |
| PK-023 | 无影响 | 管理员币安嵌入每日消费页、今日全用户/5秒默认、完成后排程与卸载取消、管理员首字权限未改。 |
| PK-024 | 无影响 | 实时账单只读查询、租约/冷却/充值优先与失败退避未改，不进入入账链。 |
| PK-025 | 无影响 | 每次香港出口探测、失败关闭、独立固定出口 transport、CONNECT 限制和凭据隔离未改。 |
| PK-026 | 无影响 | 排行榜脱敏、扣费预览冻结、行锁/逐条防重复扣费及 admin_balance 流水保留。 |
| PK-027 | 无影响 | 迁移245默认关闭/阈值10、管理员豁免、request_type=4计数、手动解禁重计与鉴权失效链未改。 |
| PK-028 | 已保留 | 调度投影补开关与旧缓存版本兼容；强制 HTTP、模型映射及工具/脱敏保持。按用户新要求修复共享倍率泄露，原始强度倍率仅在 Kilo 开启的客户计费生效，普通路径恢复上游。 |
| PK-029 | 已授权移除 | 隐藏倍率/用量缩放实现及旧迁移未恢复，不删除历史数据库数据或迁移记录。 |
| PK-030 | 已授权移除 | 原始 Token 趋势覆盖实现及旧迁移未恢复，不改写历史统计或账单。 |
| PK-031 | 无影响 | DP API 默认名/Logo、柔蓝主题、Home/登录、站内合规、友链移除、菜单权限和16rem/72px侧栏保持。 |
| PK-032 | 无影响 | 智商测试回环一次性凭证、模型/强度、动态并发、保存代次、租约 fence、失败重试/独立收尾及展示未改，原鉴权及计费继续生效。 |
| PK-033 | 无影响 | 联系文字/图片校验、局部保存、审计/公开/注入链和未配置隐藏保持；首页/登录/仪表盘共享白卡、右置二维码及容器换行未改。 |

## 升级行为与迁移

- 上游迁移 `246_enable_gemini_image_generation_groups.sql` 只开启存量 Gemini/Antigravity 分组的生图开关；混合 composite 分组不自动开启。混合分组需要使用 Gemini 生图时，应在分组配置中明确开启“允许当前分组生图”。
- 迁移 runner 按完整 filename 主键和 checksum 管理，遍历全部 SQL 文件，并非只运行大于当前最大编号的文件。因此已有248～250及历史已移除246/247记录不阻止新246执行；不改旧迁移文件、重命名历史记录或降低校验。未连接数据库执行迁移。
- `grok-4.7-fast` / `grok-4.7-build-fast` 接受上游独立 Fast 价格修复；默认价格为普通4.7的两倍，长上下文按 Fast 规则计费，不覆写用户既有自定义价格。
- 图片转存完整接受上游公网目标/重定向及图片类型限制，未借此改动 PK 联系二维码或智商测试作品的本地净化/展示规则。

## 验证与交付边界

双开关关闭的专项静态核对：`convertOpenAICacheCreationToRead` 原样返回 tokens，不设置 `OriginalContextTokens`；长上下文仍按上游三类输入求和。用量解析器、别名/显式0规则、强制 include_usage 均沿用上游，HTTP/SSE/WS/图片/embeddings 转换关闭时保留原始数据。统一 `usage_billing.go`、`usage_billing_repo.go`、模型价格 JSON 及本次新增断流实现与固定上游相同。普通强度计算使用恢复后的上游函数，Kilo私有标记不会传入账号成本或请求原context。首包及原始请求强度的额外记录字段保留，不改变关闭路径的金额/额度规则。

- 已做：固定来源/两侧差异检查、所有有效 PK ID 静态核对、生产兼容适配与双开关关闭计费专项复核、依赖声明/锁文件检查、迁移命名和不可变性审查、冲突标记与 `git diff --check` 检查。
- 未执行任何本地或服务器测试、类型检查、lint、应用/前端构建；没有新源码通过报告或 notes，不以历史报告证明本次通过。上游随版本提交的回归文件完整保留；扩展仓库原有调度缓存、强度倍率和网关计费测试文件，本次未自行生成临时测试文件。
- 正式标签建立前核对私库、GHCR 为 private，以及目标标签、Release、镜像版本未占用。提交后核对远端 main、附注标签剥离 commit 与祖先关系；最终 SHA 以本次 Git 提交及交付结果为准。
- Jenkins 交接：`ACTION=release`、`SOURCE_REF=v0.2.18-pk.1`（不带 `origin/`），`COMMIT_SHA` / `PACKAGE_ID` 留空，由用户亲自点击。未执行 Jenkins 构建、十一项制品运行时验收或发布，没有 package_id/制品 SHA256 结果。
- 本次未进行线上变更，线上实际版本未核实；源码合入及标签准备不代表制品已发布或生产已升级。动态协议串联、并发、视觉与数据库迁移运行结果仍以之后真实执行为准。
