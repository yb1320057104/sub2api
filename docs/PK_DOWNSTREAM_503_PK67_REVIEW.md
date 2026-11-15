# pk.67 下游 503 修复复核

## 范围与状态

用户要求查明并修复下游频繁出现的 503。本轮在唯一 `v0.2.19-pk.67` 工作分支修复缓存时序缺口与转发池握手失败处理；沿用 PK-006/021/047，保留正常限流、计费拒绝保护、工具重放限制和单 writer/活动 worker。旧正式标签、版本分支、封存制品及 pk65/pk66 HA profile 不修改。

当前已完成源码修复与定向验证。正式制品验证、发布和生产更新另行记录实际结果；未完成的阶段不能据此视为通过。

## 生产只读诊断

北京时间 2026-11-15 21:23 至 21:30 的有界采样显示：

- 初次快照最近 1 小时共 171 次 503，集中在同一个业务分组。两 API 未见重启或 OOM，数据库无锁等待/查询排队，主要节点负载约 0.5。
- 最近 2 小时的 41 条 `billing.inflight_unavailable` 全部为 `balance_read/cache_miss`，单次读取约 62–83ms。最近 6 小时的 220 次计费类 503 涉及的三个用户均没有自动授信配置。因此不能把 Redis 网络故障、自动授信或配额回填超时作为该批错误的已证原因。
- 业务组有三个账号：一个手动停调，一个 OAuth 账号处于已记录的额度限流期，只剩一个 API Key 转发池账号。该池发生模型级短暂熔断后，调度候选为空。
- 最近 1 小时该转发池的上游失败事件中，159 条为外层 502，消息为 `failed to WebSocket dial: expected handshake response status code 101 but got 403`；另有 18 条响应流未正常完成、2 条真实上游过载。本地出站已经关闭 WebSocket、使用 HTTP Responses，握手拒绝发生在 relay 内部。

各日志窗口不同，不把计数相加为同一总体。诊断器按文本前缀解析日志级别和消息，避免遗漏 JSON 内没有 `level`/`msg` 的事件。原始私有证据留在工作区忽略目录，不提交账户身份、上游地址、凭据或生产机器信息。

## 修复合同

### HA 余额缓存

资格检查在普通余额缓存未命中时查询数据库并异步排队回填；HA 在途预约紧接着只读 Redis，会在回填尚未完成时将 miss 误判为不可用。

独立 pk67 HA profile 对明确 miss 做短预算、有限次数补读，等待已排队回填；首次命中与真实存储错误保持原处理。持续 miss、取消和错误仍拒绝放行。不得将数据库余额覆盖回 Redis，不重试结果不明的预约 Lua，不改变扣费、限额、预约原子性或 TTL。自动授信 DB 权威余额的独立兼容问题未在本轮现场触发，不纳入本次修改。

### 转发池握手失败

严格识别 API Key 转发池的结构化上游错误：外层 502、顶层 `upstream_error` 语义及上述精确握手 403 消息。将明确的池内传输失败交给现有同号有界恢复，避免额外冷却整个账号的该模型。显式关闭或限制重试的管理员配置仍优先；普通 5xx、真实账号/权限错误、模型不支持和非池账号保持原保护。

不得对已输出语义正文、工具结果或不能安全重放的续链绕过现有限制。真正上游持续拒绝、流中断、过载、未支持模型以及缺少健康备用容量仍可能返回错误，本修复不保证外部上游故障清零。

## 验证与交付

1. HA 计费服务定向 30 个顶层测试、50 个通过事件；repository 现有 miniredis/Lua 定向 13 个顶层测试全部通过。合计 43 个顶层、63 个通过事件，无失败或跳过。相同异步回填回归用例替换回旧函数后实际失败，还原修复后通过。详见 `PK_BILLING_CACHE_503_PK67_REVIEW.md`，其中保留首次辅助脚本失败与有效负对照的区别。
2. 普通源码转发服务定向 11 个顶层测试通过；handler 定向 7 个顶层测试通过。覆盖 Responses、Chat、Messages，普通及透传、流式及非流式、第三次恢复成功、重试耗尽、次数为零、显式状态列表/空列表、普通 5xx 冷却、既有模型 404 与工具协议保护。最终命令见下，退出码均为 0；输出分别报告 service 0.420s、handler 22.771s。此组未保存独立日志文件，以本轮执行工具输出为证，不虚构子测试计数。
3. 独立审阅四个业务文件、三个原有测试和 HA 两路径差异，未发现阻止合入的实际风险。新规则仅在 HTTP 错误构造入口赋予同号恢复资格；流内终止事件、已输出文本及工具调用不会取得新增重放权限。显式重试策略、Lua 单次写入及真实 Redis 客户端上下文超时保持。
4. 新 profile 的 96 个路径均实际应用并核对输出 SHA256；相对 pk66 仅计费函数与既有 HA 测试两路径变化。普通源码七个变更路径与 profile 无交集。`git diff --check` 通过。无迁移、依赖或前端改动，不运行全套源码回归。
5. 通过旧生产版本现有管理员账号测试入口执行了一次最小真实 `gpt-6.1-sol` 请求，返回成功。它只证明该上游间歇可用，不证明新规则已生效。生产日志未保留完整 502 错误信封，严格解析遇到未知字段、尾随 SSE 或不同错误类型时不匹配；新规则是否命中实际故障需在更新后观察固定诊断日志，不能提前声称生产握手错误已消失。

```text
go test -tags unit ./internal/service -run '^(TestOpenAIRequestScoped403|TestOpenAIPoolRelayWebSocketHandshake|TestOpenAIPoolMode(Retryable|NonRetryable)5xx|TestOpenAINonPoolAPIKey5xx)' -count=1
go test -tags unit ./internal/handler -run '^(TestPoolRelayWebSocketHandshake|TestPK64ToolProtocolRoutingAndProviderFailoverOnWire$|TestPK65ModelNotFound404RoutingOnWire$|TestOpenAIForwardMayFailoverOnlyAfterNonSemanticWrite$|TestPKEarlySpaceFailoverRejectsSpaceWrittenBeforeForwardSnapshot$)' -count=1
```

本轮准备新的 pk67 制品工具，旧工具和通过回执不覆盖。构建、同制品验收、正式发布及生产更新尚未完成，本节不将工具准备当作这些阶段的通过证据。
