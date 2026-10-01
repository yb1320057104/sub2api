# PK出版二开功能清单

**本清单是二开功能的唯一登记表，受 [项目约束](PROJECT_CONSTRAINTS.md) 保护。** 新增、扩展、修复二开必须同步登记；合并上游必须逐项核对。清单遗漏不代表可以删除已有功能，发现遗漏应立即补齐。

## 基线与阅读方式

- 上游业务基线：`a60a29549f488a854966aaec9541abbe006cac22`（0.2.10）。首次二开保存：`6655dd8ca`，恢复标签 `pk-baseline-20260930`。
- 本次清点覆盖从该上游基线到私库 `23e5fe663338c86cd0cebbf2f688c8a75dd8c152` 的全部二开提交；应用发行参照 `v0.2.10-pk.7`，另登记本次新增的项目约束机制。此处是代码核对基线，不代表线上当前版本。
- 以下 PK-001 至 PK-025 均为有效保护项。文件路径用于定位，不是完整修改白名单；所有新调用入口、迁移和旁路都要检查。
- 每项说明“必须保持”的行为。文末另列验证入口与已知边界；测试存在、历史验证通过和本次候选通过是三件不同的事。

## 功能索引

| ID | 二开功能 | 必须保留的重点 |
|---|---|---|
| PK-001 | 账号缓存转换设置 | 新增/编辑、默认值、倍率 0、extra 保存与校验 |
| PK-002 | 缓存转换算法 | decimal 向下取整、溢出检查、创建明细清零 |
| PK-003 | 费用与额度结算 | 原始输入先拆分、转换一次、各账务口径一致 |
| PK-004 | 长上下文与账号统计 | 原始档位、独立倍率、原始/客户统计口径 |
| PK-005 | 下游 usage 改写 | JSON/SSE 无损、别名/显式 0、原始用量保留 |
| PK-006 | 多协议与通道接入 | Responses/Chat/Messages、直通、桥接、WS、图片、embeddings |
| PK-007 | 首字设置 | 独立页签、默认关闭、随机范围、局部保存与缓存 |
| PK-008 | 首字提前响应 | 真实发送 SSE 心跳、适用范围、禁用时仍统计首包 |
| PK-009 | 双计时与持久化 | 首包和原上游计时分开、异步传递、所有 SQL 路径 |
| PK-010 | 首字显示与权限 | 用户原文案、管理员上游指标、导出和监控不泄露 |
| PK-011 | 心跳与排队/换号 | 心跳不占正文提交判定、周期保活不丢、费用不变 |
| PK-012 | 提前响应后的错误与并发 | 协议终止事件、响应头隔离、写锁、停止与 context 生命周期 |
| PK-013 | PK版本标识与排序 | “版本号 + PK出版”、pk 修订按数值比较 |
| PK-014 | 私库更新与恢复来源 | 私库/私有镜像、只读凭据、无官方回退路径 |
| PK-015 | 更新下载和文件替换 | 资产授权、跨域去凭据、SHA256、重复更新固定路径 |
| PK-016 | Docker网页更新持久化 | 数据卷可执行文件、种子哈希、镜像回退边界 |
| PK-017 | 私有发行与部署入口 | 同一二进制、正式 Release、云端验证、amd64、文档 |
| PK-018 | 新实例启动并发保护 | 可跳过全局启动清理，继续清理过期槽位 |
| PK-019 | 后台启动与停止交接 | 19个受控后台、普通文件激活、只启动一次、停止安全 |
| PK-020 | 切换与恢复运维流程 | 保护端口/数据/密钥、请求排空、后台单实例交接 |
| PK-021 | 二开登记与项目约束 | Git持久化入口、唯一清单、增量登记、逐项合并审查 |
| PK-022 | 币安 UID USDT 自助充值 | 五位金额完整入账、账户加密、流水去重、过期回查、订单权限 |
| PK-023 | 管理员每日消费页签 | 复用全部使用记录、默认今日全用户、可配置自动刷新、仅管理员 |
| PK-024 | 管理员币安实时账单 | 直接只读查询收支、日期与收款账户筛选、金额原精度、与入账隔离 |
| PK-025 | 币安专用香港出口探测 | 每次操作重新探测、固定成功节点、香港校验、失败关闭、凭据隔离 |

## PK-001

**账号缓存转换设置。** 引入：`6655dd8ca`。

- OpenAI 账号新增/编辑页保留独立缓存转换开关及倍率，默认关闭、倍率 `1`；`0` 和有限非负小数有效。不能合并到分组倍率或账号计费倍率。
- 存储键为 `accounts.extra.cache_conversion_enabled`、`cache_conversion_multiplier`。保留非法类型、负数、NaN/Inf 等校验；普通更新未提交这些键时保留已有值，显式关闭和倍率 0 不被当成缺省。
- 创建、普通更新、局部 extra、批量 API、导入/重新授权的保存入口都要审查；批量 API 支持不等于批量页面已有相应 UI。
- 平台限制为 OpenAI，不限制为 API Key 类型；OAuth 兑换、Codex Session/PAT 导入和批量 RT 创建的接入也要保留。批量 API 预检目标存在且属于 OpenAI，保存后继续刷新调度快照；局部 extra 保留 JSONB 合并语义。
- 入口：`frontend/src/components/account/{CreateAccountModal,EditAccountModal}.vue`、`frontend/src/types/index.ts`、中英 `admin/accounts.ts`；`backend/internal/service/{account_cache_conversion_validation,admin_account}.go` 及账号 repository 的 extra 更新逻辑。
- 验证：设置往返、缺字段保留、显式 false/0 和非法倍率；已有 `TestPKCacheConversionSettings`。重导边界见文末。

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
- 下游展示改写不反向污染上游原始计费证据，重试、桥接、stream usage 收集也不能重复转换或漏记。
- `RecordUsage` 已收到原始 usage 但转换溢出时，保留原始 token 证据、`ActualCost=0`、跳过后续扣费并返回错误；不能默默改收原价。上游出口提前返回错误而无法到达此分支的缺口见文末。
- 入口：`backend/internal/service/{openai_gateway_usage,billing_service,openai_cache_conversion,openai_cache_conversion_output}.go` 及各转发结果的 usage 传递。
- 验证：普通输入/读取/创建/输出拆分、费用及各额度结果、关闭时原账务。已有 `openai_gateway_usage_integrity_test.go`；合并上游余额预留等新机制时必须另查预估与实扣是否使用兼容口径。

## PK-004

**长上下文档位与账号成本统计。** 引入：`6655dd8ca`。

- 保留 `UsageTokens.OriginalContextTokens` 和 `InputContextTokens()`，价格档位依据转换前原始上下文，不能使用转换后缩小/放大的 token 总数选档。
- 这项适用于 token 阶梯、按次计费上下文档位和整会话长上下文倍率；保护的是二开转换后的兼容语义，不把整套上游定价功能当作 PK 新增。
- 账号/分组倍率与缓存转换倍率相互独立。
- 自定义账号价格统计及模型价格文件回退使用原始 token；启用“将客户定价应用于账号统计”时使用转换后、客户倍率前的 `TotalCost`。不能在合并中把两个分支统一成另一种算法。
- 入口：`backend/internal/service/billing_service.go`、`openai_gateway_usage.go` 中的 usage 构造、价格选择与账号统计分支。
- 验证：原始长上下文恰好跨阶梯、转换倍率 0/小数/大于1、两种账号统计价格分支。现有用例不能代替所有模型价格及预留机制的场景验收。

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

## PK-007

**独立首字设置与配置链路。** 引入：`54b4372c2`；后续完善：`41958f4d4`、`a6123c20c`。

- 管理员系统设置保留独立“首字设置”页签、开关及随机时间上下限；默认关闭，范围默认 1–2 秒。界面按秒输入（精度 0.001），后端按毫秒保存。
- 键：`stream_early_heartbeat_enabled`、`stream_early_heartbeat_min_delay_ms`、`stream_early_heartbeat_max_delay_ms`；必须满足 `0 ≤ min ≤ max ≤ 30000`，非法范围不写入。
- 局部更新缺失字段保留原值，显式 false/0 生效；审计包含新增字段。设置缓存默认60秒，当前进程成功保存后及时失效；读取失败/非法配置采用安全关闭回退。
- 保存失败不能提前替换有效缓存。其他实例按自身缓存刷新，已开始的请求使用启动时取得的设置，不能承诺所有实例和在途请求立即变更。
- 入口：`frontend/src/views/admin/SettingsView.vue`、`frontend/src/api/admin/settings.ts`、类型和中英设置文案；`backend/internal/service/{domain_constants,settings_view,setting_parse,setting_update,setting_gateway_runtime}.go`；`backend/internal/handler/admin/setting_handler*.go` 和 `dto/settings.go`。
- 验证：`TestStreamEarlyHeartbeat*`、`TestUpdateSettingsEarlyHeartbeat*`、`SettingsView.spec.ts` 中设置、秒/毫秒换算及局部更新场景。

## PK-008

**真实提前发出的 SSE 心跳。** 引入：`54b4372c2`，完善：`41958f4d4`。

- 对已接入的 OpenAI 兼容 Responses、Chat、Messages SSE 请求，在完成必要校验后、等待账号/上游时，从请求起始时间计算随机发送时机，真实写出并 flush 无正文的 `: keepalive` 注释。
- 真实业务正文开始后停止提前心跳；请求快速结束或取消时不得再写。随机数用于调度发送，不能直接作为统计值，也不伪造模型文本。
- 单纯修改 Header 或写出成功响应头不能取消等待心跳；首个心跳设置 SSE/no-cache/`X-Accel-Buffering: no` 并移除 `Content-Length`。真实输出更早到达时立即转发，不等待随机时间。
- 不注入非流式、WebSocket 入站、图片、legacy compact、native V2 compact 等排除路径；上游 WS 转下游 SSE 的既有重连语义保留。
- 适用范围由 `OpenAIGatewayHandler` 及其路由决定（包括复用该处理器的兼容平台），原生 Claude/Gemini handler 不因这项改动自动纳入；新增路由必须重新核对处理器。
- 禁用提前心跳时，已纳入测量的入口仍采集真实首包。服务器写出耗时不等于客户端网络到达耗时，部分 SDK 忽略注释。
- 入口：`backend/internal/service/openai_early_sse_heartbeat.go`；`backend/internal/handler/{openai_gateway_handler,openai_chat_completions}.go`。
- 验证：`TestPKEarlyHeartbeat_*`、`TestPKFirstResponse_DisabledHeartbeatStillRecordsBodyAndCopiesContext`，以及获准的隔离远端通过实际反代读取 SSE。

## PK-009

**双计时与全部持久化路径。** 引入：`54b4372c2`，完善：`41958f4d4`。

- `usage_logs.first_response_ms` 独立记录首次成功非空下游正文写出的实测耗时（可为心跳）；原 `first_token_ms` 保存原上游计时，调度/计费不得改用随机值。
- 保留 nullable 字段和真实 `0` 的区分。历史或未接入测量的记录保持 null，不回填原上游指标冒充首包。
- 异步 usage worker 使用独立原子计时状态，通过 `CopyFirstResponseTiming` 传递到 detached context；不能异步持有已回收的 Gin context。
- 只记录第一次实际 body 写出，header-only flush 不计；换号不重置同一个下游请求的首包计时。
- SQL select/scan、单条插入、批量、best-effort、查询和 DTO 均保留字段。插入追加在既有参数末尾，不打乱账务参数位置。
- 迁移 `backend/migrations/241_usage_log_first_response_ms.sql` 增加可空列，保留旧显式 SQL 兼容。当前由 SQL repository 管理该扩展字段；若以后转入 Ent，必须同步生成描述符并验证索引，不能直接往旧 Ent schema 中插字段。
- 入口：`backend/internal/service/{openai_early_sse_heartbeat,usage_log,openai_gateway_usage,gateway_usage_billing}.go`、handler usage context、`backend/internal/repository/usage_log_repo_{insert,query}.go`。
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
- 外部排队心跳取消待发送的首次心跳，但共享控制器仍可接受之后的周期保活；关闭提前开关时也必须成立。只有心跳时 adjusted size 归一为 `-1`；外部注释字节由原调用方计数，writer 不重复累计。
- `Written()` 必须继续表示真实 HTTP 提交状态；只有“是否已输出业务正文、是否允许换号/重试/fallback”的判断使用一致的 adjusted size。Handler 到转发 service 的相关业务判断都要保留，不能遗漏桥接或重试分支，也不能把 HTTP 已提交状态改成未提交。
- 保留首输出超时的安全标记、最多一次安全切号和客户端取消阻止重放；已输出语义正文后，不进行会重复输出/收费的重放。
- 心跳不改变缓存转换、模型正文、usage 和账单，不以排队心跳冒充真实上游首字。
- 入口：`backend/internal/service/{openai_early_sse_heartbeat,openai_compact_sse_keepalive,openai_gateway_forward,openai_gateway_service,openai_gateway_request_body,openai_gateway_chat_completions}.go`；`backend/internal/handler/gateway_helper.go` 及两个 OpenAI handler。
- 验证：`TestPKEarlyHeartbeat_AdjustmentCombinesStreamComments`、`ExternalHeartbeatAllowsLaterPeriodicKeepalive`、`OnlyCompleteTransportFramesPreserveController`、`QueueAccountingWithoutController`、`ReusesExistingPassthroughKeepalive` 等同前缀用例。

## PK-012

**提前 HTTP 提交后的错误与并发安全。** 引入：`54b4372c2`，修正：`41958f4d4`。

- 未发送正文的快速错误保留原 HTTP 状态和 JSON。心跳已提交 HTTP 200 后，晚到错误使用入站协议对应的 SSE 终止事件：Responses、Chat、Messages 不能混发，也不能在流后追加裸 JSON。
- Header 克隆隔离定时器与业务修改，写锁串行化输出；停止等待定时 goroutine 退出并还原 writer，之后才允许 handler 返回、Gin context 回收。
- timer 不回读变化中的 Gin context；取消、结束、错误及 Hijack 都要停止等待，Stop 可重复调用。错误转换只发送一次终止事件，保留原拟返回状态的 Ops 错误标记。
- 保留 Ops 流错误/已提交标记；后到动态上游响应头已无法补发，这是提前响应的边界。
- `Server-Timing` 中间件只在真实 `/api/v1/` UI 路径启用；伪造 UI 请求头不能在模型网关启用并产生并发写入冲突。
- 入口：`backend/internal/service/openai_early_sse_heartbeat.go`、`backend/internal/handler/gateway_helper.go`、`backend/internal/server/middleware/server_timing.go`。
- 验证：`TestPKEarlyHeartbeat_FastErrorKeepsHTTPStatusAndJSON`、`LateJSONErrorUsesProtocolTerminal`、`QueueOnlyLateJSONErrorUsesSSE`、`HeaderMutationDoesNotRaceTimer`、`CancellationStopsPendingTimer`、`StopRestoresWriterAndPendingHeaders`；云端 race 检查与 `TestServerTimingScopesAndRoleGate`。

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
- 验证：私库 Release workflow 中容器启动、同种子保留、换种子恢复检查，以及隔离环境的重复更新/重启；必须确认运行程序版本，不能只看容器镜像标签。

## PK-017

**私有发行与可用部署入口。** 引入：`85d690928`、`e67a95f2e`；文档：`23e5fe663`。

- `PK Release` 在指定私库且为 private 时运行，手动选择已审阅且未复用的 PK 标签；同一构建二进制进入 Docker 镜像和 Release 归档。
- 标签必须指向私库 `main` 历史内的目标提交，已有 Release 不覆盖；发布串行执行，镜像写入来源/版本/revision 标签并核对包私有性，使用 Actions 自身的 `GITHUB_TOKEN`。
- Release 是正式发布而非 prerelease，带 `sub2api_版本_linux_amd64.tar.gz` 和 `checksums.txt`。当前仅发布 `linux/amd64`；Apple/arm64 必须先发布并验证相应私有镜像，不能替换成上游镜像。
- 保留云端前端/后端构建、更新与账务/首字回归、部署脚本及持久化启动检查。feature 分支候选制品与正式 Release 分开，发布流程不自动部署生产。
- 2026-10-01 币安充值接入：私库 `codex/` 分支和原 `feat/` 分支均生成候选制品；正式发布前增加币安协议、余额事务与并发入账检查。前端 Axios 升至 1.20.0，修复旧 1.18.1 的七项 high 安全告警，保持原有 API 客户端与刷新令牌行为，不扩大审计豁免。
- PK-025 接入时，CI 和正式发布增加独立网关检查，验证认证、香港地域、目标白名单、每次探测与固定节点、错误及超时；正式发布必须等待此检查通过。网关脚本随私库源码管理，服务器凭据单独保存。
- 原 `install.sh`、`docker-deploy.sh` 的不兼容旧安装入口保持停用。全新部署用私版完整 Compose；已有环境的 `docker-compose.pk.yml` 是 overlay，不能替换整套生产配置。
- 被保留的脚本 API helper 仅接受本私库 Release 元数据端点，凭据经 stdin 注入，不接受任意 URL/重定向参数；source 后直接调用旧 downloader 也不能绕过停用。Apple restart 必须在停服务前验证显式私有 ARM 镜像配置，不能将 `latest` 当作已支持 ARM。
- 管理员更新/回退/重启保留原认证、审计和系统操作锁；UI 在线升级仅面向正式 release build，候选不能冒充正式升级包。
- [中文部署指南](deploy/PK_DEPLOYMENT_CN.md)、README、[私有镜像说明](deploy/DOCKER.md) 和 [维护文档](PK_MAINTENANCE.md) 随源码保存；新部署使用自己的数据/账号/密钥，程序没有固定服务器 IP 或域名限制。
- 入口：`.github/workflows/{backend-ci,release}.yml`、Dockerfile、`deploy/` 下相关脚本、Compose 与文档。验证边界见下文，不能把“发布已完成”当成“线上已更新”。

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
- 当前19项受控后台：BatchImageCleanup、TokenRefresh、CNProviderBalanceCheck、DashboardAggregation、AccountExpiry、OpenAICodexVersionSync、ClaudeCodeVersionSync、ProxyExpiry、SubscriptionExpiry、OpsMetricsCollector、OpsAggregation、OpsAlertEvaluator、IdempotencyCleanup、ScheduledTestRunner、OpsScheduledReport、PaymentOrderExpiry、ChannelMonitorRunner、ChannelMonitorV2Aggregator、BinanceRechargeService。币安轮询随同原后台交接，仍以数据库唯一约束和事务防止重复入账。新增后台服务时必须审查是否需要同样交接，并同步本条。
- 门控不暂停全部任务，不是数据库只读模式；请求处理、必要记账、缓存失效及已有锁保护的必要工作仍应可用。
- 入口：`backend/internal/service/{background_start_gate,wire,batch_image_cleanup}.go`、`backend/cmd/server/{wire,wire_gen}.go`。
- 验证：六项 `TestBackgroundStartGate_*`，以及 `TestBatchImageCleanupLifecycle_StopWithoutStart`、`ImmediateStop`。

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

- `AGENTS.md` 纳入 Git，入口指向 `PROJECT_CONSTRAINTS.md` 和本清单；私库克隆/恢复后也必须保留。
- 本文件为唯一功能登记表。维护文档、PR模板和 `skills/pk-sub2api-maintenance/` 通过链接/工作区路径读取它，不复制多份可能过期的功能清单。
- 新增二开在同一变更中添加 ID；扩展旧功能更新原 ID；合并上游逐个有效 ID 核对，未登记、未验证的受影响项不能合入/发布。
- 已安装 skill 读取当前工作区约束和清单；更新仓库版 skill 时同步实际使用的安装副本。目录丢失从私库恢复，不靠旧记忆或功能标题重写项目。
- 验证：静态核对 Git 跟踪、入口链接、登记条目与 diff、仓库/安装版 skill 一致性。此机制不等于 GitHub 分支保护或自动语义验证。

## PK-022

**币安 UID USDT 小数点自助充值。** 状态：有效。
引入：2026-10-01 用户要求参考 `多商户版本-部署净版-20260914` 接入，并明确首版仅使用币安 UID 内部转账。

- 独立“币安自助充值”菜单，管理员包含收款账户、充值订单、累计自助充值、每日消费、币安实时账单五个页签；后两项见 PK-023/024。管理员可配置收款账户并查看全部订单/统计；普通用户仅查看自己的订单和累计充值。统计只汇总已完成订单，包含所有用户的精确总额、笔数、按 Sub2 用户账户的金额汇总和收款账户筛选。
- 独立币安充值账户、订单和到账流水，用户关联现有 Sub2 用户，实收金额完整加入原余额。币安账户未启用时不提供新建入口；管理员可配置多个收款账户，用户历史记录在账户禁用后仍可查。
- 充值按 `1 USDT = 1 美元余额`，包含识别小数。订单使用五位小数，例如实付 `10.01234` 必须增加 `10.01234`，不得经过现有支付的两位四舍五入或金额容差。金额匹配使用整数最小单位，接口以十进制字符串传递；余额和累计充值使用现有八位 NUMERIC 字段。
- 币安 Key/Secret 使用现有 SecretEncryptor 加密，响应只显示是否已配置；留空保留已保存的密钥。收款 UID 有历史订单后不得修改，订单保存收款身份。禁用账户停止创建新单，已有订单继续回查。凭据只用于只读账单查询，不提供明文读取接口。
- 使用币安 `GET /sapi/v1/pay/transactions`，校准服务时间并签名；只识别正向 USDT 的 C2C/PAY 收入及匹配的接收 UID。只读查单不能相信客户端自报到账。查询达到100条时分时间段补查，限流和网络异常记录为可见查询错误，不显示虚假成功。
- 可选 `BINANCE_RECHARGE_PROXY_URL` 为此客户端指定 HTTP/HTTPS/SOCKS5 代理，未配置时直连；无效代理配置使启动失败，不静默绕过指定代理。`BINANCE_RECHARGE_PROXY_PREFLIGHT` 默认 `false`；开启后必须使用带认证的 HTTP/HTTPS 专用网关，每次查收、探测和实时账单先探测香港节点，规则见 PK-025。其他 HTTP 客户端不继承这两项配置。
- 输入整数 `1～1000000` USDT，系统追加 `0.00001～0.99999` 尾数。同一真实收款 UID 的完整应付金额不跨订单复用，防止旧单迟付认领到新用户；报价由数据库事务及唯一约束保护，耗尽明确拒绝。用户同时最多3个待付单，新建间隔30秒，每日最多20单。
- 币安流水 ID 按真实收款 UID 去重；订单、已使用 balance 充值记录、`users.balance`、`users.total_recharged` 与到账流水同一数据库事务提交。并发、重试和进程重启不能重复加款；提交后失效用户鉴权及余额缓存。新充值须在原用户余额流水中可查。
- 自动轮询覆盖最近24小时的到账流水，包括到期订单；币安交易时间必须在原订单有效期内，接口延迟发现的有效付款仍可入账。超时付款及无法准确匹配的情况保留供管理员核对，不盲目认领。重新查单按订单创建时间至到期后24小时的窗口读取真实历史流水，受币安可查询历史范围限制。
- 新增轮询纳入 PK-019 启动门、停止等待及多实例查询互斥；普通支付、缓存转换、模型计费与首字行为不改变。
- 入口：`backend/internal/pkg/binancepay/`、`backend/internal/service/binance_recharge*.go`、`backend/internal/handler/binance_recharge*.go`、`backend/internal/server/routes/binance_recharge.go`、`backend/migrations/242_binance_recharge.sql`，以及前端币安充值 API/类型、用户与管理页面和导航。
- 验证：在已有支付协议测试、用户 repository 集成测试和支付前端测试中扩展 `PKBinance` 场景；CI 覆盖五位精度、签名/账单过滤、并发重复到账、历史金额不复用、过期回查与权限。仅在 GitHub Actions 执行，本地不运行构建或测试。模拟账单与真实 PostgreSQL 验证不等同于真实币安转账验收。
- 边界：首版仅 UID 内部转账，不支持链上地址、订阅购买、原支付倍率/手续费、自动退款或邀请返利。有关联订单的异常展示为待核对；无归属到账流水保存在数据库，首版没有独立流水浏览或人工认领入账接口。与其他独立系统共用相同收款 UID 时，小数订单无法识别付款意图，应使用专用收款账户。历史金额占用记录必须保留，不得通过删除订单回收已使用报价。

## PK-023

**管理员每日消费页签。** 状态：有效。
引入：2026-10-01 用户要求在币安自助充值菜单增加每日消费，复用现有全部使用记录页面。

- 仅管理员可见并通过原管理员使用记录 API 取数，普通用户不能访问全局消费记录。默认选择今天、所有用户，保留原页面的筛选、汇总、详情和导出能力。
- 展示的是使用记录中的实际消费，不按资金充值来源归属或过滤。页面提示明确包含所有充值渠道余额产生的消费，包括自助充值和手动充值；不得将这些数值标成“仅币安充值的消费”。
- 默认每5秒自动刷新，可关闭或输入刷新秒数；切换页签/卸载后停止定时任务，刷新不得造成重叠请求堆积。保留现有手动刷新和筛选行为。
- 复用 `frontend/src/views/admin/UsageView.vue` 的嵌入模式和 `frontend/src/components/admin/usage/AdminDailyUsage.vue`；原 `/admin/usage` 页面默认行为与 PK-010 管理员首字权限保持。
- 验证：云端前端类型检查、既有使用记录测试及新增在已有测试文件中的嵌入/日期/刷新生命周期断言；不运行本地构建或测试。

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
- 验证：在原有支付协议和前端测试文件扩展账单收支/小数、日期校验、上限提示、只读权限用例，GitHub Actions 执行；真实账户账单可用性须在部署配置后核对。

## PK-025

**币安专用香港出口探测。** 状态：有效。
引入：2026-10-01 用户明确要求只让币安相关逻辑使用香港代理，并在每次查询前探测可用节点。

- 仅影响 PK-022/024 的币安客户端。保持其他模型请求、支付渠道、GitHub 更新和系统网络的既有出口；不配置全局代理、TUN 或系统路由。
- `BINANCE_RECHARGE_PROXY_PREFLIGHT` 默认 `false`，兼容既有静态代理；开启时 `BINANCE_RECHARGE_PROXY_URL` 必须是带用户名和密码的 HTTP/HTTPS 网关。空地址、SOCKS 预探测、非法布尔值或代理配置使启动失败，不能静默降级。
- 每次顶层 `History`、`Bills`、`Probe` 操作调用认证网关 `POST /_binance/preflight`，上限15秒，拒绝重定向、非200、畸形响应和非法令牌。预探测包含在原操作超时及数据库查询租约内；失败作为可见错误交给既有退避机制。
- 网关并行检查最多8个固定节点。每个候选先以严格 TLS 访问 Cloudflare trace 并确认 `loc=HK`，再确认币安时间接口返回200及有效 `serverTime`；两个检查共享该节点6秒预算。每次重新探测，不用上次成功代替本次证据。全部失败时拒绝操作，不回退到直连或其他地区。
- 成功的随机令牌固定指向一个节点，有效期240秒。应用为本次操作创建独立 transport，校时和全部分页共享该出口，令牌仅放在 CONNECT 头；并发操作不得通过切换全局代理组互相改路由。固定出口中途失败则本次失败，下次操作重新探测。
- 网关只接受经过认证且带有效令牌的 `CONNECT api.binance.com:443`。币安签名和 API 凭据在应用到币安的 TLS 内传输；拒绝其他目标、普通转发、无令牌或过期令牌。节点监听仅在本机，网关只绑定应用可达的私网地址，凭据和订阅保存在服务器受限文件中，不写入源码或日志。
- 入口：`backend/internal/pkg/binancepay/{client,proxy_preflight,bills}.go`、`backend/internal/service/{wire,binance_recharge}.go`、`deploy/binance-proxy/gateway.py`。固定节点及 systemd 运维见该目录说明和 `PK_MAINTENANCE.md`。
- 验证：既有 `backend/internal/payment/crypto_test.go` 的 `TestPKBinanceProxyPreflight*` 覆盖真实模拟 CONNECT/TLS、分页固定出口、并发隔离、失败关闭和取消；网关验证在 CI 与正式发布工作流执行。全部构建测试在云端；生产只做公开币安时间接口及真实出口探测，不用模拟到账冒充真实转账。
- 边界：地域依据 Cloudflare 当前看到的出口，节点名称不作为地域证明；公开时间接口成功不能代替收款账户 API 权限或真实转账验收。订阅节点凭据失效或出口变化时停止该节点使用，需更新服务器节点配置；没有直连兜底。

## 验证入口与现有边界

### 已有回归入口

| 范围 | 已有测试/流程 | 使用边界 |
|---|---|---|
| PK-001～006 | `backend/internal/service/openai_gateway_usage_integrity_test.go` 中 `TestPKCacheConversion*` | 覆盖设置、公式及 JSON/SSE 代表场景；不是所有协议端到端验证 |
| PK-007 | `setting_service_update_test.go`、`setting_handler_partial_payload_test.go`、`SettingsView.spec.ts`、`api_contract_test.go` | 包含配置链、局部保存、错误与前端换算 |
| PK-008～012 | `openai_compact_sse_keepalive_test.go`、handler context、DTO、usage repo、channel monitor、Server-Timing 测试 | unit/race 与数据库 integration 分开；候选须检查对应 SHA 的实际 CI |
| PK-010 UI | `frontend/src/components/admin/usage/__tests__/UsageTable.spec.ts` | 币安/每日消费接入时纳入 `FRONTEND_CRITICAL_VITEST`，与 `UsageView.spec.ts` 一同验证嵌入模式和原上游首字权限 |
| PK-013～015 | `update_service_test.go`、`github_release_service_test.go` | 使用更新器自身 PK 修订、授权、路径及校验测试 |
| PK-016～017 | `.github/workflows/release.yml`、现有 `deploy/tests/` 和候选制品流程 | 发布构建不等于生产已部署；测试清单/矩阵改变时同步核查 |
| PK-018～019 | `concurrency_service_test.go`、`wire_test.go`、`batch_image_cleanup_test.go` | 需要门控/停止并发行为证据，不能仅搜索到 env 键即认为有效 |
| PK-020～021 | 维护流程、固定 commit 的隔离验收和静态文档核对 | 不运行本地构建测试；不把历史线上/探测报告当作本次新代码验证 |

正式 Release 的 unit/race 选择不会执行数据库 integration 的 `TestPKUsageFirstResponsePersistence`；该项由常规 CI 的 integration 阶段覆盖。前端 Release 构建也不能代替 UI 测试。有关功能修改时，必须检查真实执行列表并在云端补足受影响验证，不能只列出测试名。

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

新增独立能力使用下一个未用 ID，当前下一个为 `PK-026`；分支并行新增时在合并前消除 ID 冲突，已有 ID 不重用。同步更新索引、有效条目范围和此处的下一个 ID。

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
