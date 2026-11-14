# pk.65 调用失败与缓存回填优化

本轮从 `3f6f74ecb0d07b08a90506b414cdb3393b118d09` 继续，复用未发行的 `v0.2.19-pk.65`。用户在近期生产调用错误诊断后要求“优化”。保留本版本已有分组 Ultrafast、凭证运营与智能运维工作台；不合并其他上游变更，不修改依赖或迁移。

## 问题依据

北京时间 2026-11-15 00:44—01:44 的固定只读窗口中，按请求 ID 去重得到 90 个加密历史失败、11 个计费预约 503、2 个上游模型不支持导致的 502。加密失败含 3 个 HTTP 200 后流内失败；另一个已恢复的 WebSocket 403 不计入失败。usage 记录不能单独证明整条流成功，未用这些计数计算错误率。

主要承流节点的 Redis GET 只读样本为 53—116ms，而三个配额回填分支采用 50ms 期限，同窗口出现 153 条回填超时告警。此证据支持调整回填预算，但这些写失败只告警，不能据此将 11 个预约 503 全部归因于该超时。生产身份、真实地址、原始请求和私有证据均留在仓库外。

## 实施范围

- 三处配额回填统一使用独立的 250ms 预算，涵盖无限额占位、数据库记录回填及过期窗口刷新。保留取消隔离、TTL、限额判断与现有回写语义，不把写入失败改成放行新的金融路径。保留原有数据库查询 singleflight，不新增缓存覆盖合并。
- 原生 OpenAI 兼容转发识别明确的模型不支持 404，与已有有限换号链路衔接；同一请求不再在已明确拒绝该模型的账号上重复尝试。只读取上游错误字段，防止把错误回显的请求文本、鉴权或 URL 不存在误判为模型不支持。无可用线路时返回固定、脱敏的服务端路线错误；管理员错误透传规则继续优先。
- 现有非池账号及显式管理员策略的模型级冷却保持。默认转发池保留跨请求不写长期负缓存的行为，避免多层转发把短期不可用放大为长时间禁用。因此本轮不声称已经对默认转发池实现跨请求熔断；永久模型能力缺口仍需管理员维护真实支持范围。
- 加密历史按实际 `input` 项识别携带非空 `encrypted_content` 的 `agent_message`、`compaction` 和 `compaction_summary`，包括项内的加密内容片段；不以用户文本中的关键词判断，也不阻断普通加密 reasoning、明文摘要或首次准入。已有验证归属继续使用原检查；未知归属的不透明历史在失败后跨账号之前停止自动恢复，无工具结果时保留原有有限同账号重试。
- 未知归属的不透明历史若包含工具结果，HTTP 外层和原生 WebSocket 内部均阻止有歧义的重放。WebSocket 在实际请求发送后的读写异常、缺失完整终态及上游错误事件处停止自动重发；连接建立前的安全重试仍使用原策略。新的 `409 / encrypted_history_recovery_unverified_owner` 指导保留完整任务上下文、核实原账号和工具执行结果；真正的上游 `invalid_encrypted_content` 保留原错误代码和参数，并补充恢复说明。不得删掉不透明内容来伪造恢复成功，也不重复已完成的工具动作。
- HA 计费预约新增固定白名单的失败阶段、原因和耗时诊断；不输出底层错误正文、地址、凭证或请求内容，对外仍使用既有通用 503。保留缓存故障拒绝放行，不加入余额数据库覆盖、未知结果 EVAL 重试或上游重放。

## 二开保护与 HA 交付

本轮扩展 PK-006、PK-047，沿用 PK-021、PK-042、PK-045、PK-058；无新增功能 ID。PK-003/004、PK-007～012、PK-028/044/046 的计费金额、usage、首字、工具结果和代理边界保持。

生产使用独立 HA profile，不能只用普通主线的计费实现解释或验证。新 profile 从已核验的 pk.64 HA 输出与当前 pk.65 源码进行逐文件三方合并，保留两个后台 provider 启动门修复和审计 Drain 修复；不改写历史 profile、旧标签或封存制品。正式制品验收与生产更新是后续独立动作，profile 生成或源码测试通过不代表已经上线。

新的 `deploy/cluster-updater/overlays/pk65/` 已按 96 条原 overlay 路径生成，独立应用补丁后逐文件核对 SHA256 通过。四处源码交叉为 `openai_gateway_handler.go`、`billing_cache_service.go`、`wire.go` 和 `wire_gen.go`；当前工作区的 114 条相关输入与冻结快照一致。profile SHA256 为 `aaa55d48ccca5c95e17145efd27db89a174502b47682c6b258f43ef07c18196f`，patch SHA256 为 `b32b84eeb501ce9d4b1a3043995e4c8f4ffa7cd1a3df962d622106af3cba6b41`。源码提交后须用 `prepare_candidate_overlay.py` 对实际提交再次核验，生成绑定该提交的新候选；不能直接把工作区准备目录当成正式制品。

本次针对加密历史的修改减少不安全的自动重试并明确恢复路径，不能解密或修复已经损坏、归属不兼容的上游密文。模型拒绝只有在存在可用且支持原模型的其他账号时才能恢复；没有可用线路时返回 `503 / openai_model_route_unavailable`，不暗中替换模型。计费诊断为继续定位 503 提供证据，不代表本轮已经证明或消除了全部预约失败根因。

## 验证与交付状态

验证在本机隔离模拟环境完成，使用模块要求的 Go 1.27.3、`-p 1 -tags unit`，编译缓存与 `GOTMPDIR`、`TEMP`、`TMP` 均设在 D 盘，`GOMAXPROCS=2`。加密历史最终批次另设 `GOMEMLIMIT=3GiB`；HA service/handler 批次未显式设置此项，不将其记为已设置。没有连接生产数据库或发送真实模型请求。下表只统计有 `Test` 字段的通过事件，不把包级通过重复计数；各范围存在重合，不相加宣称独立覆盖数量。

| 范围 | 顶层通过 | 含子项通过事件 | 失败 / 跳过 |
| --- | ---: | ---: | ---: |
| 普通配额回填、限额与过期窗口 | 18 | 26 | 0 / 0 |
| 普通模型阶段：404 识别、三类 HTTP 入口换号、透传与默认池冷却边界 | 15 | 115 | 0 / 0 |
| 加密历史定位、恢复指引、HTTP/WS 防重放及原 Issue494/715 回归 | 67 | 271 | 0 / 0 |
| 最终 HA service/handler：金融保护、回填、模型、历史及后台启动门 | 128 | 486 | 0 / 0 |
| 最终 HA server：Drain、readiness、cleanup 与 Wire 编译 | 9 | 16 | 0 / 0 |

加密历史最终执行集合：`go test -p 1 -json -tags unit -count=1 ./internal/service ./internal/handler -run 'Test(UnverifiedOpaqueHistoryRecoveryScope|EncryptedHistoryClientGuidancePreservesStructuredError|OpaqueHistoryHTTP.*|OpaqueHistoryWS.*|OpaqueCompactionWS.*|ForwardOpenAIWSV2_OpaqueToolResultRetryBoundary|Issue494.*|Issue715.*)$'`。真实 WebSocket 夹具覆盖读断线、缺失完整终态、服务端错误三类已发送请求故障，并验证握手 503 后恢复和普通 reasoning 重试；`write_request` 通过实际写入错误包装与守卫位置的代码审阅确认，没有宣称执行了真实半写连接故障测试。

模型阶段日志保存了实际执行集合，包含 `TestPK65ModelNotFound404RoutingOnWire`、`TestPK65ModelRouteExhaustionPreservesPassthroughRules`、`TestPK65ModelNotFound404FailoverSkipsSameAccount` 及原池级/非池级负缓存、工具协议与错误脱敏回归。原始宽匹配命令未保存，不把后来从日志重建的正则冒称原始命令。配额夹具用 100ms 写入延迟验证三条回填路径，并验证卡住的写操作仍有界取消及原限额判断保持。

上述普通模型阶段之后，最终静态复核发现无结构化错误码的 `Model <模型名> not found` 句式被过窄的匹配遗漏；新增精确句式兼容及负例后，由最终 HA 批次重新验证完整模型测试集合。普通模型阶段的 115 次通过只对应当时源码，不替代这项末轮修正的验证。

早期失败记录保留：模型新夹具一度错误假设透传会改写别名，且重复 Messages 输入触发既有续链；加密历史新夹具一度把 SSE 失败当成 HTTP 错误闭包，并要求握手失败必须收到 409 帧。这些新增断言已按实际协议合同修正，未削弱原有测试。C 盘临时空间不足、并发编译内存不足和中止的冷缓存编译不计为通过，随后改为 D 盘临时目录和串行执行；HA 早期 preview 的编译资源失败也不计为有效动态验证。

HA `final-1` 批次中 service 测试包因 Windows `VirtualAlloc errno=1455` 编译失败，未运行测试；同批 handler 的 47 个顶层、223 个通过事件有效，但该批整体退出码为 1。保留原失败结果，不作为最终全批通过；后续使用明确的进程内存预算验证新的冻结快照。

HA `final-2` 使用末轮模型兼容修正后的冻结源码，profile 和 patch 与已审阅值相同，114 条相关输入再次核对一致。service/handler 批次明确设置 `GOMEMLIMIT=3GiB`、`GOGC=50` 和 `GOMAXPROCS=2`，两个包均退出成功：service 81 个顶层、263 个通过事件，handler 47 个顶层、223 个通过事件，零失败、零跳过。测试前后完整冻结源码哈希保持一致，日志 SHA256 为 `b3f85dc9f31e795689f347eafc7b8873a8783dcf0e8d5448ea7e992daa67bb0f`。

最终 HA server 使用相同内存和临时目录约束，执行 `go test -p 1 -json -tags unit ./cmd/server -run '^(TestHA.*|TestProvideServiceBuildInfo|TestProvideCleanup_WithMinimalDependencies_NoPanic|TestProvideCleanupGracefulWithMinimalDependencies)$' -count=1`，9 个顶层、16 个通过事件，零失败、零跳过，实际 Wire 接线编译通过。最终 HA 三包合计 137 个顶层、502 个通过事件；完整冻结源码清单 SHA256 为 `648ef8c1b41e6e45a6c4a6a2fda36975d98f581ecfe0c50d28340335afd85421`。精确命令、原始 JSON 日志、源码清单及失败记录留在仓库外的本机验证目录。

本轮交付源码和经过验证的独立 HA 适配，复用 `v0.2.19-pk.65` 并按维护流程提交、推送至该分支与 `main`。提交后的候选准备还须核对实际提交与全部输入哈希，结果单独留存，不修改上述已验证源码或历史 profile。未运行全套源码回归、正式制品验收或生产故障演练；未创建正式标签或 Release，未进行生产配置修改或重启，不能把源码完成写成线上错误已消失。
