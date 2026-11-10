# ranxi2001/sub2api 源码比较（固定快照，正式报告）

本报告比较 [ranxi2001/sub2api 的 production](https://github.com/ranxi2001/sub2api/tree/3bbfe758eaa713c8695295abe6a86d125c9abd4f) 提交 `3bbfe758eaa713c8695295abe6a86d125c9abd4f` 与私库 `dcbf50a2ae0eeec30661b2fcfe0bbab17f6233ba`。两个快照已独立取得；本次仅比较，没有整库合并或生产操作。同轮 pk.60 防蒸馏用户白名单扩展另见 [实现与验证记录](PK_GROUP_ANTI_DISTILLATION_ALLOWLIST_PK60_REVIEW.md)，不计入此处比较基线。

Git `ls-remote --symref` 与 GitHub REST 仓库元数据共同确认默认分支是 `production`。其提交时间为 2026-11-10 21:35:19 +08:00（合并 PR #778）；`main=e1317a4fdbb3c9bbb07bdd3318c6df2c3dbacf28`，提交时间 2026-09-18 16:58:16Z，提交说明是同步 VERSION 到 2.5.5。因此不能把该仓库较旧的 main 当成当前生产实现。API 观察到 `pushed_at=2026-11-10T13:35:19Z`；这不是 Release 发布时间，本报告不据此推断发行或上线日期。

以下来源行号均对应上面的 production 固定提交；本地证据按固定私库提交检索，不能把宣传文案或同名函数直接等同已验证运行行为。引用 URL 模板为 `https://github.com/ranxi2001/sub2api/blob/3bbfe758eaa713c8695295abe6a86d125c9abd4f/<path>#L<line>`。

## 已有明确证据的分类

| 功能域 | 对方证据 | 私库基线及结论 |
|---|---|---|
| 账号性能阈值与独立暂停 | `backend/internal/service/account_performance.go:38` 配置，`:155` 周期评估；`backend/internal/server/routes/admin.go:500` 批量读、保存、解除与事件接口，`:789` 全局窗口配置；`docs/account-performance-monitor.md:3` 指标口径 | 私库有 PK-037 账号健康统计、最近首字和失败统计（`backend/internal/server/routes/admin.go:82`），但固定基线没有 `AccountPerformance`、`account_performance` 配置/API或该独立暂停机制。判定为**部分已有，缺阈值自动暂停能力**，不能说完全没有账号监控。对方上游错误率只采集当前进程HTTP，非集群总错误率，原生WS不覆盖；移植时须适配现有单worker/API分离。 |
| Mihomo 订阅与静态节点代理管理 | `backend/internal/server/routes/admin.go:865` 内核管理、节点测试与静态代理导入；`backend/internal/service/admin_mihomo_static_proxy.go:39` 批量创建；`docs/MIHOMO_MANAGED_CN.md` | 私库已有原生HTTP/SOCKS代理、PK-044多代理/全局容量、PK-045静态IP分组；固定基线没有 `mihomo` 模块或管理入口。**缺内置订阅/内核/节点导入，不缺普通代理管理**。 |
| 内置 BEpusdt USDT/USDC 链上支付 | `backend/internal/bepusdt/engine.go`、`backend/internal/payment/provider/bepusdt_embedded.go`、`backend/internal/handler/admin/payment_bepusdt.go` 均是实际模块 | 私库有 PK-022 币安UID USDT充值及原上游支付系统，但未检出 `bepusdt`。**是新增支付通路，不是现有币安充值的重复实现**。需要独立评估钱包、扫链、订阅与入账幂等边界，不能整体替换PK-022。 |
| Anthropic API Key 模拟 Claude Code | `backend/internal/service/gateway_anthropic_mimic_claude_code.go:59` 身份注入、`:180` 头注入、`:254` 可选估算扣减；`account.go:2779`、`:2792` 实际读取两个显式开关，默认关闭，抹除身份优先 | 私库基线无 `anthropic_mimic_claude_code` 或对应函数。**缺该兼容选项，不缺基础Anthropic支持**；可选14–24 token估算扣减涉及账务，不能混同精确上游计费或连带默认开启。 |
| API Key 自身并发上限及独立等待队列 | `backend/migrations/237_add_api_key_concurrency_limit.sql:2` 增加上限，0无限制；`backend/internal/config/config.go:991` 队列结构、`:1008` 默认等待5、超时30秒、`:1016` 严格校验；`middleware/api_key_admission_owner.go:51` 重验证 | 私库APIKey固定结构只有组关系与CurrentConcurrency，无可配置Key上限，未检出 `api_key_queue`。**缺Key级上限/队列，现有用户/账号限流仍在**。对方不承诺FIFO，HTTP/SSE/WS各轮/Live有接线；移植需核PK首字等待、多组及共享Redis释放语义，不能据此承诺3000并发容量。 |
| 分组防蒸馏 | 私库 `docs/PK_GROUP_ANTI_DISTILLATION_PK59_REVIEW.md:8` 明确已从对方 `1c3882e935a01b376f9aefdf844863125fff7c2b` 移植；私库 `backend/internal/service/group_anti_distillation.go:19`、middleware真实入口 | **已移植（PK-048）**，不能列为缺失。应只审来源旧提交→新production的后续差异，不覆盖现有计费/多组/WS适配。 |
| 定时质量/糖果测试 | 对方 `backend/internal/service/account_quality.go` 与 `docs/account-quality-operations.md`；私库 `backend/internal/server/routes/admin.go:65`–`:80` 雷达/账号糖果实际路由 | **两边已有同类能力，实现和规则不同**。私库PK-032/034/035支持定时、多模型、判21、转组；对方另有按模型隔离、连续自身OAuth401后清理检测规则、自动BPS规则，须作为增量选项单列，不能说糖果测试缺失或直接替换私库判题语义。 |
| DeepSeek / Codex 协议转换 | 对方 `backend/internal/pkg/apicompat/deepseek_tool_text.go:25` 流式工具文本解码、`:84` 完结恢复、`:231` 非流式恢复；私库 `openai_chat_roles.go:17` 明确DeepSeek/Kimi/Zhipu分支且有Responses/Chat回退 | **基础协议支持已有，工具文本恢复实现有差异**：私库未检出该模块。图片schema/compact等其他项目本次未逐行为证明等价，不能把文档名称全算缺失，更不能说“本地不支持DeepSeek”。 |
| UltraFast 档位计费 | `backend/internal/service/service_tier_billing.go:10` 新开关名，`:74` OpenAI API Key明确启用后按响应升降档，无响应退回出站请求；`billing_service.go:144` UltraFast默认倍率6 | 私库已支持UltraFast及Astra6倍；基础倍率默认2，`UltrafastMultiplier` 可覆盖，Astra已设6。固定基线无响应升档开关，只有默认降档和OAuth default特例。**差异是可选响应计费合同与默认倍率范围，不是缺UltraFast**。不能未经资费决策直接统一6倍。 |
| 抽奖/邀请奖励 | 对方 `backend/internal/server/routes/admin.go:42`–`:55`，`user.go:27`–`:34` 池、邀请链接、领取邀请、抽奖、记录，真实Lottery服务/Ent schema | 私库固定基线未检出同域。**可列营销附加项，优先级低于稳定性/调度**。本次未审计奖品扣返款、刷奖与入账幂等，不据存在路由推荐直接上线。 |
| API Key独立模型映射/推理强度/长上下文策略 | `backend/migrations/327_api_key_model_policy.sql:2` 增加model_mapping/reasoning_effort，`333_api_key_allow_long_context.sql:2` 默认true；`routes/gateway.go:85`–`:86` 真实middleware接线 | 私库APIKey固定结构无这些字段。已有组/账号映射与推理强度、PK-042 Key多组不等同Key独立覆盖。**缺该Key级策略**。对方只允许管理员配置，映射一次且同时检查原始/目标模型权限；禁长上下文时不可验证隐藏历史/媒体会拒绝。值得优先评估，保留多组与实际计费模型归属。 |
| Excel/BPS OAuth通道 | `backend/internal/service/openai_excel_bps.go:222` 实际转发；`routes/gateway.go:55` GET/HEAD图片回传；`docs/excel-bps.md` 说明专用token/client、模型范围、Responses/compact及403恢复边界 | 私库未检出basispoints/openai_excel_bps。**缺额外上游传输/OAuth通道**，不等同PK-028 Kilo桥接。默认关闭、非所有账号/工具通用，应按真实账号场景选择，不能当通用性能优化。 |
| 国家→gateway路由（共享DB/Redis） | `backend/internal/serverless/routing.go:140` 路由、`:285` 转发；`routes/gateway.go:42` 调用；`docs/serverless-routing.md` 描述注册/健康、Key或Key+IP绑定、签名身份 | 私库无serverless模块，但已有PK-047 HAProxy双节点/共享存储/单worker。**缺该应用路由，不缺多节点HA**。默认owner故障拒绝，可选仅转发前回落，不重放；不创建/扩缩K8s，用户字节仍经入口，不是地理DNS或节省入口带宽方案。 |
| 独立区域鉴权/调度/额度/槽位账本 | `backend/internal/repository/regional_funds_runtime.go:87` ProvideRegionalMetering，`:131` authority/control handler，`:147` meter.Admit；`handler/openai_gateway_handler.go:5512` readiness；`routes/gateway.go:87` 协议限制 | 私库无RegionalFunds/RegionalScheduler同域。**实验性基础设施，不计为成熟收费功能**。文档与当前源码存在差异，详见下文；独立DB/Redis架构与现有共享存储HA不同，不能作为此次优化直接启用。 |
| 按上游域名/账号选择地区出口 | `backend/internal/pkg/upstreamroute/router.go:47` 配置验证、`:120` 精确/最长后缀优先、`:156` 按上游账号匹配；`docs/upstream-regional-routing.md` | 私库有多代理/静态IP组，但无该upstreamroute规则模块。**缺该域名/账号→区域出口规则**，不等于缺代理。默认关闭、配置错误拒绝、不静默直连，变更需重启；决定已选账号如何出站，与用户分到哪个应用节点不同，不减少入口带宽或保证提速。 |

## 需要特别保留的边界

**账户性能不能直接搬进单worker架构。** [account-performance-monitor.md](https://github.com/ranxi2001/sub2api/blob/3bbfe758eaa713c8695295abe6a86d125c9abd4f/docs/account-performance-monitor.md) 明确上游错误率是当前进程HTTP采样，原生WS不覆盖。worker不承担API流量时，自动暂停所用错误样本不完整；需跨节点共享/汇聚后再启用，不能拿进程指标当集群健康。

**区域账本文档滞后于部分源码接线，但仍不能称成熟。** [regional-ledger.md:1](https://github.com/ranxi2001/sub2api/blob/3bbfe758eaa713c8695295abe6a86d125c9abd4f/docs/regional-ledger.md#L1) 标为“基础（未接入模型入口）”，列出监听、模型接线、定价验证和部署对账未完成项；当前 [regional_funds_runtime.go:87](https://github.com/ranxi2001/sub2api/blob/3bbfe758eaa713c8695295abe6a86d125c9abd4f/backend/internal/repository/regional_funds_runtime.go#L87) 已有资金runtime、handler与Admit，OpenAI handler有readiness接线。因此不能照旧文档断言当前源码完全未接入，也不能仅因函数存在便断言生产闭环完成。源码要求匹配guarded concurrency、固定定价/明确目标、迁移epoch，批量图片队列须排空关闭；没有认证费用上限的协议返回503。独立数据库部署、故障恢复、配额边界、真实请求和双方账单对账仍需完整审计与验收。

**BEpusdt确实有订单实现，不能泛化成私库没有支付。** [bepusdt_embedded.go:36](https://github.com/ranxi2001/sub2api/blob/3bbfe758eaa713c8695295abe6a86d125c9abd4f/backend/internal/payment/provider/bepusdt_embedded.go#L36) 创建订单、`:71` 查询、`:97` 取消；[payment_bepusdt.go:21](https://github.com/ranxi2001/sub2api/blob/3bbfe758eaa713c8695295abe6a86d125c9abd4f/backend/internal/handler/admin/payment_bepusdt.go#L21) 管理钱包/汇率/节点。只是额外链上渠道，未完成独立资金安全和幂等审计。

## 建议顺序

1. 后续发行先按现有HA流程验证main已累积的统计追赶修复，再评估新增功能。此前资源/任务恢复的实际状态以运维报告为准；这份源码比较没有部署或压测，不能作为高并发容量验收。
2. 优先评估Key并发队列、Key模型/长上下文策略；账号性能自动暂停必须先适配跨节点指标。按PK流程小范围移植，保留多组、计费、首字等待与WS语义。
3. DeepSeek工具文本恢复等按真实失败样本选补丁。Mihomo、BPS、BEpusdt、抽奖属于业务选项。
4. 响应升档与UltraFast默认6倍属于资费决策，不能当“不计费修复”顺手开启。Serverless/独立区域账本属于架构工作，应另做压测、故障测试和账单对账。

## 旧来源到production的历史增量限制

PK-048来源 `1c3882e935a01b376f9aefdf844863125fff7c2b` 到当前production的**完整历史差异未取得**。第一次定点fetch返回TLS early EOF，第二次只抓commit/tree的 `--filter=blob:none` fetch仍遇TLS握手失败；GitHub REST compare返回公开API rate limit exceeded。没有继续反复重试，也没有把失败/空结果当差异证据。

因此上面15个域的“已有/缺少/不同”均只比较**两个已成功取得的固定快照**，不声称全部新增于PK-048移植以后，也不根据commit标题推断具体安全修复。若后续API compare的files达到300，须按截断数据处理，不能当全量变更集。仓库元数据的许可证为LGPL-3.0；实际移植须保留来源与许可证记录。

网络最初在沙盒中发生TLS凭据/连接关闭错误，按规则提升后只读克隆成功。未读取生产凭据或运维秘密。代码存在/静态审阅不等同生产验收或性能验证；没有执行构建、测试、真实模型请求或任何上线操作。
