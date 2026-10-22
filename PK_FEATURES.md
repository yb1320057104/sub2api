# PK出版二开功能清单

**本清单是二开功能的唯一登记表，受 [项目约束](PROJECT_CONSTRAINTS.md) 保护。** 新增、扩展、修复二开必须同步登记；合并上游必须逐项核对。清单遗漏不代表可以删除已有功能，发现遗漏应立即补齐。

## 基线与阅读方式

- 首次二开上游基线：`a60a29549f488a854966aaec9541abbe006cac22`（0.2.10）。首次二开保存：`6655dd8ca`，恢复标签 `pk-baseline-20260930`。
- 2026-10-08 在唯一在途历史分支 `codex/jenkins-build` / 草稿 PR #7 合并上游 `v0.2.14`（commit `0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d`，tag 对象 `1400a7b482974d98db5b284a8b2afbe3eaf9aaef`），合并前私库为 `3902278043ab5edb1bf590cd755b87e043be4d49`。逐项静态核对及当时待验证项见 [0.2.14 合并记录](docs/PK_UPSTREAM_0.2.14_REVIEW.md)。源码 VERSION 对齐 `0.2.14`，未创建发行标签。合并提交 `fc468013` 生成时仅作静态核对；后续 Jenkins #4 候选 `4-fc46801394b0` 已构建成功，十一项实际制品验收通过，证据只绑定该旧 commit。当前流程新 tree 的完整源码测试以新报告及 notes 为准，旧候选成功不代表当前已合入 main、正式发布或线上更新。
- 2026-10-02 合并上游 `v0.2.12`（commit `5106065716e494204fc0e8db16f68f6e9d576be0`，tag 对象 `cbe9966432317e6757b47f598a103d2132ec27db`），合并前私库为 `162a58972bee08078a43eefa761da1b245f667fa`。逐项核对见 [0.2.12 合并记录](docs/PK_UPSTREAM_0.2.12_REVIEW.md)，正式发布目标为 `v0.2.12-pk.1`；发布与线上更新分别记录。
- 2026-10-02 合并上游 `v0.2.11`（commit `96f4c115c9749078f90cbf210a01d39baf3f53b6`），合并前私库为 `32f068dcbdc75063ff7f6488aef6c85912d7bf3f`。逐项核对及本次验证见 [合并记录](docs/PK_UPSTREAM_0.2.11_REVIEW.md)；合并源码不代表已发布或更新线上。
- 本次清点覆盖从该上游基线到私库 `23e5fe663338c86cd0cebbf2f688c8a75dd8c152` 的全部二开提交；应用发行参照 `v0.2.10-pk.7`，另登记本次新增的项目约束机制。此处是代码核对基线，不代表线上当前版本。
- 以下 PK-001 至 PK-028 及 PK-031、PK-032 均为有效保护项；PK-029、PK-030 已按用户授权移除，保留原 ID 与回退记录，不得复用。文件路径用于定位，不是完整修改白名单；所有新调用入口、迁移和旁路都要检查。
- 每项说明“必须保持”的行为。文末另列验证入口与已知边界；测试存在、历史验证通过和本次候选通过是三件不同的事。
- 2026-10-21 用户最新要求取代 2026-10-08/19 的源码测试及 notes 前置门禁：核对二开保护和变更后直接提交、合入私库 main 并推送不带 `origin/` 的正式版本标签，交给用户点击 Jenkins `ACTION=release`。不再默认运行完整源码测试，不要求源码验证 notes；Jenkins 继续执行构建、同制品运行时验收和私库发布，不使用 GitHub Actions。可选测试如实记录，未执行不能声称通过。下文历史门禁仅说明当时状态，现行规则以 PK-017 为准。

- 2026-10-15 在 `codex/v0.2.16-pk.1` 合并上游 `v0.2.16`（`01842576b3d5c1e2545b5a99ad555d36e28ed306`），增加 PK-028；逐项依据见 [0.2.16 合并核对](docs/PK_UPSTREAM_0.2.16_REVIEW.md)。最终源码验证以绑定提交的 notes 及服务器原报告为准，发行与部署另行记录。
- 2026-10-16 用户明确要求“所有代码都回退到 v0.2.16-pk.1”。从 `d1b1dbf650b69292cb3998a9f1d32821c769dfd2` 逆序撤销 pk.2～pk.5 的四次提交，源码、原有测试和验证入口恢复到私库标签 `v0.2.16-pk.1`（`f0671148bc54d87a95623035e23a94ad5c74be0e`）；仅本清单补充撤销记录。无新增二开；PK-001～028 的实现与目标标签一致，PK-029/030 按下文退出。源码提交以本次隔离服务器完整验证报告及绑定 notes 为准，不沿用旧版报告冒充本次执行；本次不发布、不部署、不改业务数据。

## 功能索引

2026-10-19 在唯一目标分支 `codex/v0.2.17-pk.1` 合并上游 `v0.2.17`（`060d0160417782cd962a0c7f460f6cd4565d4916`），合并前私库为 `82689422e5bb1444769265534265f570fed274a2`。无新增二开，PK-029/030 继续保持已授权移除；全部有效 ID 的静态核对及源码验证要求见 [0.2.17 合并记录](docs/PK_UPSTREAM_0.2.17_REVIEW.md)，普通 OpenAI Key 计费边界见 [专项审计](docs/PK_OPENAI_KEY_BILLING_AUDIT_0.2.17.md)。本次合并不代表正式发行或线上更新，动态结果只以当前 tree 的服务器原报告及最终 commit notes 为准。

| ID | 二开功能 | 必须保留的重点 |
|---|---|---|
| PK-001 | 账号缓存转换设置 | 新增/编辑、默认值、倍率 0、extra 保存与校验 |
| PK-002 | 缓存转换算法 | decimal 向下取整、溢出检查、创建明细清零 |
| PK-003 | 费用与额度结算 | 原始输入先拆分、转换一次、各账务口径一致 |
| PK-004 | 长上下文与账号统计 | 原始档位、独立倍率、原始/客户统计口径 |
| PK-005 | 下游 usage 改写 | JSON/SSE 无损、别名/显式 0、原始用量保留 |
| PK-006 | 多协议与通道接入 | Responses/Chat/Messages、直通、桥接、WS、图片、embeddings |
| PK-007 | 首字设置 | 独立页签、默认关闭、随机范围、局部保存与缓存 |
| PK-008 | 首字提前响应 | 注释/ping/空格三模式、协议边界、禁用时仍统计首包 |
| PK-009 | 双计时与持久化 | 首包和原上游计时分开、异步传递、所有 SQL 路径 |
| PK-010 | 首字显示与权限 | 用户原文案、管理员上游指标、导出和监控不泄露 |
| PK-011 | 心跳与排队/换号 | 注释/ping不占正文提交判定，空格算正文，周期保活不丢 |
| PK-012 | 提前响应后的错误与并发 | 协议终止事件、响应头隔离、写锁、停止与 context 生命周期 |
| PK-013 | PK版本标识与排序 | “版本号 + PK出版”、pk 修订按数值比较 |
| PK-014 | 私库更新与恢复来源 | 私库/私有镜像、只读凭据、无官方回退路径 |
| PK-015 | 更新下载和文件替换 | 资产授权、跨域去凭据、SHA256、重复更新固定路径 |
| PK-016 | Docker网页更新持久化 | 数据卷可执行文件、种子哈希、镜像回退边界 |
| PK-017 | 私有发行与部署入口 | 正式标签、Jenkins 打包、同制品运行时验收、私库发布、amd64 |
| PK-018 | 新实例启动并发保护 | 可跳过全局启动清理，继续清理过期槽位 |
| PK-019 | 后台启动与停止交接 | 20个受控后台、普通文件激活、只启动一次、停止安全 |
| PK-020 | 切换与恢复运维流程 | 保护端口/数据/密钥、请求排空、后台单实例交接 |
| PK-021 | 二开登记与项目约束 | Git持久化入口、唯一清单、增量登记、逐项合并审查 |
| PK-022 | 币安 UID USDT 自助充值 | 完整五位金额乘订单倍率、账户加密、流水去重、过期回查、订单权限 |
| PK-023 | 管理员每日消费页签 | 复用全部使用记录、默认今日全用户、可配置自动刷新、仅管理员 |
| PK-024 | 管理员币安实时账单 | 直接只读查询收支、日期与收款账户筛选、金额原精度、与入账隔离 |
| PK-025 | 币安专用香港出口探测 | 每次操作重新探测、固定成功节点、香港校验、失败关闭、凭据隔离 |
| PK-026 | 耻辱排行榜与手动扣费 | 独立菜单、当天与日期筛选、实时刷新、用户只读、精确倍率、逐条防重复扣费 |
| PK-027 | 安全策略触发自动禁用 | 默认关闭、可配置次数、独立累计、管理员豁免、解禁可重计、鉴权失效 |
| PK-028 | OpenAI 账号 Kilo 桥接 | 独立默认关闭、用户模型映射、账号代理、ultra 转 max、工具参数保真 |
| PK-029 | 账号隐藏计费倍率及下游用量缩放 | 已授权移除；恢复 pk.1 计费与输出，保留历史记录 |
| PK-030 | 原始 Token 趋势统计 | 已授权移除；恢复 pk.1 趋势口径，保留历史数据 |
| PK-031 | DP API 品牌与柔蓝主题 | 渐变标志、统一名称、胶囊导航、移除友链，保留权限、响应式与深色模式 |
| PK-032 | 服务端定时智商测试 | 全分组/类型并发、已选分组页签、保存清空重启、糖果文案/回复、每行6张、专用 Key 轮换、回环调用、最近30条 |

## PK-001

**账号缓存转换设置。** 引入：`6655dd8ca`。

- OpenAI 账号新增/编辑页保留独立缓存转换开关及倍率，默认关闭、倍率 `1`；`0` 和有限非负小数有效。不能合并到分组倍率或账号计费倍率。
- 存储键为 `accounts.extra.cache_conversion_enabled`、`cache_conversion_multiplier`。保留非法类型、负数、NaN/Inf 等校验；普通更新未提交这些键时保留已有值，显式关闭和倍率 0 不被当成缺省。
- 创建、普通更新、局部 extra、批量 API、导入/重新授权的保存入口都要审查；批量 API 支持不等于批量页面已有相应 UI。
- 平台限制为 OpenAI，不限制为 API Key 类型；OAuth 兑换、Codex Session/PAT 导入和批量 RT 创建的接入也要保留。批量 API 预检目标存在且属于 OpenAI，保存后继续刷新调度快照；局部 extra 保留 JSONB 合并语义。
- 入口：`frontend/src/components/account/{CreateAccountModal,EditAccountModal}.vue`、`frontend/src/types/index.ts`、中英 `admin/accounts.ts`；`backend/internal/service/{account_cache_conversion_validation,admin_account}.go` 及账号 repository 的 extra 更新逻辑。
- 验证：设置往返、缺字段保留、显式 false/0 和非法倍率；已有 `TestPKCacheConversionSettings`。重导边界见文末。

- 2026-10-15 新增同平台的 PK-028 独立开关，缓存转换默认值、倍率0及保存行为保持；创建/编辑现有回归覆盖两个开关独立保存。

## PK-002

**缓存转换算法。** 引入：`6655dd8ca`；输出缓冲写入整理：`74c372bcc`。

```text
新缓存读取 = 原缓存读取 + floor(原缓存创建 × 转换倍率)
新缓存创建 = 0
```

- 使用 decimal 计算、向下取整及 int32 范围检查；不能改成不精确的浮点四舍五入。
- 原读取量不再乘转换倍率，普通输入和输出不直接受此倍率影响。缓存创建总量、5m/1h 明细转换后清零；倍率 0 仍清零创建量，但不新增读取量。
- 开关关闭保持原行为。计费和出口分别使用原始 usage 各计算一次，不对已经转换的 usage 再转换。
- 入口：`backend/internal/pkg/cacheconversion/usage.go`、`backend/internal/service/{openai_cache_conversion,openai_cache_conversion_output}.go`。
- 验证：原读取存在、倍率 `0/0.5/1`、小数边界、关闭、非法输入与溢出；已有 `TestPKCacheConversionDisabledPreservesOriginal`、`TestPKCacheConversionBillingTokens`。溢出错误的结算边界见文末。

## PK-003

**费用与额度统一结算。** 引入：`6655dd8ca`。

- 先从原始上游 usage 拆出普通输入，再转换缓存并定价；不能将转换后减少的缓存量误算为普通输入，再收一次费用。
- usage 记录、余额、订阅额度、API Key 额度/限速额度及账号额度使用同一转换结果；开启/关闭的两条路径都保留。
- 2026-10-08 合入上游 v0.2.14 的在途请求 API Key 删除结算修复：共享 `usage_billing_repo.go` 仅在该 Key 已不存在时跳过它自身的额度/限速持久化，用户余额或订阅、账号额度和幂等结算继续按原转换结果执行；真实数据库错误仍回滚。预留与实扣口径不变。上游删除 Key 的余额回归保留，转换倍率与删除 Key、订阅、账号额度的组合仍待后续获准动态验证，本次只作静态核对。
- 下游展示改写不反向污染上游原始计费证据，重试、桥接、stream usage 收集也不能重复转换或漏记。
- 2026-10-02 合入上游余额在途预留：预留只占用 Redis 中的请求额度，不直接扣款；最终账务仍用转换后的 `ActualCost`。请求及异步计费共同持有预留，计费先同步扣余额缓存，再释放预留，异常/丢弃/panic 的引用也须归还。
- 预留发生在选账号之前，以请求体估算普通输入与输出上限，不读取选定账号的缓存转换倍率或真实 usage，也不精确覆盖服务档位与长上下文倍率。预估可能高于或低于最终账单；低余额并发可能比旧版更早被拒绝。不能把该上游防并发机制描述为与缓存转换实扣严格相等，不能用预留金额替换实扣。开关与参数仍由上游 `billing.inflight_reservation` 控制，默认开启。
- `RecordUsage` 已收到原始 usage 但转换溢出时，保留原始 token 证据、`ActualCost=0`、跳过后续扣费并返回错误；不能默默改收原价。上游出口提前返回错误而无法到达此分支的缺口见文末。
- 入口：`backend/internal/service/{openai_gateway_usage,billing_service,openai_cache_conversion,openai_cache_conversion_output}.go` 及各转发结果的 usage 传递。
- 验证：普通输入/读取/创建/输出拆分、费用及各额度结果、关闭时原账务。已有 `openai_gateway_usage_integrity_test.go`；合并上游余额预留等新机制时必须另查预估与实扣是否使用兼容口径。
- 2026-10-02 在该既有文件扩展 `TestPKCacheConversionInflightBillingPreservesOriginalContext`：倍率0/0.5/30与原始上下文272000/272001组合，检查预留独立、异步实扣前持有、各额度统一、原始上下文选档以及幂等释放；候选与正式发行CI均纳入race选择。
- 上游 `TestInflightEstimate_AccountMappingNoDBAndBoundedMemory` 在同一测试二进制的独立子进程内测量，避免整个 service 测试进程的残留状态混入堆内存差值；保留 100 次预热、20000 次随机模型请求、数据库/快照断言和 8 MiB 阈值，并确保被测服务存活至末次采样。子进程失败仍使原测试失败，不改变生产预留逻辑。

## PK-004

**长上下文档位与账号成本统计。** 引入：`6655dd8ca`。

- 保留 `UsageTokens.OriginalContextTokens` 和 `InputContextTokens()`，价格档位依据转换前原始上下文，不能使用转换后缩小/放大的 token 总数选档。
- 这项适用于 token 阶梯、按次计费上下文档位和整会话长上下文倍率；保护的是二开转换后的兼容语义，不把整套上游定价功能当作 PK 新增。
- 账号/分组倍率与缓存转换倍率相互独立。
- 自定义账号价格统计及模型价格文件回退使用原始 token；启用“将客户定价应用于账号统计”时使用转换后、客户倍率前的 `TotalCost`。不能在合并中把两个分支统一成另一种算法。
- 入口：`backend/internal/service/billing_service.go`、`openai_gateway_usage.go` 中的 usage 构造、价格选择与账号统计分支。
- 验证：原始长上下文恰好跨阶梯、转换倍率 0/小数/大于1、两种账号统计价格分支。现有用例不能代替所有模型价格及预留机制的场景验收。
- 2026-10-02 上游新增 `gpt-6.1-sol` 的 272000 原始上下文阈值，继续使用 `InputContextTokens()` 选档；转换后 token 数量跨过阈值不得改变该判断。组合回归扩展在原有 `openai_gateway_usage_integrity_test.go`，按 PK-017 的提交前源码验证入口执行并记录真实环境。

- 2026-10-15 兼容上游 v0.2.16：实际 OAuth 类凭据及最终 GPT-6.1 Sol 模型决定模型长上下文豁免，自定义区间仍按原始上下文选档；API Key 不继承订阅豁免。账号原始 token 统计与转换溢出零扣费分支保留。

## PK-005

**下游 usage 与无损输出。** 引入：`6655dd8ca`。

- 开启缓存转换时，下游实际 JSON/SSE usage 同步改写；关闭时保持原输出路径。
- OpenAI 输入总量和 `total_tokens` 按对应协议语义调整；Anthropic `input_tokens` 保持普通输入语义。兼容 usage 字段别名、显式 `0` 和 5m/1h 创建明细。
- 只修改目标 usage 字段，保留模型文本、未知字段、事件类型及 SSE 多行 frame。保留原始 usage 供计费；客户端需要 usage 的场景继续向上游请求 stream usage。
- 保留 canonical 字段与已有别名同步、嵌套显式 0 优先、Anthropic 5m/1h 创建量回退。typed Responses/Chat 序列化不能让 `omitempty` 丢掉显式 0；JSON 改写后移除旧 `Content-Length`，SSE 保留注释、空行及 `[DONE]`。
- 客户端未请求 `include_usage` 不应丢弃已经收到的上游 usage；原始 Chat 强制向上游请求 stream usage，Anthropic→Chat 发现真实 usage 后保留下游发送路径。
- 入口：`backend/internal/service/openai_cache_conversion_output.go`、`openai_gateway_{response_handling,passthrough,cc_pipeline}.go` 及 PK-006 接入点。
- 验证：已有 `TestPKCacheConversionJSONKeepsPayloadAndOriginalUsage`、`TestPKCacheConversionSSEPreservesTextAndFraming`；增加协议或上游字段时逐个检查原始数据与改写结果。

## PK-006

**跨协议和通道接入。** 引入：`6655dd8ca`。

必须保留以下已经接入的转换路径，不能只保留转换模块本身：

| 通道 | 主要入口（均在 `backend/internal/service/`） |
|---|---|
| Responses、Chat、Messages JSON/SSE | `openai_gateway_response_handling.go`、`openai_gateway_chat_completions*.go`、`openai_gateway_messages*.go` |
| Responses/Chat/Messages 兼容桥接和回退 | `openai_gateway_responses_anthropic_native.go`、`openai_gateway_responses_chat_fallback.go`、各 `*_anthropic_native.go`、`*_chat_fallback.go`、`openai_gateway_cc_pipeline.go` |
| 直通 | `openai_gateway_passthrough.go`、`openai_gateway_chat_completions_raw.go` |
| WebSocket 文本帧和协议适配 | `openai_ws_forwarder_ingress.go`、`openai_ws_forwarder_v2.go`、`openai_ws_v2_passthrough_adapter.go` |
| 已接入的图片出口 | `openai_images.go`、`openai_images_direct.go`、`openai_images_responses.go` |
| Embeddings | `openai_embeddings.go` |

验证要覆盖下游实际 usage 与内部账单两侧。当前 API Key 号池是历史真实验收范围；这不授权合并时移除已有其他路径。WS 二进制帧、部分 OAuth 图片合成路径的边界见文末，不能声称所有图片或 WS 都已改写。

2026-10-02 空格首字模式在既有 handler、service 与 Grok HTTP transport 的自动重试/协议回退入口增加同一请求门控，具体见 PK-011。仅空格正文已提交时拒绝重试，comment/ping 的转换与回退行为保留；真实 usage 仍走原缓存转换与账务路径。

- 2026-10-15 Kilo 接入既有 HTTP Responses 请求构造器，普通/透传及 Chat/Messages 兼容入口继续走现有 usage/计费/首字链；不新增网络客户端。该桥接不支持上游 WebSocket，启用时强制 HTTP，详见 PK-028。

## PK-007

**独立首字设置与配置链路。** 引入：`54b4372c2`；后续完善：`41958f4d4`、`a6123c20c`。

- 管理员系统设置保留独立“首字设置”页签、开关及随机时间上下限；默认关闭，范围默认 1–2 秒。界面按秒输入（精度 0.001），后端按毫秒保存。
- 键：`stream_early_heartbeat_enabled`、`stream_early_heartbeat_min_delay_ms`、`stream_early_heartbeat_max_delay_ms`；必须满足 `0 ≤ min ≤ max ≤ 30000`，非法范围不写入。
- 2026-10-02 增加 `stream_early_heartbeat_mode`：`comment` 为默认/旧配置行为，`downstream_ping` 为显式选择的下游兼容模式。缺字段保留，显式非法值/空字符串/null/错误类型拒绝；坏存储配置安全关闭并回退默认。GET/PUT、审计、设置缓存和前端局部保存同步包含此字段。关闭开关不清除已选模式。
- 2026-10-02 用户进一步要求增加第三种 `space`（空格首字/正文占位），并解释三种模式。它代表普通空格 U+0020，不是空字符串；旧默认及已保存的 comment/ping 选择不变。三种值必须在后端解析/校验、GET/PUT、审计、缓存及前端中英说明中同步维护，关闭开关仍保留所选模式。
- 局部更新缺失字段保留原值，显式 false/0 生效；审计包含新增字段。设置缓存默认60秒，当前进程成功保存后及时失效；读取失败/非法配置采用安全关闭回退。
- 保存失败不能提前替换有效缓存。其他实例按自身缓存刷新，已开始的请求使用启动时取得的设置，不能承诺所有实例和在途请求立即变更。
- 入口：`frontend/src/views/admin/SettingsView.vue`、`frontend/src/api/admin/settings.ts`、类型和中英设置文案；`backend/internal/service/{domain_constants,settings_view,setting_parse,setting_update,setting_gateway_runtime}.go`；`backend/internal/handler/admin/setting_handler*.go` 和 `dto/settings.go`。
- 验证：`TestStreamEarlyHeartbeat*`、`TestUpdateSettingsEarlyHeartbeat*`、`SettingsView.spec.ts` 中设置、秒/毫秒换算及局部更新场景。

## PK-008

**真实提前发出的 SSE 心跳与空格占位。** 引入：`54b4372c2`，完善：`41958f4d4`；2026-10-02 先增加显式结构化 ping 模式，再按用户要求增加空格模式。用户已明确选择保留真实 Responses ID，接受占位可能晚于设定时间。

- 对已接入的 OpenAI 兼容 Responses、Chat、Messages SSE 请求，在完成必要校验后、等待账号/上游时，从请求起始时间计算随机发送时机，真实写出并 flush 无正文的 `: keepalive` 注释。
- 上述注释为默认 `comment` 模式。选择 `downstream_ping` 时，首次定时写出 `data: {"type":"ping"}\n\n`，Messages 另带 `event: ping`；后续周期保活仍为注释。这两种旧模式不注入模型正文、空格、usage、伪造响应ID或修改真实输出。
- `space` 是用户明确要求的第三种正文占位模式，允许额外发送并保留一个 U+0020 空格，不声称可撤回或加快模型生成。Chat 使用合法 assistant 开始块、空格正文块和空 delta 块，后者推出 New API 原生 Chat 转发暂存的上一帧；后续 Chat 标识保持同一网关响应，真实正文、工具参数、usage 与未知字段保留。
- Responses 的空格使用独立 message 的完整 item/content/text 生命周期，保留在终态 output 中；保留上游真实 response.id、真实 item/tool ID、usage 和续聊语义，统一调整占位后真实 output_index 及出口 sequence_number。为避免尚未知真实 ID 时伪造响应，须等真实 response.created 到达且配置时间已到才注入；因此 Responses 空格可能晚于设置时间。模型输出先开始时直接保留真实输出，不为占位延迟业务。
- Messages 不实现空格占位，选择 space 时该请求使用注释保活。Responses 的 JSON Schema/JSON Object 结构化输出也使用注释保活，避免独立空白 message 被 JSON 解析器拒绝；Chat 接口携带 input 且没有 messages 的兼容请求可能返回 Responses SSE，也回退注释以避免混合协议。设置值仍保留 space。非流式、WebSocket、图片和 compact 等原排除范围不变。
- 空格是输出内容，会影响逐字比较、签名及下游缺失 usage 时的文本估算。本站仍使用原上游 usage 计费，不能声称任何下游的估算费用都不受影响；不能用空格伪造真实模型 token。
- New API `1a4166d8` / `v1.0.0-rc.41` 的 HTTP/SSE scanner 接受非空 data，原生 Responses 立即转发，原生 Chat 缓存一帧；Responses→Chat 保留非空空格。须保护其 terminal-only 正文兼容：真实正文只出现在完成快照时，出口补足缺失真实文本事件，避免已有空格让转换器跳过真实内容。此项代码依据与我方回归不同于真实部署链路验收。
- 兼容模式针对原版 Sub2API 的 Responses 默认 `semantic` 和原始 Chat 记时路径；Responses `visible` 仍等待非空内容，跨协议桥接/大请求保护也可能丢弃或缓冲 ping。该 ping 不是标准 Responses/Chat 模型事件，严格 SDK 可能拒绝；下游可能把它当已提交输出并停止失败换号。这些限制须在管理员设置中展示，不声称普遍兼容。
- 2026-10-02 用户接受“下游日志记首字、客户端仍可能等待”的行为并授权发布当前 ping 方案。New API `1a4166d8` / `v1.0.0-rc.41` 的 HTTP/SSE scanner 会对首个非空 data 帧记时；原生 Chat 缓存上一帧，Responses 转 Chat 丢弃 ping，故其日志时间不保证等于客户端收包时间。此项为源码核对，未做 New API 串联动态验收；不引入正文空格。
- 上条记录的是 `0.2.11-pk.1` 的 ping 发布范围。此后用户取消付费串联实测并单独授权新增 space 模式；空格变更按本条新增约束保护，不反向改写旧版或宣称已做线上实测。
- 真实业务正文开始后停止提前心跳；请求快速结束或取消时不得再写。随机数用于调度发送，不能直接作为统计值，也不伪造模型文本。
- 单纯修改 Header 或写出成功响应头不能取消等待心跳；首个心跳设置 SSE/no-cache/`X-Accel-Buffering: no` 并移除 `Content-Length`。真实输出更早到达时立即转发，不等待随机时间。
- 不注入非流式、WebSocket 入站、图片、legacy compact、native V2 compact 等排除路径；上游 WS 转下游 SSE 的既有重连语义保留。
- 适用范围由 `OpenAIGatewayHandler` 及其路由决定（包括复用该处理器的兼容平台），原生 Claude/Gemini handler 不因这项改动自动纳入；新增路由必须重新核对处理器。
- 禁用提前心跳时，已纳入测量的入口仍采集真实首包。服务器写出耗时不等于客户端网络到达耗时，部分 SDK 忽略注释。
- 2026-10-02 下游兼容调查：原版 Sub2API 的 Responses 会忽略注释及 `response.created/in_progress/keepalive`；语义模式与可见内容模式判定不同，空语义事件不能保证所有下游识别。Chat 原始转发会对非 usage-only 的数据块记时。账号测试页的 `_` 是 `AccountTestModal.vue` 的 UI 光标，不是可被协议撤回的模型字符；不得据此宣称注入 `_` 后能无损替换正文。
- 入口：`backend/internal/service/{openai_early_sse_heartbeat,openai_early_sse_space}.go`；`backend/internal/handler/{openai_gateway_handler,openai_chat_completions}.go`。
- 验证：`TestPKEarlyHeartbeat_*`、`TestPKFirstResponse_DisabledHeartbeatStillRecordsBodyAndCopiesContext`，以及获准的隔离远端通过实际反代读取 SSE。
- 空格扩展沿用 `openai_compact_sse_keepalive_test.go`：`TestPKEarlyHeartbeat_Space*` 覆盖 Chat 三帧、真实 Responses ID、terminal-only 正文/工具生命周期、快照字段、CRLF/分片、多行 data、短写、回退与取消。原 comment/ping 回归保留。新增用例由现有 CI/Release 的 `TestPKEarly` 选择器纳入；是否通过须核对本次提交的实际本地结果。

## PK-009

**双计时与全部持久化路径。** 引入：`54b4372c2`，完善：`41958f4d4`。

- `usage_logs.first_response_ms` 独立记录首次成功非空下游正文写出的实测耗时（可为心跳）；原 `first_token_ms` 保存原上游计时，调度/计费不得改用随机值。
- 保留 nullable 字段和真实 `0` 的区分。历史或未接入测量的记录保持 null，不回填原上游指标冒充首包。
- 异步 usage worker 使用独立原子计时状态，通过 `CopyFirstResponseTiming` 传递到 detached context；不能异步持有已回收的 Gin context。
- 只记录第一次实际 body 写出，header-only flush 不计；换号不重置同一个下游请求的首包计时。
- SQL select/scan、单条插入、批量、best-effort、查询和 DTO 均保留字段。插入追加在既有参数末尾，不打乱账务参数位置。
- 迁移 `backend/migrations/241_usage_log_first_response_ms.sql` 增加可空列，保留旧显式 SQL 兼容。当前由 SQL repository 管理该扩展字段；若以后转入 Ent，必须同步生成描述符并验证索引，不能直接往旧 Ent schema 中插字段。
- 入口：`backend/internal/service/{openai_early_sse_heartbeat,openai_early_sse_space,usage_log,openai_gateway_usage,gateway_usage_billing}.go`、handler usage context、`backend/internal/repository/usage_log_repo_{insert,query}.go`。
- 2026-10-02 上游新增 TypeSafe `/v1/systemone` 为非流式独立平台，不注入 SSE 或虚构首包值；其异步用量任务入队前必须快照渠道用量字段，闭包不得再访问原 Gin context。沿用 detached usage context、预留引用和原上游计时，扩展该入口已有回归并纳入候选及正式发布 race。
- 验证：`TestPKFirstResponseSurvivesDetachedUsageContext`、`TestPKUsageFirstResponseInsertPaths`、integration `TestPKUsageFirstResponsePersistence`，以及 scan/insert shape 现有用例。

## PK-010

**首字显示、导出与上游指标权限。** 引入：`54b4372c2`，完善：`41958f4d4`。

- 普通客户文案继续为“首字”，普通 DTO 的 `first_token_ms` 映射实测 `first_response_ms`；没有测量值显示 `-`，不能 fallback 到真实上游指标。
- 管理员使用记录保留“首字 / 上游首字 / 耗时”，管理员 DTO 保留原 `first_token_ms` 并另给 `first_response_ms`；管理员导出分别保留首包和上游首字两列，字段顺序与标题一致。共享表格默认不展示上游，管理页面显式开启。
- 普通用户 API、详情、CSV 与渠道监控都不能绕过权限暴露原上游 TTFT。公开渠道监控去除原 TTFT 分位数并重算不含 TTFT 的健康分数，管理员数据保留。
- 监控的 Snapshot、Trend、Models、Matrix/buckets、Users 都要覆盖，连同 sample count、TTFT health/score 一起清理。隐私映射不随提前心跳开关关闭而恢复；未采集的非流式/WS 等用户首字可能为空。
- 上游计时沿用原 semantic/visible 定义；部分桥接取首上游事件，不承诺等于首段可见文字时间。
- 入口：`backend/internal/handler/dto/{types,mappers}.go`、`backend/internal/service/channel_monitor_v2.go`；`frontend/src/components/admin/usage/UsageTable.vue`、`frontend/src/views/admin/UsageView.vue`、类型和中英 dashboard 文案。
- 2026-10-01 PK-023 在币安菜单嵌入同一管理员使用记录页；嵌入模式沿用管理员 DTO、筛选和首字/上游首字展示权限，独立 `/admin/usage` 入口的原有默认行为保留。
- 验证：`TestPKUsageFirstResponsePrivacy`、`TestPKChannelMonitorPublicLatencyDoesNotExposeUpstreamTTFT`、`UsageTable.spec.ts`。前端该文件的 CI 纳入情况见文末。

## PK-011

**提前心跳与排队、周期保活及换号兼容。** 引入：`54b4372c2`，修正：`41958f4d4`。

- 队列心跳、compact/直通原有保活和提前心跳独立计数；从正文提交判断中分别扣除且不重复扣。只有心跳时仍可按原规则换号/回退。
- 提前控制器与原有心跳共用调度，完整注释/已知 ping 不终止后续周期保活；真实正文、部分或混合业务帧应停止提前心跳，避免插入正文中间。
- `downstream_ping` 模式下，更早的队列/周期注释不消耗待发送的首次数据 ping；外部完整 ping 可以满足首次发送。内部数据 ping 的字节和原注释一样从本层正文提交判断中扣除，不能重复扣外部队列心跳，也不能改变本层安全换号。
- `space` 的占位是正文，实际成功写出后纳入业务已提交判断，不能继续按“只有保活”重试或换号。等待占位前的注释仍按原保活规则处理；comment/ping 的既有字节扣除和安全换号行为不变。这是用户要求正文占位带来的明确边界，不得误用于旧模式。
- space 在占位前发生安全重试时，须在同一写锁内确认尚未写出占位并取消本请求剩余占位，再进行重试；保存设置不变。不能仅靠“转发前后字节数相等”或分离的布尔检查，因为排队/退避期间定时器也可能写出；不能在新尝试中沿用旧 response.id。
- 默认 `comment` 模式的外部排队心跳取消待发送的首次心跳；`downstream_ping` 按上条区分注释与数据 ping。共享控制器仍可接受之后的周期保活，关闭提前开关时也必须成立。只有心跳时 adjusted size 归一为 `-1`；外部注释字节由原调用方计数，writer 不重复累计。
- `Written()` 必须继续表示真实 HTTP 提交状态；只有“是否已输出业务正文、是否允许换号/重试/fallback”的判断使用一致的 adjusted size。Handler 到转发 service 的相关业务判断都要保留，不能遗漏桥接或重试分支，也不能把 HTTP 已提交状态改成未提交。
- 保留首输出超时的安全标记、最多一次安全切号和客户端取消阻止重放；已输出语义正文后，不进行会重复输出/收费的重放。
- 心跳不改变缓存转换、模型正文、usage 和账单，不以排队心跳冒充真实上游首字。
- 上述“心跳不改变正文”适用于 comment/ping。space 只允许登记的一次空格及协议索引/生命周期适配，不得改写真实模型内容、工具参数或原上游计费证据。
- 入口：`backend/internal/service/{openai_early_sse_heartbeat,openai_early_sse_space,openai_compact_sse_keepalive,openai_gateway_forward,openai_gateway_service,openai_gateway_request_body,openai_gateway_chat_completions,openai_gateway_passthrough,openai_gateway_grok}.go`；`backend/internal/handler/gateway_helper.go` 及两个 OpenAI handler；`backend/internal/repository/http_upstream.go` 与 `backend/internal/pkg/ctxkey/` 的请求级重试门控。
- 验证：`TestPKEarlyHeartbeat_AdjustmentCombinesStreamComments`、`ExternalHeartbeatAllowsLaterPeriodicKeepalive`、`OnlyCompleteTransportFramesPreserveController`、`QueueAccountingWithoutController`、`ReusesExistingPassthroughKeepalive` 等同前缀用例。
- 空格重试回归在原有 `openai_gateway_first_output_timeout_test.go` 的 `TestPKEarlySpaceFailover*`、`http_upstream_test.go` 的 `TestPKEarlySpaceGrokTransportFallbackHonorsRetryGate` 和上述 service 回归文件中。覆盖排队期间先写空格、SafeToFailoverAfterWrite 不能越过正文、原子取消未发占位、transport 不重发及 comment/ping 原行为；均纳入现有 race 选择器。

- 2026-10-15 上游新增“结构化 input 被拒绝时回退 Chat”入口：扣除 comment/ping 心跳后判断正文，回退前调用 `PrepareEarlySSESpaceRetry` 原子取消尚未发送的占位；已发送空格则禁止重试。回归在原 `openai_gateway_chat_completions_test.go`。

## PK-012

**提前 HTTP 提交后的错误与并发安全。** 引入：`54b4372c2`，修正：`41958f4d4`。

- 未发送正文的快速错误保留原 HTTP 状态和 JSON。心跳已提交 HTTP 200 后，晚到错误使用入站协议对应的 SSE 终止事件：Responses、Chat、Messages 不能混发，也不能在流后追加裸 JSON。
- Header 克隆隔离定时器与业务修改，写锁串行化输出；停止等待定时 goroutine 退出并还原 writer，之后才允许 handler 返回、Gin context 回收。
- timer 不回读变化中的 Gin context；取消、结束、错误及 Hijack 都要停止等待，Stop 可重复调用。错误转换只发送一次终止事件，保留原拟返回状态的 Ops 错误标记。
- 2026-10-02 增加短写保护：底层返回不足长度但未报错时转为 `io.ErrShortWrite` 并停止定时器，不把半帧当完整 ping 或成功错误转换；首包仍只按实际非空写出计时，心跳只累计实际写出字节。
- space 的 SSE 解析须覆盖分片、CRLF、多行 data 及工具调用；定时器不能插进半帧。占位后的序号、终态快照及晚到错误必须使用一致的真实 Responses ID，短写/取消仍停止；格式错误不能静默输出错乱生命周期。已有计时、锁与隐私边界继续保护。
- 保留 Ops 流错误/已提交标记；后到动态上游响应头已无法补发，这是提前响应的边界。
- `Server-Timing` 中间件只在真实 `/api/v1/` UI 路径启用；伪造 UI 请求头不能在模型网关启用并产生并发写入冲突。
- 入口：`backend/internal/service/{openai_early_sse_heartbeat,openai_early_sse_space}.go`、`backend/internal/handler/gateway_helper.go`、`backend/internal/server/middleware/server_timing.go`。
- 验证：`TestPKEarlyHeartbeat_FastErrorKeepsHTTPStatusAndJSON`、`LateJSONErrorUsesProtocolTerminal`、`QueueOnlyLateJSONErrorUsesSSE`、`HeaderMutationDoesNotRaceTimer`、`CancellationStopsPendingTimer`、`StopRestoresWriterAndPendingHeaders`；本地 race 检查与 `TestServerTimingScopesAndRoleGate`。

## PK-013

**PK版本标识与修订排序。** 引入：`85d690928`。

- 页面保留“版本号 + PK出版”。机器版本采用 `上游版本-pk.N`，Git 标签可带 `v`，镜像标签不带 `v`；中文不进入机器版本号。
- 更新判断和回退排序按修订数字比较，`pk.10` 大于 `pk.2`；不能仅比较上游版本或按字典序排序。
- 保留当前回退过滤/授权范围：读取最近15个 Release，过滤草稿、预发布、重复项及不旧于当前的版本，降序最多3个；远程回退目标必须在允许列表中，支持可选 `v` 前缀。这不是完整 SemVer 实现。
- 入口：`frontend/src/components/common/VersionBadge.vue`、中英版本文案、`backend/internal/service/update_service.go`。
- 验证：`TestUpdateServicePrivateRevisionUpdates`、`TestUpdateServicePrivateRevisionRollbackOrder`。其他模块同名版本比较测试不能代替这两个更新器场景。

## PK-014

**私有来源、凭据与缓存隔离。** 引入：`85d690928`、`e67a95f2e`。

- 源码、Release、更新、网页回退和恢复固定使用 `pengkang0322/sub2api`；镜像为私有 `ghcr.io/pengkang0322/sub2api`，不可访问时不回退到官方来源。
- 运行时 `UPDATE_GITHUB_TOKEN` 读取私库 Release 和资产；宿主机 GHCR 登录使用独立的包读取授权。开发者宽权限 token 不复制到生产，不进入 Git/日志。
- 更新缓存带仓库标识；旧官方缓存不得变成 PK 更新来源。
- 通用 GitHub 元数据客户端还服务于 Codex/Claude 版本同步，不能将所有元数据查询误锁为本私库；私库限制针对本应用升级/回退资产。
- 入口：`backend/internal/service/update_service.go`、`backend/internal/repository/github_release_service.go`、`VersionBadge.vue`、各正式 Compose、`.env` 示例、安装/恢复文档。
- 验证：`TestUpdateServicePrivateRepositoryCacheIsolation`、`PrivateCacheRoundTrip` 和 GitHub 客户端授权用例；操作前分别核对仓库与镜像私有状态。

## PK-015

**安全下载、SHA256 与重复更新路径。** 引入：`85d690928`；固定安装路径修复：`0121f7479`。

- 私有资产 API 正确携带凭据；离开受信 GitHub API 来源后去掉授权头。保留 URL 限制、重定向次数、下载大小等检查。
- 资产 API 优先于匿名下载 URL，使用 octet-stream；初始及每跳 URL 都校验准确 HTTPS 主机、本私库资产/下载路径、端口和 userinfo 等。拒绝伪造子域、路径型资产名；重定向最多10跳，归档上限500 MiB、checksum 上限1 MiB。
- Release 必须存在对应架构归档及 `checksums.txt`，资产 API 下载仍使用原资产文件名匹配 SHA256，不能省略校验或使用 API 数字 ID 当文件名。
- 更新/回退在原安装文件所在文件系统内暂存、备份、原子替换，保留恢复路径；重复更新和回退始终写原安装位置，不因运行中可执行文件带 `(deleted)` 变成另一路径。
- 无法可靠解析原路径时停止替换，不能猜测路径继续安装。
- 安装路径在服务创建时解析一次（含符号链接和绝对路径），更新/远程回退/本地 backup 回退共用；保留归档路径穿越、文件类型和解压大小限制。先备份旧程序再放入新程序，第二次 rename 失败尝试恢复。
- 入口：`backend/internal/service/update_service.go`、`backend/internal/repository/github_release_service.go`。
- 验证：`TestGitHubReleaseClientPrivateAsset*`、`TestGitHubReleaseClientRedirectAuthorization`、`TestGitHubReleaseClientPrivateChecksumSizeLimit`、`TestUpdateServicePrivateAssetAPIPreservesChecksumFilename`、`RejectsReleaseWithoutChecksum`、`RepeatedUpdatesAndRollbacksKeepInstallPath`、`ExecutableResolutionFailurePreventsReplacement`（后四项同 `TestUpdateService` 前缀）。

## PK-016

**Docker内网页更新及持久化。** 引入：`85d690928`；镜像回退完善：`c54ac2264`。

- `SUB2API_DOCKER_SELF_UPDATE=true` 时，以普通应用用户运行可写数据卷中的 `/app/data/bin/sub2api`，镜像原始 `/app/sub2api` 保持只读；无需 Docker socket。
- `/app/data/bin/.image-seed.sha256` 记录镜像种子。同镜像重启/重建保留网页更新后的程序；种子不同则原子安装所选镜像二进制，包括旧镜像。
- 同一镜像的普通重建不等于回退。需强制重置时，只对准备恢复的独立副本，或已获准停机后的应用数据处理种子标记，不能删除数据卷。
- 同种子主程序缺失时，可恢复可执行普通 backup；拒绝 runtime 目录/程序/marker 符号链接，校验类型与可写性，同目录 staging 先发布程序再发布 marker。自定义启动命令保持原路径，不强制替换为应用启动。
- 网页下载与“立即重启”分开，`restart: unless-stopped` 负责恢复进程；端口和反代配置不被网页更新修改。单进程重启会断流，不是自动热切。
- 入口：`deploy/docker-entrypoint.sh`、各 Dockerfile、正式 Compose、`VersionBadge.vue`；运行库/资源/entrypoint 变化必须部署对应镜像。
- 验证：本地 `artifact-runtime` 套件中容器启动、同种子保留、换种子恢复检查，以及隔离环境的重复更新/重启；必须确认运行程序版本，不能只看容器镜像标签。

## PK-017

**私有发行与可用部署入口。** 引入：`85d690928`、`e67a95f2e`；文档：`23e5fe663`。

- 2026-10-21 用户明确要求移除完整源码测试和 Git notes 门禁，以后直接交付无 `origin/` 前缀的正式标签。本扩展目标 `v0.2.17-pk.4`，继承 pk.3 首页及 pk.2 全部功能，不移动已推送标签。默认流程为保护项/差异核对 → 提交/合入私库 main → 推送未占用正式 `vX.Y.Z-pk.N` 标签并核验远端 commit → 用户点击 Jenkins `release` → build/inventory/十一项运行时验收/seal/publish；不自动部署生产。
- 新包使用 `validation_profile=pk-artifact-only-v1`，不 fetch/read 源码 notes、不生成虚假的源码通过报告，不要求 `source-validation.json`。制品报告仍为 `pk-artifact-v2`，严格绑定仓库、commit/tree、package_id、版本、二进制/归档/镜像身份及哈希；十一项检查和清理必须真实通过。旧 `pk-source-artifact-v2` 封存包继续按原规则核验源码报告哈希、notes 出处和运行时对源码哈希的绑定；旧 `pk-local-v1` 手动流程继续兼容，不能用新 profile 掩盖旧包证据缺失或篡改。

- 2026-10-08 Jenkins 缓存与打包清理修复：npm/npx、pnpm 内容存储、Go modules/build 使用专用 daemon 的持久命名卷，按 `release` / `manual-release` / `candidate`、工具链、平台及 UID/GID 分隔，不缓存项目 `node_modules`、发行结果或测试证明。卷身份与 local driver/无外部挂载选项必须核对；初始化只修改挂载根目录属主，构建仍非 root 且不挂凭据、socket 或生产数据。保留 frozen lockfile、pnpm 完整性与复制导入/禁用 side-effects cache、Go module verify 及 readonly 编译。首次冷缓存仍下载，后续仅复用有效内容。清理阶段仅恢复 HEAD 已跟踪的 `.keep`，不因源码本就没有该占位而失败，真实 Git 错误仍终止，其他源码变更继续由 seal 拒绝。既有 `test_release_matrix.py` 增加真实临时 Git 场景，覆盖占位不存在/被删、其他变更保留和恢复失败；缓存复用需另用两份干净源码及第二轮断网进行实际验证。

- Jenkins 从指定私库构建，手动选择已审阅的提交/分支或未复用的 PK 标签；同一构建二进制进入 Docker 镜像和 Release 归档。GitHub 保留私有源码、Release 和 GHCR 存储职责，不执行构建或测试。
- `SOURCE_REF` 保留 Git Parameter `PT_BRANCH_TAG` 可搜索分支/标签选择，分支值为 `origin/分支名`，正式标签为 `vX.Y.Z-pk.N`，统一规范化为完整 ref。2026-10-08 默认动作改为 `ACTION=release`：必须选择正式 `refs/tags/vX.Y.Z-pk.N`、属于私库 `main` 历史且版本未占用，拒绝 `COMMIT_SHA` 覆盖；列表中的默认分支不能直接发布。`ACTION=package` 保留分支及 40 位小写 SHA 候选打包、正式标签手动打包，`COMMIT_SHA` 只在该动作覆盖源码选择。候选不进入正式更新列表，被打包源码不得改变加载可信流水线的 SCM 分支。
- 按 2026-10-03 用户追加要求，Jenkins 仅监听服务器 `127.0.0.1:18081`，不开放公网端口；域名反向代理由用户配置。原网页一键更新、版本回退、私库授权下载和 SHA256 校验能力必须保留，迁移只改变构建与验证执行位置。
- 2026-10-03 用户要求 Jenkins 界面使用简体中文：安装 Locale 与简体中文语言包，保存默认语言 `zh_CN`，忽略浏览器语言偏好并关闭用户语言覆盖。首次安装脚本保留同样默认值；不改变应用自身语言、权限或打包发布流程。
- 标签必须指向私库 `main` 历史内的目标提交，已有 Release 不覆盖；发布串行执行，镜像写入来源/版本/revision 标签并核对包私有性，通过独立 Jenkins 发布凭据授权，读取源码使用只读 deploy key；不复制开发者宽权限 token。缺发布凭据时可打包，但不得称为已发布。
- Release 是正式发布而非 prerelease，带 `sub2api_版本_linux_amd64.tar.gz` 和 `checksums.txt`。当前仅发布 `linux/amd64`；Apple/arm64 必须先发布并验证相应私有镜像，不能替换成上游镜像。
- 历史规则（已由 2026-10-21 用户要求替代）：2026-10-08 曾将完整源码测试前移到提交前，并要求通过后才提交、绑定 notes 和发布；该前置现已取消。源码测试入口保留为可选诊断，不自动运行、不把未执行记为通过；仍不在本地编译发行程序。
- 同日用户指定服务器测试，默认以控制端 Python 3.12+/Paramiko 执行 `scripts/test-server.py`；仅上传 HEAD bundle 和暂存树 Git 对象包，远端解包后直接恢复暂存树，保留原始 blob、文件 mode、tree 与 base HEAD，不重新 `git add` 规范化或生成临时 commit。每轮使用独立 UUID、8 CPU/16 GiB 的 DinD 测试环境及目录，工具镜像来自 `deploy/jenkins/Dockerfile.source-tests`，不编译发行程序。SSH 主机指纹必须已核验，凭据文件须 Git 忽略且未跟踪，不传容器；测试源码、数据库/Redis 与夹具隔离，不挂宿主 socket/生产卷或影响生产服务。完成回传报告和原日志，清理本轮容器、匿名 daemon 卷及远端目录。报告注明 `execution_environment=server-isolated`，不得写成本机通过。本机 Linux/WSL 的 test-local 入口仍可用。
- 默认 `local-validation.json` 为 `schema_version=2`、`tests_profile=pk-source-v2`，含九个源码套件 `backend-unit`、`backend-integration`、`backend-race`、`backend-lint`、`frontend`、`shell`、`binance-proxy`、`release-helpers`、`security` 及独立 preflight；保留真实命令、日志 SHA256、前后相同的暂存 tree/工作区快照、base_commit 和执行环境。报告绑定可由 Windows Python 执行，不要求 WSL。
- 选择保存可选源码证据时，可用 `--bind-commit HEAD --report ... --publish-evidence` 将真实通过的原报告绑定到相同 tree、包含 base_commit 的干净最终 commit，并通过私库 `refs/notes/pk-source-validation` 发布。notes 不强制覆盖、不挪用其他提交证据；已绑定报告仅可重绑到同 tree 后代，保留原日志和执行时间。该证据不是新包提交、合入、打标签或正式发布的前置条件。
- 当前正式流程为正式标签/私库来源核对 → build → inventory → 专用 daemon 制品运行时验收 → seal → publish。`pk-artifact-v2` 十一项检查验证同一次归档/镜像、非 root 执行、可写卷、同种子保留、backup 恢复及种子切换等边界。运行时容器不接生产或发布凭据，使用隔离 daemon 和可清理测试卷。完整证据绑定 package_id、二进制/归档/镜像哈希；同次发布原制品，不重新构建、不自动部署生产。
- 旧手动 `package` → 下载同包 → 本地 `--artifact-dir` → 上传报告 → `publish` 仍兼容：`pk-local-v1` 十套件（含 `artifact-runtime`）必须全部通过且 commit/tree/package_id/制品 SHA256 一致。`publish` 可处理已封存但尚未对外发布的包；若已推送镜像、产生草稿或版本被占用，先核查阻挡和日志，不声称自动恢复或覆盖重发。候选不能由此转为正式版。
- 临时测试卷、种子变更夹具和新生成测试文件用完删除，不使用生产数据库/数据卷；仓库原有回归保留。Apple 部署脚本沿用 fake runtime 回归，在 Linux 上执行不等于真实 macOS/Apple runtime 验收。本机缺 WSL Linux 发行版和 Docker 后，用户已改为授权服务器隔离验证；新入口及文档不等于动态通过，实际结果以真实环境报告为准，凭据不进入清单/文档/skill/提交。
- 2026-10-01 币安充值接入：私库 `codex/` 分支和原 `feat/` 分支均生成候选制品；正式发布前增加币安协议、余额事务与并发入账检查。前端 Axios 升至 1.20.0，修复旧 1.18.1 的七项 high 安全告警，保持原有 API 客户端与刷新令牌行为，不为二开新增或扩大审计豁免。上游原有功能的审计策略按 2026-10-08 用户要求完整同步，范围见下条及项目约束第 3 节。
- PK-025 接入时，原 CI 和正式发布增加独立网关源码检查，验证认证、香港地域、目标白名单、每次探测与固定节点、错误及超时；2026-10-21 后该源码检查随可选套件保留，不再单独阻止正式发布。网关运行时行为未改动，脚本随私库源码管理，服务器凭据单独保存。
- 2026-10-02 上游同步分支 `sync/` 同样运行私库候选构建；候选版本从 `backend/cmd/server/VERSION` 读取并追加 `-pk.candidate`，仍不进入正式更新列表。前端关键回归增加上游充值优惠、金额组件、账号优先级、平台/配额及仪表盘测试，保留原有币安、首字与使用记录覆盖；TypeSafe 异步用量上下文回归加入候选和正式发布的 race 检查。
- 2026-10-03 耻辱排行榜接入：候选和正式发布执行关键前端回归，以及 `TestPKCyber*` 的 unit/race 和真实 PostgreSQL integration/race 检查，覆盖 PK-026/027；发布仍不操作生产。
- 原 `install.sh`、`docker-deploy.sh` 的不兼容旧安装入口保持停用。全新部署用私版完整 Compose；已有环境的 `docker-compose.pk.yml` 是 overlay，不能替换整套生产配置。
- 2026-10-08 合入上游 v0.2.14：保留私版停用脚本与部署来源，将新初始化规则同步到私版指南及三语 README。全新自动初始化的空邮箱/密码随机生成，显式邮箱须符合登录格式、密码为 8–72 字节；先检查已有用户并跳过创建，不能重置已有管理员。Compose 邮箱默认值改为空，原私有镜像、token、数据卷和网页更新选项保留。
- 同次依赖合并保留 Axios 1.20.0，接受上游 Vue 3.5.43、source-map-js override 及对应锁文件；前端运行行为待验证。2026-10-08 用户要求上游全部更新合入，并将“不扩大豁免”限定为二开功能，因此两条 xlsx 例外的理由、缓解措施及期限 `2027-01-06` 按上游原样同步；仅适用于上游既有只写导出，不新增或豁免二开特有解析/权限风险。安全套件仍须按实际结果处理，不把静态核对称为审计通过。
- 被保留的脚本 API helper 仅接受本私库 Release 元数据端点，凭据经 stdin 注入，不接受任意 URL/重定向参数；source 后直接调用旧 downloader 也不能绕过停用。Apple restart 必须在停服务前验证显式私有 ARM 镜像配置，不能将 `latest` 当作已支持 ARM。
- 管理员更新/回退/重启保留原认证、审计和系统操作锁；UI 在线升级仅面向正式 release build，候选不能冒充正式升级包。
- [中文部署指南](deploy/PK_DEPLOYMENT_CN.md)、README、[私有镜像说明](deploy/DOCKER.md) 和 [维护文档](PK_MAINTENANCE.md) 随源码保存；新部署使用自己的数据/账号/密钥，程序没有固定服务器 IP 或域名限制。
- 入口：`Jenkinsfile`、`deploy/jenkins/{control.py,package.sh,verify-runtime.py,Dockerfile.source-tests}`、`scripts/test-server.py`、`scripts/test-local.{ps1,py}`、Dockerfile、`deploy/` 下相关脚本、Compose 与文档。验证边界见下文，不能把“发布已完成”当成“线上已更新”。

- 2026-10-15 跟随上游 Go 1.27.2 / golangci-lint 2.14.x，服务器工具镜像更新为 `pk-source-tests:20261015`；上游 Actions 中的源码/部署/security 检查仍由现有九套件执行，关键前端列表合并保留双方条目。
- 2026-10-21 为通过提交前安全门禁，按 Go 官方 2026-10-19 补丁发布将当前 Go 工具链统一为 1.27.3，修复扫描命中的 `GO-2026-6809`（`crypto/x509`）。同步 `backend/go.mod`、三个应用 Dockerfile、源码测试工具镜像、严格 preflight 版本与维护命令；新工具镜像为 `pk-source-tests:go1.27.3-20261021`。不添加漏洞豁免、不关闭扫描，不在本地构建应用发行物，正式发行仍走 Jenkins。
- 同日分支清理后，`deploy/jenkins/bootstrap.groovy` 的初始化 SCM 同步为 `*/main`，与实际 Jenkins 任务一致，不再指向已停用的维护分支。`Jenkinsfile` 的 `origin/` 分支识别、正式标签识别和发布门禁保持不变。
- 同次验证将原有 `TestServerTimingConnectorRecordsDriverCallsWithoutRowLifetime` 放入标准库 `testing/synctest` 虚拟时钟，保留原睡眠时长、应用时间/数据库时间关系、查询计数和 SQL 不泄露断言，避免测试服务器调度延迟造成误失败；不修改生产 SQL 包装器、计时采集或响应头输出。
- 同次前端安全扫描命中 Axios 间接依赖 `follow-redirects 1.16.0` 的两项高危公告（`GHSA-q3jh-wrc6-jgq8`、`GHSA-8r9p-f939-6h3c`）；通过现有 pnpm override 机制固定修复版 `1.16.1` 并同步锁文件，不更改 Axios 版本，不增加或扩大漏洞豁免。

- 2026-10-19 合入 v0.2.17：将原有 `StripePaymentView.spec.ts` 纳入 `FRONTEND_CRITICAL_VITEST`，保证订单本地快照/client_secret 绑定的上游安全修复进入默认源码验收。工具链、九套件、notes 和 Jenkins 私库门禁不变；不扩大审计豁免。

- 2026-10-19 确立的正式标签交接继续保留，2026-10-21 取消其中“完整源码测试及匹配 notes”的前置：交给用户点击 Jenkins `release` 前，推送已审阅的 main 提交和未占用正式标签，核对远端标签剥离后的 commit。工作分支不能代替正式标签；交付 `ACTION=release`、`SOURCE_REF=正式标签`，其他两项留空，不默认替用户点击构建。规则同步 AGENTS、项目约束、维护文档及 Jenkins 说明。

2026-10-23 智商测试接入时，服务器安全检查命中 DOMPurify 3.4.14 的新安全公告，按官方修复版本将前端声明、override 和锁文件同步到 3.4.17；不新增安全豁免、不升级无关依赖。SVG 预览的净化、无脚本沙箱和 CSP 继续独立保留，动态结果记录在本次交接记录。

## PK-018

**旁路实例启动时保护现有并发。** 引入：`66671398d`。

- `SUB2API_SKIP_STARTUP_SLOT_CLEANUP=true` 让新实例跳过启动时的全局槽位/等待计数清理，不能删除旧实例真实在途请求的并发状态。
- 默认未设置时保留原启动行为；跳过全局清理后仍运行过期槽位清理，不能关闭正常过期回收。
- 入口：`backend/internal/service/wire.go` 的 concurrency provider。
- 验证：`TestProvideConcurrencyService_StartupCleanupDefault`、`StartupCleanupExplicitSkip`、`StartupCleanupSkipKeepsExpiredSlotCleanup`（同 provider 测试前缀），以及 stale-process cleanup 现有测试。

## PK-019

**后台启动门和安全停止。** 引入：`66671398d`。

- `SUB2API_BACKGROUND_ACTIVATION_FILE` 未设置时保留同步启动。设置后，仅在该路径出现普通文件时启动等待中的受控后台；目录/符号链接不充当激活标记。
- 激活只执行一次，不靠重启应用交接；激活后删除文件不暂停已运行任务。
- 停止流程先调用 `StopPendingBackgroundStarts()`，取消尚未启动的任务并等待正在执行的启动回调，再停止各服务。`wire.go` 和 `wire_gen.go` 顺序必须一致。
- `BatchImageCleanupService` 保留未启动即停止、立即停止、幂等生命周期保护，不能因启动门等待导致退出死锁。
- worker 关闭捕获的局部 `done` channel，不能改回读取可能被 Stop 清空的 `s.done` 字段。
- 当前21项受控后台：BatchImageCleanup、TokenRefresh、CNProviderBalanceCheck、DashboardAggregation、AccountExpiry、OpenAICodexVersionSync、ClaudeCodeVersionSync、ProxyExpiry、SubscriptionExpiry、OpsMetricsCollector、OpsAggregation、OpsAlertEvaluator、IdempotencyCleanup、ScheduledTestRunner、OpsScheduledReport、PaymentOrderExpiry、ChannelMonitorRunner、ChannelMonitorV2Aggregator、BinanceRechargeService、GeminiCachedContentService、IntelligenceTestService。币安轮询随同原后台交接，仍以数据库唯一约束和事务防止重复入账。2026-10-23 新增的智商测试同样经过启动门，退出时取消模型请求并等待工作结束；其他后台的门控顺序不改变。
- 门控不暂停全部任务，不是数据库只读模式；请求处理、必要记账、缓存失效及已有锁保护的必要工作仍应可用。
- 入口：`backend/internal/service/{background_start_gate,wire,batch_image_cleanup}.go`、`backend/cmd/server/{wire,wire_gen}.go`。
- 验证：六项 `TestBackgroundStartGate_*`，以及 `TestBatchImageCleanupLifecycle_StopWithoutStart`、`ImmediateStop`。

- 2026-10-15 新 Gemini 缓存恢复任务纳入启动门；停止取消数据库/上游调用并等待 worker 完成，未启动/重复/并发停止安全。普通客户端缓存修改仍保留上游脱离客户端取消的语义。回归 `TestPKBackgroundGeminiRecoveryStop` 及上游取消传播回归，加入源码 race 选择器。

## PK-020

**切换、更新与恢复的运维保护。** 引入：`66671398d`、`d3f5c8a53`，属于运维约束，不是自动零中断功能。

- 保留现有 Compose 参数、应用端口、反代、DB、Redis、JWT/TOTP 密钥和数据卷；旁路候选使用独立可写 `/app/data` 副本，不能与旧应用共写程序/激活标记。
- 保留在途请求时：先验证新实例，再由反代 graceful reload 接新连接，保留旧连接及旧后台。真实连接与异步计费排空、旧后台退出后，再激活新实例后台。
- 共享并发为0、固定等待时长、`/health` 成功不证明请求排空；WS 连接及进程内续链另查。没有完整实例排空接口时，不承诺自动迁移或零中断。
- 只有当前用户接受中断时才硬切或使用网页单进程重启。历史硬切授权不用于未来部署。
- 回退先检查迁移兼容与原密钥，使用可信私库版本和明确备份。恢复旧版本需说明功能差异，源码恢复不得扩大为清空业务数据。
- 入口与验收：`PK_MAINTENANCE.md` 的蓝绿、回退、恢复流程，PK-018/019 启动保护，以及实际部署前的授权范围和隔离验证。

## PK-021

**持续登记、项目约束和维护 skill。** 原维护 skill：`e67a95f2e`；统一清单与约束：2026-10-01 本次新增。

- 2026-10-19 将 PK-017 的正式打包交接写入持久化入口。2026-10-21 用户进一步取消完整源码测试与 notes 前置：以后直接完成主分支/正式标签推送并核验，再交付 Jenkins 参数，由用户自行点击构建；不再只推工作分支或要求补 notes。AGENTS、项目约束、维护文档、PR 模板及仓库/本机维护 skill 同步新规则。属于原 ID 扩展，无新增二开，仍不允许改写已发布/已推送正式标签。

- `AGENTS.md` 纳入 Git，入口指向 `PROJECT_CONSTRAINTS.md` 和本清单；私库克隆/恢复后也必须保留。
- 2026-10-21 用户要求全面取消分支名前缀，替代 2026-10-03 的 `codex/` 命名规则：`main` 为正式分支，所有工作分支直接使用 `vX.Y.Z-pk.N`，不允许任何目录式/文本前缀或任务后缀。同一未发布版本复用一个分支与 PR，默认只有一个在途版本；功能、修复、文档、上游同步与并行助手不分别建分支。新建前核对本地/远端分支、在途 PR 和发行标签，已有版本分支优先复用。规则同步 `AGENTS.md`、项目约束、维护文档、PR 模板和仓库/安装版维护 skill；无新增 ID，原合入/发布验收门禁保持。
- 本次按用户授权删除本地与私库的 `codex/v0.2.17-pk.1`、`feat/early-stream-heartbeat`、`sync/upstream-0.2.12` 分支引用；三者均已包含在 `b99ca3ab9825cccfe771d3fbf3df620792c0d0e7` 历史中，无独有提交或开放 PR，Jenkins 实际 SCM 已为 `main`。当前版本保留 `v0.2.17-pk.2`，既有标签与提交历史不改写；作者仓库及 Git 的 `origin/`、`refs/heads/` 等命名空间不属于私库分支名前缀清理范围。GitHub 当前私库套餐不支持仓库 Rulesets，本规则通过项目约束和维护流程执行，不宣称已启用服务端强制拦截。
- 本文件为唯一功能登记表。维护文档、PR模板和 `skills/pk-sub2api-maintenance/` 通过链接/工作区路径读取它，不复制多份可能过期的功能清单。
- 2026-10-08 用户明确要求完整吸收上游新功能、安全修复、依赖和上游自身审计策略；“不扩大豁免”只约束二开新增或修改功能，不阻止上游原有功能的例外更新。共享代码须说明上游范围和二开扩展，不将上游豁免套到二开特有风险。已同步 AGENTS、项目约束、维护文档和维护 skill；不改变私库来源、本地禁令及正式合入/发布门禁。
- 新增二开在同一变更中添加 ID；扩展旧功能更新原 ID；合并上游逐个有效 ID 核对，未登记、存在功能丢失或未解决冲突的受影响项不能合入/发布；源码测试是否执行及结果如实记录，不把可选测试或 notes 重新设为前置门禁。
- 2026-10-02 按用户要求强化每次合并的文本冲突和行为冲突检查，覆盖上游新增/替换入口；Git 无冲突不能替代核对。PK-007～PK-012 的三模式、配置、协议 ID/生命周期、真实计时、usage/工具、错误与换号必须整体审查，PR模板记录逐项依据。
- 历史记录（默认流程及禁令已由下文 2026-10-08 授权替代）：2026-10-03 同步约束、维护流程、PR 模板与 PK-017，当时采用 Jenkins 打包、本地电脑测试，迁移期间禁止本地构建/测试。历史云端报告保持原执行环境，未执行项如实保留，不改写为后续验收通过。
- 已安装 skill 读取当前工作区约束和清单；更新仓库版 skill 时同步实际使用的安装副本。目录丢失从私库恢复，不靠旧记忆或功能标题重写项目。
- 2026-10-03 用户要求将实际发布操作持久化到项目 skill：固定记录 `package` → 同制品本地验收并上传报告 → `publish` → 按授权网页更新，明确正式标签与候选分支区别、`PACKAGE_ID` 复用、报告绑定和失败处理。只有试打包时停在 `package`；发布不重新编译、不自动升级生产。详见 skill 的“发布、更新或恢复”及 `references/private-release.md`；本次为流程文档补充，无新增二开，未执行打包、测试或正式发布。
- 2026-10-08 用户最新要求替代上两条的默认执行顺序与测试禁令：先在用户指定服务器隔离环境（或本机 Linux/WSL）完成九套件源码验证、提交相同 tree 并绑定最终 commit 的 notes，再一次 Jenkins `ACTION=release` 完成构建、十一项制品运行时验收及私库发布，之后按授权网页更新；旧手动流程仅作兼容。约束入口、维护文档、PR 模板、三语 README、Jenkins 说明及仓库/安装版 skill 同步；无新增 ID，扩展 PK-017/021，不改写历史报告。缺环境或失败时不提交、不合入、不发布，明确实际测试环境，不把服务器通过写成本机通过。
- 验证：静态核对 Git 跟踪、入口链接、登记条目与 diff、仓库/安装版 skill 一致性。此机制不等于 GitHub 分支保护或自动语义验证。

## PK-022

**币安 UID USDT 小数点自助充值。** 状态：有效。
引入：2026-10-01 用户要求参考 `多商户版本-部署净版-20260914` 接入，并明确首版仅使用币安 UID 内部转账。
最近变更：2026-10-01 用户明确要求将固定 1:1 扩展为管理员可配置倍率，并显示兑换比例和系统到账金额；保留原 ID，默认倍率仍为 1。

- 独立“币安自助充值”菜单，管理员包含收款账户、充值订单、累计自助充值、每日消费、币安实时账单五个页签；后两项见 PK-023/024。管理员可配置收款账户并查看全部订单/统计；普通用户仅查看自己的订单和累计充值。统计只汇总已完成订单，保留 `total_amount` 的实收 USDT 口径，新增 `total_credited_amount` 统计实际增加的系统余额；总体与每个 Sub2 用户分别显示两种金额，并保留笔数、收款账户筛选和权限边界。
- 独立币安充值账户、订单和到账流水，用户关联现有 Sub2 用户，完整实收金额按订单保存的倍率加入原余额。币安账户未启用时不提供新建入口；管理员可配置多个收款账户，用户历史记录在账户禁用后仍可查。
- 管理员在收款账户配置 `credit_multiplier`，默认 `1`，允许 `0.000001～1000` 的普通正十进制、最多6位小数；拒绝零、负值、科学计数和非法格式。新建缺省为1，更新未提交字段时保留原值，不能因旧客户端省略该字段重置倍率。公开账户只暴露兑换倍率，不暴露币安凭据。
- 订单创建在原收款账户事务锁内保存 `credit_multiplier` 快照和 `system_amount`。计算为完整五位 USDT 应付金额乘快照倍率，以精确十进制按余额精度四舍五入至8位；例如 `10.12345 × 6.7 = 67.82711500`。不得先舍弃识别小数、使用浮点数或套用原支付的两位精度与容差。后续修改账户倍率只影响新订单；旧账户与迁移前订单按倍率1保留原账务，迁移不重算用户余额或已完成充值流水。
- 付款页显示 `1 USDT = 倍率 系统余额`，准确转账金额下方显示 `system_amount`，管理员和普通用户订单列表均增加系统到账金额列。详情与列表使用订单快照，不用账户当前倍率重新计算。`system_amount` 是确认付款后应加的余额，`credited_amount` 只表示实际入账金额，未完成时保持0，不能将预计金额显示成已到账。
- 币安 Key/Secret 使用现有 SecretEncryptor 加密，响应只显示是否已配置；留空保留已保存的密钥。收款 UID 有历史订单后不得修改，订单保存收款身份。禁用账户停止创建新单，已有订单继续回查。凭据只用于只读账单查询，不提供明文读取接口。
- 使用币安 `GET /sapi/v1/pay/transactions`，校准服务时间并签名；只识别正向 USDT 的 C2C/PAY 收入及匹配的接收 UID。只读查单不能相信客户端自报到账。查询达到100条时分时间段补查，限流和网络异常记录为可见查询错误，不显示虚假成功。
- 可选 `BINANCE_RECHARGE_PROXY_URL` 或持久化配置 `binance_recharge.proxy_url` 为此客户端指定 HTTP/HTTPS/SOCKS5 代理，未配置时直连；无效代理配置使启动失败，不静默绕过指定代理。`BINANCE_RECHARGE_PROXY_PREFLIGHT` / `binance_recharge.proxy_preflight` 默认 `false`；开启后必须使用带认证的 HTTP/HTTPS 专用网关，每次查收、探测和实时账单先探测香港节点，规则见 PK-025。其他 HTTP 客户端不继承这两项配置。
- 输入整数 `1～1000000` USDT，系统追加 `0.00001～0.99999` 尾数。同一真实收款 UID 的完整应付金额不跨订单复用，防止旧单迟付认领到新用户；报价由数据库事务及唯一约束保护，耗尽明确拒绝。用户同时最多3个待付单，新建间隔30秒，每日最多20单。
- 币安流水 ID 按真实收款 UID 去重；订单、已使用 balance 充值记录、`users.balance`、`users.total_recharged` 与到账流水同一数据库事务提交，使用同一个订单快照系统金额。实收 USDT 仍按原五位整数单位匹配、留存，币安原始账单不乘倍率。并发、重试和进程重启不能重复加款；提交后失效用户鉴权及余额缓存。新充值须在原用户余额流水中可查。
- 自动轮询覆盖最近24小时的到账流水，包括到期订单；币安交易时间必须在原订单有效期内，接口延迟发现的有效付款仍可入账。超时付款及无法准确匹配的情况保留供管理员核对，不盲目认领。重新查单按订单创建时间至到期后24小时的窗口读取真实历史流水，受币安可查询历史范围限制。
- 新增轮询纳入 PK-019 启动门、停止等待及多实例查询互斥；普通支付、缓存转换、模型计费与首字行为不改变。
- 2026-10-02 上游普通充值赠送/折扣仅使用 `payment_orders` 和普通支付报价，币安继续使用自己的倍率快照与独立事务，不叠加该赠送或折扣。新增上游两份 `241_*` 迁移与首包迁移文件名不同；迁移器按完整文件名和校验和跟踪，保留已发行迁移名称及内容。
- 2026-10-08 上游 EasyPay 回调参数白名单及普通支付 return URL 去用户 query 修复只影响普通支付路径；币安的签名账单查询、订单倍率、金额匹配和入账事务保持独立，不复用 EasyPay 回调。此次没有数据库迁移变更；静态核对不代表真实充值验收。
- 入口：`backend/internal/pkg/binancepay/`、`backend/internal/service/binance_recharge*.go`、`backend/internal/handler/binance_recharge*.go`、`backend/internal/server/routes/binance_recharge.go`、`backend/migrations/242_binance_recharge.sql` / `243_binance_recharge_multiplier.sql`，以及前端币安充值 API/类型、用户与管理页面和导航。
- 验证：在已有支付协议测试、用户 repository 集成测试和支付前端测试中扩展 `TestPKBinance*` 场景；CI 与正式发布覆盖五位精度、倍率与8位舍入、配置缺省/更新保留、订单快照、旧数据迁移、签名/账单过滤、并发重复到账、多倍率统计、历史金额不复用、过期回查与权限。前端原有 `PaymentView.spec.ts` / `payment.spec.ts` 检查配置保存、兑换比例、订单金额与统计口径。按 PK-017 的提交前源码验证入口执行并记录真实环境，发行构建仅在 Jenkins 进行。模拟账单与真实 PostgreSQL 验证不等同于真实币安转账验收。
- 倍率回归入口：原有 `backend/internal/service/wire_test.go` 的 `TestPKBinanceCreditMultiplierValidation`、`backend/internal/repository/user_repo_integration_test.go` 的 `TestPKBinanceMultiplierCreditsFullUSDTWithEightDecimalRounding` / `SnapshotSurvivesAccountChangesAndConcurrentChecks` / `RejectsOldSettlementAtomically`（后三项同 `TestPKBinanceMultiplier` 前缀），以及原有 `migrations_schema_integration_test.go` 的 `TestPKBinanceMultiplierMigrationPreservesLegacyDataAndDefaults`。保留既有重复入账、状态/权限和实时账单只读断言。
- 边界：首版仅 UID 内部转账，不支持链上地址、订阅购买、原支付倍率/手续费、自动退款或邀请返利。有关联订单的异常展示为待核对；无归属到账流水保存在数据库，首版没有独立流水浏览或人工认领入账接口。与其他独立系统共用相同收款 UID 时，小数订单无法识别付款意图，应使用专用收款账户。历史金额占用记录必须保留，不得通过删除订单回收已使用报价。
- 倍率迁移的回退边界：订单倍率在旧数据回填为1后移除数据库默认值，新程序必须显式保存快照；`system_amount` 由数据库按原金额和快照生成，已完成状态要求实际 `credited_amount` 与其一致。pk.9 及更早程序缺少新字段，新建会失败，旧结算 SQL 会被约束拒绝、整笔账务事务回滚；不能静默按1:1少加款。旧版本也不识别新的显示/统计口径，不能当作倍率功能可用的回退版本。迁移不会因程序回退撤销，恢复自助充值须使用支持本迁移的私库版本。

## PK-023

**管理员每日消费页签。** 状态：有效。
引入：2026-10-01 用户要求在币安自助充值菜单增加每日消费，复用现有全部使用记录页面。

- 仅管理员可见并通过原管理员使用记录 API 取数，普通用户不能访问全局消费记录。默认选择今天、所有用户，保留原页面的筛选、汇总、详情和导出能力。
- 展示的是使用记录中的实际消费，不按资金充值来源归属或过滤。页面提示明确包含所有充值渠道余额产生的消费，包括自助充值和手动充值；不得将这些数值标成“仅币安充值的消费”。
- 默认每5秒自动刷新，可关闭或输入刷新秒数；切换页签/卸载后停止定时任务，刷新不得造成重叠请求堆积。保留现有手动刷新和筛选行为。
- 复用 `frontend/src/views/admin/UsageView.vue` 的嵌入模式和 `frontend/src/components/admin/usage/AdminDailyUsage.vue`；原 `/admin/usage` 页面默认行为与 PK-010 管理员首字权限保持。
- 验证：本地前端类型检查、既有使用记录测试及新增在已有测试文件中的嵌入/日期/刷新生命周期断言；发行构建仅在 Jenkins 进行。

## PK-024

**币安实时账单。** 状态：有效。
引入：2026-10-01 用户要求迁入参考项目实时账单，并限定管理员访问。

- 管理员在币安自助充值菜单选择收款账户和日期区间，默认今天，直接查询币安 Pay 账户流水。展示收支方向、交易类型、币种、币安原精度金额、交易时间、交易号和详情；不将账单金额强行截成自助订单的五位精度。
- 实时账单包括币安返回的收支与各类交易，不能用仅包含 USDT C2C/PAY 收入的自动入账过滤结果充当完整账单。首版范围是 UID/Pay 账单，不显示链上充值接口。
- 查询只读，不创建本地充值订单、不认领交易、不修改余额。只有 PK-022 的校验与事务可以自动入账；后台权限在服务端验证，不仅隐藏页签。
- 保留查询时间范围校验、100条上限及可能还有更多结果的明确提示、API 限流退避与请求错误；不以截断结果声称账单完整。管理员只能查询已经配置并加密保存凭据的收款账户，响应不回传凭据或签名URL。
- 账单与充值查收共享数据库 `api_locked_until` 租约及 `poll_token` 校验，跨实例互斥；账单另有 `bills_next_query_at` 冷却，到期订单优先查收。成功读账单不修改充值查收计划，币安限流则同步延后两类查询。
- 开启 PK-025 后，每次实时账单操作先重新探测香港出口，再用同一出口校时并读取账单；探测失败不查询币安、不改余额，也不回退直连。
- 入口：`backend/internal/pkg/binancepay/` 的账单查询、币安充值 service/handler/admin routes、前端 `BinanceLiveBills.vue` 与币安 API/类型和管理员菜单。
- 验证：在原有支付协议和前端测试文件扩展账单收支/小数、日期校验、上限提示、只读权限用例；选择运行 PK-017 的可选源码验证入口时记录真实环境与结果。真实账户账单可用性须在部署配置后核对。

## PK-025

**币安专用香港出口探测。** 状态：有效。
引入：2026-10-01 用户明确要求只让币安相关逻辑使用香港代理，并在每次查询前探测可用节点。

- 仅影响 PK-022/024 的币安客户端。保持其他模型请求、支付渠道、GitHub 更新和系统网络的既有出口；不配置全局代理、TUN 或系统路由。
- 正式配置文件支持 `binance_recharge.proxy_url` 和 `binance_recharge.proxy_preflight`，默认空地址/`false`，兼容既有静态代理；对应的非空环境变量 `BINANCE_RECHARGE_PROXY_URL` / `BINANCE_RECHARGE_PROXY_PREFLIGHT` 优先于文件。开启预探测时必须使用带用户名和密码的 HTTP/HTTPS 网关。空地址、SOCKS 预探测、非法布尔值或代理配置使启动失败，不能静默降级。
- 配置由既有 Viper 加载链在启动时读取。服务器可先在持久化 `/app/data/config.yaml` 写好独立币安配置，再由用户网页升级触发新程序重启后生效；不要求重建容器注入新环境，也不在升级时丢失代理配置。文件配置同样只注入币安客户端，不成为全局代理。
- 每次顶层 `History`、`Bills`、`Probe` 操作调用认证网关 `POST /_binance/preflight`，上限15秒，拒绝重定向、非200、畸形响应和非法令牌。预探测包含在原操作超时及数据库查询租约内；失败作为可见错误交给既有退避机制。
- 网关并行检查最多8个固定节点，允许4轮并发探测，覆盖3个后台账户 worker 并为手动查询保留容量，超限明确返回429。每个候选先以严格 TLS 访问 Cloudflare trace 并确认 `loc=HK`，再确认币安时间接口返回200及有效 `serverTime`；两个检查共享该节点6秒预算。每次重新探测，不用上次成功代替本次证据。全部失败时拒绝操作，不回退到直连或其他地区。
- 成功的随机令牌固定指向一个节点，有效期240秒。应用为本次操作创建独立 transport，校时和全部分页共享该出口，令牌仅放在 CONNECT 头；并发操作不得通过切换全局代理组互相改路由。固定出口中途失败则本次失败，下次操作重新探测。
- 网关只接受经过认证且带有效令牌的 `CONNECT api.binance.com:443`。币安签名和 API 凭据在应用到币安的 TLS 内传输；拒绝其他目标、普通转发、无令牌或过期令牌。节点监听仅在本机，网关只绑定应用可达的私网地址，凭据和订阅保存在服务器受限文件中，不写入源码或日志。
- 入口：`backend/internal/config/config.go`、`backend/internal/pkg/binancepay/{client,proxy_preflight,bills}.go`、`backend/internal/service/{wire,binance_recharge}.go`、`backend/cmd/server/wire_gen.go`、`deploy/binance-proxy/gateway.py`。固定节点及 systemd 运维见该目录说明和 `PK_MAINTENANCE.md`。
- 验证：既有 `backend/internal/payment/crypto_test.go` 的 `TestPKBinanceProxyPreflight*` 覆盖真实模拟 CONNECT/TLS、分页固定出口、并发隔离、失败关闭和取消；既有配置测试覆盖文件读取、环境优先及非法配置。网关及配置验证按 PK-017 的提交前源码验证入口执行并记录真实环境，报告作为 Jenkins 正式发布门禁；生产只做公开币安时间接口及真实出口探测，不用模拟到账冒充真实转账。
- 边界：地域依据 Cloudflare 当前看到的出口，节点名称不作为地域证明；公开时间接口成功不能代替收款账户 API 权限或真实转账验收。订阅节点凭据失效或出口变化时停止该节点使用，需更新服务器节点配置；没有直连兜底。

## PK-026

**耻辱排行榜与管理员手动扣费。** 状态：有效。
引入：2026-10-03 用户要求按“安全策略”使用记录统计破甲排行、日期筛选、实时展示与默认6.7倍率扣款；追加要求独立菜单、普通用户只读、管理员操作，并明确命名“耻辱排行榜”；普通用户标识仅显示前3位，其余脱敏。

- 独立“耻辱排行榜”菜单。已登录用户可查看排名、展示名及次数；普通用户的展示名由后端仅保留前3个 Unicode 字符、其余打码，无用户名时不回退暴露完整用户ID。管理员可查看完整标识、管理字段、调整设置和逐人扣费。普通用户接口不得暴露其他用户的邮箱、余额或扣费控制字段，不能只靠前端隐藏按钮保护写入。
- 唯一统计来源是 `usage_logs.request_type = 4`（`cyber`），每条已入库记录计一次，按次数降序并稳定排序。默认当地今天，日期区间和时区由后端校验；记录异步写入后可见，刷新不触发扣款。默认每5秒刷新，可关闭/调整；卸载停止、请求不重叠，过期日期响应不得覆盖当前筛选。
- 全局扣费倍率持久化，默认 `6.7`，允许 `0.000001～1000`、最多6位小数；服务端校验普通正十进制、精度和范围，金额使用精确十进制并按余额8位精度记录。管理员确认后扣款金额为所选时间内尚未扣费的触发次数乘倍率；已扣过的记录不因切换日期、修改倍率或再次点击而重复扣费。
- 扣费预览冻结用户、时间区间、具体使用记录、次数和倍率，10分钟有效；扣费请求使用预览ID重试。同一使用记录只能产生一次扣费，重复或并发请求不得重复扣余额。预览后新增的触发记录留待下次操作；相互重叠的已失效预览须重新预览，不能悄悄改金额。
- 扣减 `users.balance`、负值 `admin_balance` 原余额流水、扣费批次及逐条使用记录认领在同一数据库事务完成。余额不足整笔拒绝，不部分扣款；不改变累计充值、原模型费用、订阅/API Key/账号额度或上游 usage。成功提交后失效用户鉴权和余额缓存。
- 扣费记录与去重证据独立保存，清理使用记录不能删除扣费证据。排行榜只统计仍保留的使用记录；数据库迁移不回扣历史余额，也不自动扣款。程序回退不撤销新增表或已完成扣款。
- 入口：`backend/migrations/244_security_policy_ranking.sql`、`backend/internal/service/cyber_policy*.go`、管理员与普通用户使用记录 handler/routes、前端独立排行 API/页面和导航。自动禁用计数见 PK-027，不能混同为筛选日期内次数。
- 验证：在已有后端 service/repository/handler 与前端回归文件扩展 `TestPKCyber*` 及排行场景，本地验证金额、权限、日期边界、并发扣款、重试、重叠区间、余额不足回滚与刷新生命周期。当前变更的实际结果见[发布核对记录](docs/PK_SHAME_LEADERBOARD_REVIEW.md)；不在本地编译发行程序，不新增遗留临时测试文件。

## PK-027

**安全策略触发次数自动禁用用户。** 状态：有效。
引入：2026-10-03 用户追加要求可配置触发次数自动禁用账户，并可设置手动解除禁用后重新计数。

- 仅管理员可配置。`auto_disable_enabled` 默认 `false`、`auto_disable_threshold` 默认 `10`（允许1～1000000000的整数）、`reset_on_manual_enable` 默认 `true`；升级不会自动启用禁用规则，不回算历史记录。这里的账户是排行对应的 Sub2 用户，管理员用户始终豁免。
- 自动禁用以启用期间新写入的安全策略使用记录为准，在独立持久化计数上累加，达到阈值将普通用户状态改为 `disabled`。它独立于排行榜日期筛选、是否已扣款和原模型计费；关闭规则期间不累加，关闭/再开启不隐式清空已有计数。修改阈值在后续安全策略记录写入时判定。
- 数据库插入触发器覆盖现有应用对父表的单条、批量和 best-effort 使用记录写入（含父表自动路由到分区），用户行锁串行化并发累计；重复而没有实际插入的记录不累计。使用记录清理不重置该计数，也不恢复用户状态。外部导入必须写父表 `usage_logs`，直接写叶子分区不经过父表的语句级触发器。
- 管理员从已有用户管理手动把 `disabled` 改为 `active` 时，按 `reset_on_manual_enable` 决定清零或保留累计次数。清零只重置自动禁用计数，不删除原使用记录、不改变排行榜历史或扣费去重。保留计数时，下一条安全策略记录可能再次触发禁用。
- 复用已有用户状态变更的鉴权失效 outbox，事务提交后由既有 worker 广播失效并重试；不新增后台服务。禁用阻止随后鉴权的新请求，不主动中止已经开始的请求；usage 异步入库及缓存广播存在短暂延迟，不承诺恰好只允许阈值数量的并发请求。
- 入口：`backend/migrations/245_cyber_policy_auto_disable.sql` 的使用记录/用户状态触发器、PK-026 管理员设置和原用户管理解禁流程；既有鉴权失效 outbox/worker 继续受原启动和停止约束保护。
- 验证：在既有 repository integration 文件扩展 `TestPKCyber*`，覆盖阈值、默认关闭、管理员豁免、并发插入、重复写入、手动解禁两种重计模式和鉴权 outbox；选择运行 PK-017 的可选源码验证入口时记录真实环境与结果。迁移不撤销已禁用状态，旧版回退仍受数据库触发器约束，须先核对已保存开关及管理员可操作入口。


## PK-028

**OpenAI 账号 Kilo 桥接。** 状态：有效；依据：2026-10-15 用户要求接入 `E:\Ai\kilo-test` 的协议转换，并明确模型/代理交由 Sub2API 配置。

- 新增/编辑账号仅 OpenAI 平台显示独立开关，所有 OpenAI 账号类型可保存。`accounts.extra.kilo_bridge_enabled` 为 boolean，旧账号/新建默认 false；省略更新保留旧值，显式 false 关闭；其他平台提交该键或非法类型拒绝。普通更新、局部 extra 和批量 API 均校验并复用原调度刷新。
- 与缓存转换完全独立。开启后强制 HTTP Responses，忽略旧的“不支持 Responses”探测，不自动回退 Chat 上游；下游 Chat/Messages 继续通过原协议适配器。关闭后恢复既有路由设置，不覆盖所存的协议/WS 设置。
- 分组强度策略继续有效：选中 Kilo 账号后，仅对入口未识别的 ultra/persistent 按对应上游等级检查映射、上限和拒绝，保留原请求模型作用域，不对已经处理的普通等级重复映射；超限不绕过原权限规则。
- 模型交由用户账号映射，不新增参考桥接中的 `openai/` 前缀。Kilo 与透传同时开启时同样应用账号映射，调度/上游请求保持一致；不套用 Codex 内置模型别名。既有渠道映射与 compact 专用映射继续有效。
- `reasoning.effort` / `reasoning_effort` 中 ultra、persistent 转 max；其他强度不由桥接修改。按用户最新要求，使用记录通过 requested_reasoning_effort 保留客户端原始强度（ultra 仍显示 Ultra），客户费用和余额/订阅/API Key 额度按此原始强度的配置倍率计算；ultra、persistent 可分别配置，未配置按1倍，不套用 max 倍率。上游请求与管理员实际转发字段 reasoning_effort 保留 max；账号自定义成本规则仍反映实际转发强度和原始 usage。普通账号保留上游既有计费口径。
- function_call 转 custom_tool_call，arguments 原样成为 input；function_call_output 转 custom_tool_call_output，字符串包装为 input_text 数组；配对 call_id/namespace/工具参数及其中的加密子任务保留。对应 fc_/fco_ 项 ID 转 ctc_/ctco_。
- 普通用户的使用记录/API 仅显示请求强度，不提供上游强度或桥接映射说明。Responses JSON/SSE 回显的强度元数据恢复为原始 ultra/persistent；不更改生成正文、工具参数或真实 usage。原请求为 max 时照常显示 max。桥接请求的错误响应和用户错误记录中，如错误文案暴露到 max 的强度/路由映射，改为通用上游失败提示；普通参数错误保持，管理员实际错误诊断保留。
- agent_message 仅保留可读 input_text 并转用户消息；reasoning 移除 encrypted_content 并保留 summary；include 删除 encrypted_content 请求。既有 custom_tool 项保持，不进行字符串递归擦除。
- 复用账号认证、URL 安全校验、代理、超时及重试。API Key 账号 Base URL 可填 `https://api.kilo.ai/api/gateway`，拼接 `/responses`；所选代理为空即现有服务器出口。开关不替换账号凭据类型或 OAuth 的目标服务，不提供独立代理配置。
- 同步Chat/Messages与Messages的custom_tool_call_input.done保留Kilo工具调用输入。响应继续走原 HTTP JSON/SSE、首字计时、usage 改写和费用流程；内部保留原 usage，缓存转换照常。没有新增表或全局设置。
- 入口：`account_kilo_bridge.go`、`openai_kilo_bridge.go`、`admin_account.go`、`upstream_protocol_routing.go`、OpenAI forward/passthrough/chat/messages/request_body/scheduling、`account_test_service.go`，创建/编辑组件与中英说明。
- 验证：扩展原 `openai_gateway_service_test.go`、`openai_gateway_usage_integrity_test.go`、`gateway_reasoning_pricing_test.go`、强度策略/定价及 DTO 回归和两份账号表单 spec，覆盖平台限制、默认值/保存、普通/透传、映射/未映射、代理/直连、工具子任务、ultra/max、同步/流式、分组策略及缓存转换共存；独立倍率下核对客户金额、余额/订阅/API Key 额度和上游成本，用户出口不显示映射。实际执行结果见最终源码报告。未用真实 Kilo 付费凭据作在线生成验收，不声称所有模型/客户端均已实测。

2026-10-19 合入 v0.2.17：普通/透传错误终态同时保留 Kilo 客户端错误脱敏和上游新增的 `created_at`、`sequence_number`；不改变管理员原始诊断、强度计费或 usage。原 `openai_response_model_rewrite_test.go` 新增 `TestPKKiloBridgeSynthesizedFailurePreservesUpstreamMetadata`，覆盖两条实际流式合成终态路径的时间、序号、真实响应 ID、正文、客户端脱敏和内部原错误保留，纳入现有 unit/race 选择器。

同次审计发现的既有边界：强度倍率函数与校验由多类账号共享。第三方兼容 API Key 即使关闭 Kilo，若其 Chat→Responses 上游接受 ultra/persistent 且配置了同名倍率，仍可能使用该 PK 扩展键；真实 OpenAI 常规合法强度不受影响。详见[计费审计](docs/PK_OPENAI_KEY_BILLING_AUDIT_0.2.17.md)。本次登记并保留已有配置，不将这一缺口描述为所有普通账号都严格隔离，也不未经明确范围调整收费。

## PK-029

**账号隐藏计费倍率及下游用量缩放。** 状态：已授权移除。引入：`v0.2.16-pk.2` / `4427e25f8`；下游 Token 缩放扩展：`v0.2.16-pk.5` / `d1b1dbf65`。

- 2026-10-16 按用户要求将全部源码恢复到 `v0.2.16-pk.1`，撤销隐藏倍率设置、扣费桥、账务展示、预留适配、批量图片倍率快照及 HTTP/SSE/WebSocket 用量缩放；恢复 pk.1 的缓存转换、分组/用户倍率和账号成本口径，Kilo 桥接保留。
- 原入口为 `account_hidden_billing.go`、`hidden_usage_output.go`、`internal/pkg/usageoutput`、相关 handler/repository/DTO 及前端设置/展示；实现和新增迁移 `246_hidden_billing_multiplier.sql` 随源码回退，历史内容仍可从原标签读取。
- 本次仅回退源码，不删除数据库列、迁移记录或账号配置，不返还或重算已结算费用。旧代码忽略隐藏倍率配置和追加列；若日后部署旧程序，存量批量图片任务仍按已冻结单价结算，不能把源码回退称为重置全部历史倍率。
- 验证：除本清单外全部跟踪文件须与 pk.1 一致，并执行目标版本完整九套件和 preflight；结果绑定本次最终提交。迁移兼容性为源码核对，不是本次生产降级验收。

## PK-030

**原始 Token 趋势统计。** 状态：已授权移除。引入：`v0.2.16-pk.3` / `13880e18a`；普通用户缺失提示调整：`v0.2.16-pk.4` / `ec35f1e77`。

- 同次授权恢复 pk.1 的图表查询、聚合、DTO 和前端展示，撤销原始 Token 快照及覆盖标记逻辑，也撤销 pk.4 的缺失提示控制；不修改历史使用记录。
- 原入口为 `usage_original_tokens.go`、`usage_log_original_tokens.go`、usage repository/聚合及 `TokenUsageTrend.vue`；新增迁移 `247_usage_original_tokens.sql` 随源码回退。已存在的数据库追加列与迁移记录保留，pk.1 的迁移 runner 不要求删除数据库中多出的 246/247 记录。
- 验证方式与 PK-029 相同；本次不连接生产数据库，不将历史费用或图表口径恢复解释为数据回滚。PK-029、PK-030 的原 ID 永久保留，后续重新引入须明确登记。

## PK-031

**DP API 品牌与用户中心、管理后台柔蓝主题。** 状态：有效。引入依据：2026-10-21 用户提供界面截图，要求浅灰蓝配色、白色胶囊导航，并确认用户中心与管理后台都调整；随后指定 DP API 品牌、渐变标志、全部内置显示文案更名和移除 GitHub 友链。目标版本 `v0.2.17-pk.2`。

- 用户中心与管理后台共享浅灰蓝页面背景、柔蓝强调色、白色胶囊选中导航；按钮、筛选控件和卡片采用一致的圆角、边框与轻阴影。品牌显示规则见下文，不改变业务接口结构、计费算法、权限边界或既有配置存储结构，不新增依赖。
- 默认标志为 `frontend/public/dp-api-logo.png`，采用透明背景的 DP 组合与蓝紫渐变；侧栏、首页、登录注册布局、Key 使用页、模型广场、法律文档页和 favicon 使用该资源，已配置的自定义 Logo 仍优先。图像生成提示保留在 `docs/DP_API_LOGO_PROMPT.md`。不在图片内重复放置站点名称，文字继续由站点设置渲染。
- 同日按用户追加要求，浏览器 favicon 使用从现有 DP 标志等比生成的 `frontend/public/dp-api-favicon.png`（64px）与 `favicon.ico`（16/32/48/64px），不重新绘制品牌。静态入口、无自定义 Logo 以及清空 Logo 后均回退到专用 PNG；`main.ts` 监听公开站点 Logo 的异步加载/变化，`utils/branding.ts` 保留合法自定义图片优先并按真实扩展或 data MIME 声明图标格式，未知格式交给浏览器识别，非法 URL 不替换当前图标。原有 `utils/__tests__/branding.spec.ts` 扩展格式、清空回退和动态替换回归，仍由现有前端关键套件执行。
- GitHub 友链移除入口包括首页/Key 使用页页脚、顶栏用户菜单、版本弹层的发布页外跳和支付设置的 GitHub 文档链接；合规原文入口改为站内页面。保留 GitHub OAuth 登录及必要配置入口、PK-014/015 私库更新与回退请求、手动恢复命令中的真实私库地址和校验链。多语言内置文案及前端使用指引同步品牌；协议、环境变量、持久化键和服务名不因显示更名而改变。
- 原 `views/admin/__tests__/SettingsView.spec.ts` 的支付说明用例同步上述已授权的 GitHub 友链移除行为：确认外跳链接不存在，同时保留支付说明和商品名称配置的正向断言，不恢复已移除的友链来迁就旧断言。
- 2026-10-21 用户继续指定站点名称为 `DP API`。后端公开/管理员设置读取及 `GetSiteName`、余额/配额通知和统一邮件通知、前端注入配置/缓存、登录注册/验证、首页、模型广场、法律文档页、Key 使用页与浏览器标题统一使用该默认名；空白值或旧内置名 `Sub2API` 在读取时显示为 `DP API`，新实例默认值同样为 `DP API`。非默认自定义站点名优先并保留既有显示规则，不迁移或重写现有数据库记录，不替换 Go 模块/仓库路径、机器协议标识、User-Agent 或用户自定义配置；后续全局显示及合规文档调整见下文。名称规则集中在 `backend/internal/service/setting_features.go` 和 `frontend/src/utils/branding.ts`，`balance_notify_service.go` 的既有共享默认常量、`notification_email_service.go` 的名称读取、设置解析/公开接口及各显示入口复用；统一邮件通知与页面标题保留原有去除首尾空白的规则。
- 同日用户进一步要求全部内置 `Sub2API` 显示文字改为 `DP API`，并移除界面 GitHub 友链/外跳入口。后端包含安装/启动提示、插件兼容提示与 schema 显示标题、模型说明、默认支付商品名、账号测试文案、设备证明错误提示，以及新建 WebAuthn/TOTP 的显示名称；不改插件协议字段、模块名、User-Agent、可执行文件名、既有密钥、认证算法、第三方版权/许可声明或已配置的自定义商品名/前后缀。
- 合规文档 `docs/legal/admin-compliance.{zh,en}.md` 仅作品牌文字修订，责任、义务和条款保持完整；为遵守上游“内容修改需递增版本”的审计规则，版本更新为 `v2026.10.21`。`admin_compliance.go` 与 `frontend/src/stores/adminCompliance.ts` 同步新确认短语和版本；文档链接改为站内 `/legal/admin-compliance`，按站点当前语言展示。原有确认记录保留，旧版本按既有门禁要求管理员重新确认，不自动接受、不删除历史记录、不降低确认或访问要求。既有 `admin_compliance_test.go` 与 `adminCompliance.reset.spec.ts` 覆盖新品牌/站内链接、旧版本需确认与记录保留。
- 合规全文阅读仅在精确的只读 `LegalDocument` 路由 `/legal/admin-compliance` 暂不显示全局确认弹窗，避免全文再次被遮挡；其他法律路径不在此范围，返回控制台立即恢复原确认要求。既有确认状态、历史记录、接受接口及后端访问门禁不变，原 `adminCompliance.reset.spec.ts` 补充全文阅读、路由往返和状态不变回归。
- 主题使用现有布局、公共样式与 Tailwind 配置。保留原有浅色/深色模式切换、主题偏好保存与深色下文字/控件可读性，不增加新的主题配置或持久化字段；状态、错误、禁用及键盘焦点仍须可辨识。
- `AppLayout` 统一提供响应式顶栏高度和主区内边距变量，`TablePageLayout` 与 `CustomPageView` 的全屏高度按同一变量计算，兼容桌面 80px 顶栏及小屏布局；原有表格横向滚动、分页与自定义内容交互不变，对应既有回归纳入关键前端套件。
- 2026-10-21 用户审核通过 Home 设计图，并要求另交新版本且保留 `v0.2.17-pk.2` 全部功能。首页扩展目标为 `v0.2.17-pk.3`，从包含已发布 pk.2 提交 `b99ca3ab9825cccfe771d3fbf3df620792c0d0e7` 的私库 main `fb25bfc0a18c607311f6f7873b0689ad708e8774` 继续开发；不回退或替换 pk.2 的其他业务实现。默认首页改为柔蓝双栏首屏：左侧本地化价值说明与登录/控制台入口，右侧单密钥连接多模型的示意，下方为一键接入、灵活调度和用量说明。沿用站点名称/Logo/副标题/文档地址配置与 URL 清理；自定义非默认副标题完整显示，内置默认副标题使用中英文首页说明。保留 `home_content` 的 URL iframe/HTML 最高优先级和 `compact_home_enabled` 简洁模式，不新增状态接口、虚构指标或免费额度承诺。
- 首页头部与首屏的模型广场入口共同遵守 `FeatureFlags.modelPlaza` 和 `model_plaza_require_auth`；匿名主操作仍指向 `/login`，登录用户与管理员分别进入 `/dashboard`、`/admin/dashboard`。文档未配置或不安全时不显示外链，语言与主题开关、深色和窄屏布局继续保留。既有 `HomeView.compact.spec.ts` 更新旧终端断言并补充默认页跳转、双入口门控、自定义品牌、文档链接和主题持久化回归，纳入 `FRONTEND_CRITICAL_VITEST`；实际验证结果以本次测试服务器报告和浏览器截图为准。本次扩展沿用 PK-031，无新增 ID。
- 2026-10-21 用户指定登录页采用参考截图的左侧品牌文案/旋转刻度盘、右侧登录卡片排布，并明确沿用当前柔蓝主题色。登录页独立使用 `frontend/src/components/layout/LoginLayout.vue`，SVG 刻度盘封装在 `components/auth/LoginDial.vue`；颜色从现有 Tailwind `primary`、`dark` 和 `canvas` 派生，保留 `theme` 偏好键及浅色/深色切换，不使用参考站橙色、名称或业务文案。站点名、Logo、自定义副标题沿用公开设置与 URL 清理，缺省使用 DP API 品牌；不修改共享 `AuthLayout.vue` 的其他认证页面布局。桌面显示品牌区、主题色滚动条与鼠标跟随刻度盘，窄屏优先完整登录表单；减少动态效果偏好、不可见页面和组件卸载时停止对应动画/监听，不生成虚构用量或在线状态。
- 登录改版保留 `views/auth/LoginView.vue` 的邮箱/密码、三种验证码、Passkey、六种 OAuth、登录条款同意/修订门禁、TOTP 二步认证、注册/重置密码开关、邀请信息清理与登录后跳转逻辑；GitHub OAuth 仍作为认证方式保留。仅调整模板、局部样式及密码显隐按钮的可访问名称。原 `LoginView.spec.ts` 扩展公开设置未加载、后台模式、条款、密码显隐和实际登录请求回归，原 `TencentCaptchaActionGate.spec.ts` 同步布局替身；两者与原 `OAuthLoginSections.spec.ts`、`TotpLoginModal.spec.ts` 纳入 `FRONTEND_CRITICAL_VITEST`。通过与否以本次测试服务器报告为准，桌面/移动端及浅色/深色实际截图另行核对；本扩展沿用 PK-031，无新增 ID。
- 用户与管理员的业务菜单、路由、功能开关、简易模式和自定义菜单继续遵循原有可见性与访问权限；仅移除上述已授权的友链入口。保留菜单组展开、滚动位置、72px 侧栏折叠，以及移动端遮罩、菜单展开/关闭和内容布局，不扩大权限。
- 保留 PK-013 的“版本号 + PK出版”标识、管理员版本更新/回退入口及弹层可用性；保留 PK-022 币安自助充值与 PK-026 耻辱排行榜在对应角色下的独立入口、原有操作与权限。PK-010 使用记录指标权限及其余既有二开不因全局样式调整而变化。
- `-version` 输出品牌更新为 DP API；PK-017 的 `deploy/jenkins/verify-runtime.py` 同时识别新品牌与历史 Sub2API 输出，仍严格匹配版本、完整 commit 与构建字段，不放宽制品/来源校验。既有 `test_release_matrix.py` 增加两种品牌与错误版本/提交/名称的回归，随 `release-helpers` 源码套件执行。
- 主要代码入口：`frontend/tailwind.config.js`、`frontend/src/style.css`、`frontend/src/components/layout/AppLayout.vue`、`AppHeader.vue`、`AppSidebar.vue`。公共选择器 `frontend/src/components/common/Select.vue`、`DateRangePicker.vue` 以及复用公共样式的用户/管理员页面属于联动检查范围；筛选值、事件、键盘操作与弹层定位保持原有语义。展开侧栏为280px，主区与 `features/prompt-audit/PromptAuditView.vue` 固定保存栏共用布局宽度变量，折叠时同步至72px，避免遮挡配置开关；原 `PromptAuditView.spec.ts` 纳入关键回归列表。
- 现有回归入口：`frontend/src/components/layout/__tests__/AppSidebar.spec.ts` 检查排行榜菜单、订阅开关、折叠组、滚动位置、自定义图标和版本弹层不被裁剪；`frontend/src/components/user/dashboard/__tests__/UserDashboardStats.spec.ts` 检查平台统计卡片及简易模式。二者继续保留在 `Makefile` 的 `FRONTEND_CRITICAL_VITEST` 中；本次将管理员 `frontend/src/views/admin/__tests__/DashboardView.spec.ts`、公共选择器原有 `Select*.spec.ts`、`DateRangePicker*.spec.ts` 和 `disabledSelectors.spec.ts` 一并纳入，保护筛选、键盘、输入法、禁用态与日期边界。实际结果仍须核对当次源码套件原始日志，不能仅因列入清单即声称通过。
- 验证计划与边界：按 PK-017 在用户指定测试服务器隔离环境运行完整九套件及 preflight，提交相同 tree 后绑定最终 commit 的原报告及 notes；本条登记时尚未以本次报告确认通过，不沿用历史测试结果。补充核对用户/管理员、浅色/深色、桌面展开/72px 折叠/移动端，以及筛选、卡片、菜单和版本弹层；现有源码断言和 jsdom 交互用例不能代替真实浏览器视觉验收，实际结果与未覆盖范围另随交付说明记录。发行构建仍只在 Jenkins，新增临时测试文件在验证后清理。
- 邮件品牌入口还包括 `content_moderation.go` 的内容审计通知、`user_service.go` 的通知邮箱验证码，以及 `auth_email_binding.go`、`auth_oauth_email_flow.go`、`auth_service.go` 的绑定/注册/OAuth验证码与密码重置：默认值统一复用 `defaultSiteName`，直接读库的名称走同一兼容规则；保留通知流程、权限、收件人、验证码、SMTP发件名与原有去空白规则。
- 名称兼容回归扩展于原有 `setting_service_public_test.go`、`setting_service_update_test.go`、`backend/internal/server/api_contract_test.go`、`frontend/src/utils/__tests__/branding.spec.ts`、`stores/__tests__/app.spec.ts` 与 `router/__tests__/title.spec.ts`：核对默认/旧内置名、非默认自定义名、公开/管理员/邮件及内容审计通知读取一致、读取不写库，以及前端注入/请求/缓存与标题一致；API契约保留旧内置名输入并验证新默认名输出。名称需求追加后，先前源码结果不代表最终树通过，须重新执行完整验证。

## PK-032

**服务端定时智商测试。** 状态：有效。引入依据：2026-10-23 用户要求参考本机“鹈鹕测试”，在本站新增 SVG/糖果题菜单；追加总开关、管理员账户生成测试 Key，以及每次一键更新全部撤销旧测试 Key 后重建。目标版本 `v0.2.17-pk.5`。

2026-10-23 追加分组勾选，目标 `v0.2.17-pk.6`：用户只希望为自己选中的分组配置测试 Key，并明确本轮不运行测试。沿用本 ID；本轮仅静态核对，不将 pk.5 的历史测试结果描述为本轮通过。

2026-10-23 继续修订，目标 `v0.2.17-pk.7`：仅展示已选分组；保存配置后清空所有智商测试历史、取消旧任务并按新配置立即重新排队；糖果题展示当次发送文案与最终助手回复；鹈鹕和糖果桌面每行6张；新增安全失败诊断。沿用本 ID，本轮不运行测试或构建。

2026-10-23 用户明确要求“其它分组不需要排队，所有测试并发执行”，并补充按“分组数×2”动态并发，目标 `v0.2.17-pk.8`：取消此前全局2任务与整批等待限制，同时移除智商测试HTTP客户端的2连接上限；所有已选有效分组的已启用测试到期即独立启动。此授权仅改变本功能调度，不改变正常网关的账户/通道并发、权限、计费和审计。沿用本 ID，本轮仅静态核对、不运行测试或构建。

- 菜单采用“SVG / 糖果题 → 已选且有效分组”两层页签。设置区仍列出全部有效分组供勾选，结果页签、默认组、空态、键盘导航和结果请求统一按已生效的 `selected` 过滤。普通用户只读最终作品、糖果合格/不合格及当次题目/最终回复；正在运行仅显示“Agent工作中”，请求失败独立显示“测试失败”。显示真实测试时间及本机单调时钟测量的耗时，不公开推理事件、原始响应流、请求头或上游错误正文。两类结果桌面每行6张，小屏响应式1/2/3列，糖果长文在卡内滚动。
- 设置保存在 `settings.pk_intelligence_test_config`。总开关默认关闭，关闭时隐藏普通用户菜单，直接访问结果/作品接口也被拒绝；管理员始终保留配置入口。SVG、糖果各有独立开关（默认启用，受总开关控制）、完整可编辑提示词、模型、强度、1～1440分钟间隔及分组模型/强度覆盖。默认模型 `gpt-6-astra`、强度 `medium`、间隔5分钟；糖果答案默认21，可随改题配置0～1000000整数。提示词原文发送，不暗中附加额外要求。默认SVG词要求SVG/CSS动画，默认糖果词显式包含最终答案格式。
- 每次成功保存（包括相同配置）在同一事务中更新配置、生成仅后端使用的代次、删除全部智商测试结果并将任务时间置为当前时间；随后取消旧任务、唤醒调度器。关闭总开关或全部类型时只清空并停止。保存与任务领取共用事务锁，旧代次不能再插入历史，旧任务只更新原ID且不能补回已删除结果。保留有效租约直到原worker退出，正常本实例无需等5分钟，健康跨实例按5秒检查取消；崩溃worker仍受最长6分钟旧租约保护，只影响同一分组同一类型的接替，不阻塞其它测试。前端保存成功立即清空列表、取消旧读取并重新加载，迟到的旧响应不能恢复历史。
- 管理员“一键生成 / 更新”每次在数据库事务和互斥锁内，撤销映射表记录的全部旧测试 Key、移除旧凭据，再仅为明确勾选的有效分组重新生成随机 Key，归当前有效管理员账户。名称以 `智商测试-分组名` 开头，凭据为后端专用 `sk-iq-` 前缀；`pk_intelligence_test_keys` 保存分组与真实 Key ID 的对应关系。不按名字接管或删除普通 Key，不从普通用户创建测试 Key。旧 Key 使用软删除和独立加密随机删除标记清除原凭据并保留必要计费历史，避免固定占位值与普通自定义 Key 唯一约束冲突；数据库写入或随机生成失败回滚整批轮换。pk.8轮换在提交前记录旧任务取消句柄，提交后仅取消该快照并唤醒调度，避免并发调度刚启动的新Key请求被迟到取消误停。
- Key区域提供分组复选框、全选、清空和已选数量，点击“一键生成 / 更新”生效，底部普通配置保存不执行 Key 轮换。分组接口的 `selected` 按现有映射回显，独立于 `key_configured` 的当前可用状态；没有映射的新分组默认不勾选，轮询不覆盖未提交的选择。首次分组读取成功前禁用更新，区分读取失败与合法空分组；同步失败或部分分组失败保留选择供重试。
- 更新接口必须显式提交 `group_ids`；缺字段/null拒绝，空数组明确表示撤销全部旧测试 Key且不重建。最多2000个输入ID，正整数去重；同事务核对全部选择仍为未删除的有效分组，非法选择在任何撤销操作前拒绝。更新成功后，未选分组没有Key映射，既有调度JOIN映射和发送前校验会停止其新任务，原有在途取消/跨实例检查保留；Key轮换本身不改总开关、不清空历史，也不新增分组选择配置字段或迁移。pk.7的保存清空及详情迁移独立于Key轮换。
- 分组授权、订阅、Key创建频率/数量、用户余额、模型白名单、内容审计、计费与并发限制继续走既有规则。权限不足的分组明确报告原因，不自动授权订阅或绕过收费；成功轮换后，此类组旧 Key 也撤销。普通分组授权/订阅需由管理员先配置，不能把“已生成”描述为真实模型调用已通过。新增组需手动勾选后更新才加入，已删除或停用组不重建，改名采用当前名称。
- 专用 Key 的通用列表、详情和嵌套使用记录 DTO 同样脱敏，不向浏览器返回原值，不提供输入 Key、地址或手动触发模型的接口。专用 Key 不能通过普通编辑换组；普通 Key 的创建、编辑、删除、收费及输出保持原行为。
- 调度器固定使用 `http://127.0.0.1:{server.port}/v1/responses`，端口取服务端配置，不接受浏览器参数，不使用环境代理、不跟随重定向。专用 Key 在所有网关鉴权入口额外要求真实回环对端地址和进程内生成的一次性随机凭证；不信任转发IP，只有预定的 POST Responses 入口可用。一次性凭证只在内存保存哈希，1分钟失效、使用即删除并移除请求头，日志脱敏包含该头。pk.8将待消费凭证容量扩为8192以覆盖2000组双类型瞬时并发，请求结束/失败/取消后清除未消费凭证；不按Key覆盖凭证，避免同组两种测试互相作废。
- SQL任务状态与租约防多实例重复执行，同组同类型不重叠；所有到期的已选分组/已启用类型并发启动，并发任务数按实际分组数动态计算：N组双类型为2N项，关闭一项时为N项，不写死为8个。智商测试调度器或HTTP客户端不再两项一批。调度扫描独立于模型完成，不因其它慢任务阻塞已到期任务；每项仍按自身间隔运行，单次5分钟超时。停止/关闭/重配取消旧任务，跨实例每5秒检查配置和 Key 轮换；进程异常留下的记录在租约到期后标记失败。只接受完整成功终态，截断、失败和超限不能计为合格；网关已有账户/通道并发与限额继续按正常请求生效。
- 每个功能每个分组最多30条完整结果，插入时同事务删除更旧记录；删组后清理对应结果与调度状态。单作品/最终助手回复最多256KiB，单响应最多4MiB，不保存推理事件或原始流。糖果题在领取任务时保存当次完整提示词快照（最多100000字节），仅在完成时保存最终助手回复；原有合格判定保留。迁移249仅新增有默认值的详情列及长度/终态约束，不修改旧迁移；历史记录缺失的文案、回复与原因明确显示不可还原，不能用当前配置冒充当次题目。原有网关计费与审计记录遵守原保留策略，不因测试结果轮换或配置保存删除账务证据。
- 列表只返回结果元数据，作品和详情通过带登录、功能门控及既有管理面限流的只读JSON接口按需读取；前端仅缓存当前最近30条，切组/类型或保存清空时释放并取消在途读取。错误保存固定分类与最多2048字节的脱敏说明；HTTP仅提取最多32KiB JSON中的指定message字段，不保存原始响应体/请求头，详细说明仅管理员可读。SVG/HTML先净化再放入无脚本、无同源权限的iframe和严格CSP，支持SVG/CSS动画，不执行JavaScript或外部资源。糖果文字采用转义插值，不作为HTML执行。管理员提示词应使用可在该预览方式中展示的作品格式。
- 入口：`backend/internal/service/intelligence_test_*.go`、`backend/internal/handler/intelligence_test_handler.go`、用户/管理员routes、两类API Key鉴权中间件、Key DTO和编辑保护、`backend/migrations/{248_intelligence_tests,249_intelligence_test_details}.sql`、`backend/internal/service/wire.go`及`backend/cmd/server/{wire,wire_gen}.go`；前端 `api/intelligenceTest.ts`、`stores/intelligenceTest.ts`、`views/user/IntelligenceTestView.vue`、`components/intelligenceTest/`、菜单/路由和中英文文案。
- pk.5 验证与边界：用户要求测试后，最后明确改为服务器隔离测试（此前本机前端检查结果单独记录），执行后清理新建临时测试文件；实际范围/结果见交接记录，不把局部源码检查称为完整或生产运行时通过。未连接生产、未实际消耗模型Token；Jenkins发行构建与同制品十一项验收仍由用户触发。分组须具有可用模型和本站Responses兼容能力；原有仅SystemOne平台限制不被绕过。完整范围与静态依据见 [智商测试交接记录](docs/PK_INTELLIGENCE_TEST_REVIEW.md)。

## 验证入口与现有边界

### 已有回归入口

| 范围 | 已有测试/流程 | 使用边界 |
|---|---|---|
| PK-001～006 | `backend/internal/service/openai_gateway_usage_integrity_test.go` 中 `TestPKCacheConversion*` | 覆盖设置、公式及 JSON/SSE 代表场景；不是所有协议端到端验证 |
| PK-007 | `setting_service_update_test.go`、`setting_handler_partial_payload_test.go`、`SettingsView.spec.ts`、`api_contract_test.go` | 包含配置链、局部保存、错误与前端换算 |
| PK-008～012 | `openai_compact_sse_keepalive_test.go`、handler context、DTO、usage repo、channel monitor、Server-Timing 测试 | unit/race 与数据库 integration 分开；候选须检查对应 commit/tree 及真实执行环境的源码报告 |
| PK-010 UI | `frontend/src/components/admin/usage/__tests__/UsageTable.spec.ts` | 币安/每日消费接入时纳入 `FRONTEND_CRITICAL_VITEST`，与 `UsageView.spec.ts` 一同验证嵌入模式和原上游首字权限 |
| PK-013～015 | `update_service_test.go`、`github_release_service_test.go` | 使用更新器自身 PK 修订、授权、路径及校验测试 |
| PK-016～017 | `Jenkinsfile`、`scripts/test-server.py`、`scripts/test-local.py`、现有 `deploy/tests/` 和制品验收流程 | 发布构建不等于生产已部署；测试清单/矩阵改变时同步核查 |
| PK-018～019 | `concurrency_service_test.go`、`wire_test.go`、`batch_image_cleanup_test.go` | 需要门控/停止并发行为证据，不能仅搜索到 env 键即认为有效 |
| PK-020～021 | 维护流程、固定 commit 的隔离验收和静态文档核对 | 提交前源码测试与 Jenkins 发行构建分开；不把历史线上/探测报告当作本次新代码验证 |
| PK-031 | `AppSidebar.spec.ts`、`UserDashboardStats.spec.ts`、管理员 `DashboardView.spec.ts` 与公共选择器既有回归 | 核对实际 `frontend` 执行列表；菜单与交互回归不代替浅色/深色、桌面/移动端的浏览器视觉验收 |

可选完整源码验证的 `backend-integration` 执行数据库 integration 的 `TestPKUsageFirstResponsePersistence`，只有实际通过才能生成通过报告；`backend-race` 保留旧候选/Release 选择器及 PostgreSQL 15 专项。Jenkins 前端发行构建不等于执行了 `frontend` 源码套件。选择运行验证时，应检查真实执行列表并记录服务器隔离环境或本机 Linux/WSL；未执行项明确写未执行，不将服务器结果写成本机结果。2026-10-21 起这些源码检查不再是提交、合入或发布前置，功能保护核对和制品运行时验收仍按 PK-017 执行。

### 已知行为边界

1. Codex Session 重新导入会显式发送默认 `false/1` 覆盖已有缓存转换设置；普通编辑的缺字段保留规则挡不住显式值。批量编辑页面尚无转换 UI，批量 API 已支持。转换按实际选中账号读取配置，没有专门继承影子账号父转换配置的机制。
2. 超大倍率可导致 int32 溢出；部分非流式 Responses/embeddings 路径的 `nil,error` 返回可能丢失已采集的用量证据，不能标为已全面修复。
3. WebSocket 二进制帧保持既有直通范围。部分 OAuth 非 direct 图片的合成出口未接入账号转换，可能原样透传 `response.tool_usage.image_gen` 内的 usage；API Key 图片路径已接入。JSON 扫描固定 usage envelope，未知 provider 嵌套不会自动转换。OAuth 不在本次历史号池真实探测范围内。
4. 提前 HTTP 200 后，错误以 SSE 终止事件表达，晚到动态响应头不能补发；SDK/代理可能忽略或缓冲心跳。真实下游收包须在最终网络链路测量。
5. 原上游“首字”保留已有计时语义，部分协议是第一条事件，并非第一段文字；旧/未测量记录的首包为 null。
6. 网页手动 Docker 回退示例仍使用示例账号/`sub2api` 服务名。实际 Compose 项目、服务名、停机授权与等待方式须按维护文档核实，不能照抄示例宣称无中断回退。
7. 更新器的两次 rename 不是一笔文件系统事务；只保留一份 backup，本地回退会消耗它，也没有新程序启动失败后自动健康回滚。必须验证实际恢复结果。
8. 早发 writer 的自动 JSON 错误转换覆盖单次完整写入；新增分块 JSON 错误输出需要单独审查。
9. 尚无缓存转换专用前端测试、所有协议出口、decimal `100 × 0.29`、溢出结算和转换后按次档位的完整专项覆盖；VersionBadge 也未有 PK 专项测试。已有代表用例不得描述为全面覆盖。

以上边界应在相关变更中逐项处理，不借本次文档登记擅自改变业务。获得对应修复授权并完成验证后，更新原条目与边界记录。

## 新增条目模板

新增独立能力使用下一个未用 ID，当前下一个为 `PK-033`；多项变更并行新增时在合并前消除 ID 冲突，已有 ID 不重用。同步更新索引、有效条目范围和此处的下一个 ID。

```markdown
## PK-NNN

**功能名称。** 状态：有效 / 已授权替代 / 已授权移除。
引入/最近变更：可核对的提交、版本或本次变更说明。

- 用途和必须保持的行为：
- 默认值、配置/存储字段、计算规则、协议和权限范围（按实际填写）：
- 代码与 UI 入口（包含所有调用路径）：
- 验证方式、已有用例及是否纳入 CI：
- 已知边界、历史数据兼容和变更依据：
```

相同功能扩展时补原条目，不能仅增加标题却漏掉后台、存储、协议或验证。未写入清单的新增二开不算完成。
