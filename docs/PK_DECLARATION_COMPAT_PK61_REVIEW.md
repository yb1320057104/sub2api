# pk.61 缺声明二次核对

本记录核对 `.runtime/pk61/postmerge-symbol-review.json` 在三方合并中列出的 34 个私库旧声明与 3 个上游声明。固定私库为 `6ed612909fef4510f63d93a8f2ead58bae35a3c8`，上游为 `d42bb4fc4b3eef9821994b4ad170d4eb881eca95`。声明清单是线索，结论以旧函数体、旧调用点和当前实际调用链为准。它不证明发行或生产已完成，也不累计其他专项的重叠测试。

## 34 个私库声明的逐项去向

表内路径以 `backend/internal/` 为根；行号为本轮审查时定位，后续格式化可能移动。

|序号|旧声明|当前入口与行为依据|
|---|---|---|
|1|`bindOpenAIReasoningEffortPolicyForMessagesRequest`|`handler/composite_platform.go` 的 `prepareOpenAIReasoningEffortPolicyForMessagesRequest`，由 Messages handler 实际调用；先确定实际平台/组，再准备请求及上下文中的推理策略，保留禁止/上限和最终发送值。|
|2|`modelListingSource`|`service/model_listing.go` 的导出函数 `ModelListingSource`，普通 `/models` 与多组列举复用，保留来源、平台和授权空目录语义。|
|3|`defaultModelIDsForPlatform`|同文件 `DefaultModelIDsForPlatform`；handler 调共享实现，不再维护另一份平台默认目录。|
|4|`mergeModelIDs`|移至同文件 `service.mergeModelIDs`；保序去重由共享列举函数内部接续。|
|5|`ConcurrencyHelper.withAPIKeySlotFromGin`|`handler/gateway_helper.go` 的 `AcquireAPIKeySlotWithWait`；HTTP 获取/排队/释放沿新租约。WS 每轮经 `ReserveWSAPIKeySlotWithWait`，不把整条连接只准入一次。|
|6|`buildCyberSessionBlockWritePlan`|`handler/openai_gateway_handler.go` 写入前同步冻结 identity、可信 IP、规范化 UA 和 body，再调用 `MarkCyberSessionBlockedForRequestWithScope`；异步记录不再读可复用 gin context。|
|7|`findBlockedCyberSessionKey`|`handler/openai_cyber_allowlist.go` 调统一 `FindCyberSessionBlockedForIdentityAndBodyAndScope`，读取显式身份、精确指纹及有条件历史前缀。|
|8|`cyberSessionScopeKey`|统一为 `service.CyberSessionScopeKey`；handler 仍传可信 IP 和规范化 UA，不用任意客户端自报身份扩大封禁。|
|9|`cyberSessionBlockWritePlan` 类型|与第 6 项一并由同步冻结参数和统一 service 方法替代，不存在未使用的旧写计划。|
|10|`suppressOpsUpstreamAttributionForLocalModelConfiguration`|`handler/ops_error_logger.go` 的 `suppressOpsUpstreamAttributionForClientRejection`，主记录和上游错误归一入口均调用；本地模型配置/策略拒绝清除上游归因，真实上游错误保留。|
|11|`agentMessageText`|`pkg/apicompat/chatcompletions_responses_bridge.go:697` 导出为 `AgentMessageText`；`agent_message` 分支实际调用。仍按原顺序拼接 `input_text`/`text` 与明文 `encrypted_content`，不尝试解密、不丢子任务正文。|
|12|`synthesizeChatReasoningFallbackMessage`|旧函数把 reasoning 复制到 output_text。当前 Responses 缓冲与流式终态均检查“非空 reasoning、无答案、无工具”，返回 `upstream_reasoning_only`/`response.failed`，保留原思考/usage，禁止合成答案。二审另修新增 Chat→Messages 直连入口，详见下节。|
|13|`concurrencyCache.sweepLegacyWaitKeysOnce`|`repository/concurrency_cache.go` 启动清理不再删除共享等待计数。`CleanupStaleProcessSlots` 委托有界过期候选清理；其他进程前缀不是死亡证据，防止滚动启动误删活节点负载。|
|14|`concurrencyCache.allIndexMembers`|`reconcileExpiredIndexCandidates` 以 Redis 时间和 `ZRangeByScore` 只读到期候选，限制 `activeIndexCleanupBatchSize`；周期继续处理后续批次，避免全索引启动扫描。|
|15|`concurrencyCache.cleanupStaleProcessSlotsForIndex`|同文件 `readIndexLoads`/`reconcileExpiredIndexCandidates` 清理过期槽位后读取真实负载；无槽位且无等待才移除索引，仍有负载则刷新索引 TTL，保留其他活节点槽位。|
|16|`cleanupUsageLogsBatchWithRollupInvalidation`|改为 `dashboardAggregationRepository` receiver 方法，`CleanupUsageLogs` 实际调用；同一事务锁定 rollup 状态、删除原始行并从最早删除时间使派生统计失效。|
|17|`dropUsageLogsPartitionWithRollupInvalidation`|改为同一 repository 的 receiver 方法；分区删除仍先取得 rollup 状态锁、按分区月份使统计失效，随后执行删除并提交事务。|
|18|`truncateToDay`|由实例 `r.startOfDay`/`r.timezoneLocation` 接续；统计、失效水位和追赶共享配置时区，避免隐式宿主时区与报告日界不一致。|
|19|`httpUpstreamService.acquireClientWithTLS`|`repository/http_upstream.go:888` 的 `acquireRequestClientWithTLS` → `getClientEntryWithTLSHeaderTimeout(..., true, true, requestResponseHeaderTimeout(req))`。保留 in-flight 与池上限，增加请求级 header timeout 独立缓存键。`DoWithTLS` 仍经统一代理租约，错误释放，body 关闭释放；HTTP 明文回普通传输，不绕过配置代理。|
|20|`entQuotaToRecord`|`repository/user_platform_quota_repo.go:142` 的 `readEffective` 直接扫描 `usage_platform_quota_effective` 全部 11 个同名字段；Get/List 共用。独立事务用量增量叠加旧基线，旧 flusher 快照不能覆盖新用量；管理员重置仍走独立事务，不把 DTO 映射缺失误判为配额丢失。|
|21|`cloneStringSlice`|`service/gateway_service.go:1819` 的 `cloneModelCatalogSlice`；缓存写入、命中返回和模型组合都复制底层数组。新实现保留 nil 与非 nil 空切片的区别，授权空目录不会回退默认模型。|
|22|`computeGeminiTextDelta`|`service/gemini_messages_compat_service.go:3447` 的 `geminiTextDelta.next/reset`。仍移除末尾 NUL、累积输入只取新后缀、重复/回退不输出、增量输入追加；thought/text/tool JSON 各自持有状态并在 block 切换 reset。Builder 消除每个增量复制历史全文的成本。|
|23|`updateEWMAAtomic`|`service/openai_account_scheduler.go` 每账号 mutex 的 `reportAt/snapshotAt`；TTFT 保留 alpha 0.2，错误率增加两分钟半衰期，并发报告时间不倒退。读写使用同一锁。|
|24|`codexCatalogMetadataModels`|`service/openai_codex_models_service.go:969` 在 `buildCodexModelsManifestForAccounts` 每模型循环内调用原 `resolveCodexCatalogMetadataModel`；先按实际组和模型过滤可服务账号，再解析映射元数据，保持别名/复合路由，同时不汇总被组权限排除账号的能力。|
|25|`CyberSessionTranscriptBlockKeys`|已恢复，委托恢复的 transcript 推导；匿名命中后记录完整历史与最后用户回合前历史键。|
|26|`CyberSessionTranscriptLookupKeys`|已恢复，保留历史前缀候选与 256 项上限溢出信号，由 scope 激活后的匿名请求使用。|
|27|`CyberSessionScopeKey`|已恢复并成为 handler 统一入口；组/API Key 隔离之外仍保留来源 scope。|
|28|`hashCyberSessionBlockKey`|明确身份改用 `ResolveCyberSessionIdentity` 的 v3 thread/session 类型哈希，兼容 v2 读取。prompt_cache_key 只作缓存亲和，不冒充明确身份；精确请求指纹仍校验，匿名命中后补 session ID 不能绕过。旧混合身份 hash 不恢复。|
|29|`deriveOpenAICyberTranscriptBlockKeys`|`service/openai_cyber_transcript.go` 已从私库恢复；滚动历史摘要、最后用户回合前上下文和上限保持。|
|30|`openAICyberTranscriptItemStartsUserTurn`|同文件恢复；识别带有效内容的 user 角色及顶层 input_text，纯工具结果不误当新用户回合。|
|31|`openAICyberTranscriptItemIsModelGenerated`|同文件恢复；必须已有模型生成历史才写 preLatestUserKey，避免共享首轮模板导致误封其他会话。|
|32|`openAICyberTranscriptBlockKeys` 类型|同文件恢复，字段供第 25/26/29 项使用。|
|33|`openAIEncryptedLineageItemType`|不再仅按 reasoning/compaction 类型把全部密文判坏。`openAIRejectedEncryptedDigests` 使用明确错误定位；泛化错误只接受唯一 reasoning 密文，多个/不透明 compaction 保守不删。`openAIEncryptedLineageKey` 绑定账号/会话，记录摘要，后续只剥命中证据；SSE 恢复仍要求发送前明确拒绝且同轮预算有界。保留旧 lineage 目的并接新历史保护。|
|34|`OpenAIGatewayService.updateGrokUsageSnapshotWithRateLimit`|`service/openai_gateway_grok.go:1642` 的 `updateGrokUsageSnapshotWithOptions`；保留快照、节流、恢复清理与 pool 模式边界。两个独立 options 同时控制即时限流与调度 extras；模型容量 429、模型专属免费额度和显式跳过的未知 403 只保留快照，不误写账号级暂停。|

Handler 十项由独立复核提供调用证据，完整记录为 `.runtime/pk61/FRONTEND_HANDLER_SECOND_REVIEW.md`，其最终 handler/server 定向 391 个顶层通过。仓库过期清理、聚合与金融视图的验证沿主集成对应专项，不在本记录重复运行或把历史证据写成本次重跑。

## reasoning-only 直连遗漏及修复

`forwardAnthropicViaRawChatCompletions` 实际使用新增的单层 Chat→Messages 转换，绕过已有 Responses 转换的终态检查。其非流式 `chatMessageToAnthropicBlocks` 原本在无答案/无工具时把 reasoning 复制到 text；流式则直接结束为成功。二审已移除复制，并补齐两种终态。

非流式确认只有 reasoning 时返回标准 502 `api_error`；流式保留已发 thinking，在关闭 block 后发 `error`（诊断码 `upstream_reasoning_only`），不发正常 `message_stop`。真实答案和工具调用继续原有终态。只含空白的 reasoning 不虚构新失败，答案是否为空按去空白判断。reasoning_content 优先于 reasoning 别名的既有规则保持。

两条 service 路径仍返回实际读取的 `OpenAIForwardResult.Usage`，沿原记账通路处理，不通过错误换号重发上游；记录固定运维诊断，不把 reasoning 写成答案，不改 token 数、缓存转换、最终模型/强度/服务档位，也不改 `scanCCStream` 首字计时。新增状态只保存“是否出现非空思考/文本”的布尔值，不缓存整段流式正文。

本机执行 `.runtime/pk61/reasoning-direct-compat-attempt1.jsonl`：apicompat 与 service 合计 27 个顶层通过，零失败、零跳过。覆盖流式/非流式 reasoning-only、空白答案、字段别名、工具调用、正常文本、max tokens、usage 与缓存 token、最终事件幂等，以及真实 service 路径保留 7 输入/4 输出并只调用一次模拟上游。新增回归写入既有测试文件；未创建长期新测试文件，没有真实模型请求。

## 新入口、任务与保护边界

配置层明确保持 Prism、Codex ticket、WS mode router v2、prewarm、smart protocol、Codex anchor/pin、新图片 bridge、用户消息队列与 GitHub/Google OAuth 登录的缺省关闭。既有协议兼容是显式选中账号后的转换能力，不代表自动创建账号、后台登录或自动执行收费任务。

性能暂停只有 `EnabledIDs` 返回的显式启用账号进入评估，未知指标不等于零；受控实验创建为 draft，必须显式 Start、事务 claim 后才运行。定时质量任务仅读取已启用的到期计划，沿单 writer 激活和准入/排空生命周期。公开/普通用户质量视图仍只投影当前用户当前组、当前配置的脱敏结果，不能执行管理员质量任务。完整后台生命周期与 HA 接线边界见 `PK_QUALITY_WORKER_PK61_REVIEW.md`，共享证据及 API Key 有序权限见 `PK_ACCOUNT_COMPAT_PK61_REVIEW.md`。

新 Messages 直连发送前保留真实组的推理上限/禁止策略、最终发送强度和服务档位处理；模型权限在 handler 实际组准入，不能因桥接而跳过。全项目首字三模式、两开关关闭时上游计费一致性、区域金融和同制品门禁仍以主集成专项为准，本表不能替代这些验证。

## 三个上游声明

- `apiKeyRepository.entityWithGroupIDs`：由 PK `apiKeyEntityWithGroups`/`attachCandidateGroups` 接续，JSON 顺序为权威，关联表仅同事务镜像。
- `APIKeyService.validateAPIKeyGroups`：创建/更新经 `requestedAPIKeyGroups`、`NormalizeAPIKeyGroupIDs` 和 `validateCandidateGroups`，保序去重、最多二十组、nil 继承/显式空清除、逐组有效状态与用户许可保持。
- `OpenAIGatewayService.shouldForceOpenAIFastPriorityForMissingTier`：主集成复核共享 fast policy 新入口；本子专项不重复宣称独立运行覆盖，最终证据由主集成登记。

本次 34 项均有恢复或明确替代链，另发现并修复了新增直连的 reasoning-only 行为遗漏。源码审查和本机定向完成；正式构建、发行、逐节点更新与生产五分钟连续性由后续制品及运维回执证明。
