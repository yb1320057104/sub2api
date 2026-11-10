# pk.61 账号与共享证据适配复核

本记录说明源码合并与本机定向证据，不能替代最终制品和生产 HA 验收。固定比较源为私库 `6ed612909fef4510f63d93a8f2ead58bae35a3c8` 与上游 `d42bb4fc4b3eef9821994b4ad170d4eb881eca95`。

## R18 实际指标与 HA 适配

原始上游的 TPS、TTFT、探针轮次降智率读取共享 PostgreSQL 的 usage/timing/quality 数据；HTTP 错误率则由 `accountResponseTransport` 写 `upstreamstats.Default` 内存环，再由 `readAccountPerformance` 读该进程环。独立 worker 不承接转发时，无法看见其他请求节点的 HTTP 样本。它与 R19 的 Redis 共享 5xx 触发队列不是同一系统。

新增 `upstreamstats.RedisStore` 沿原被动 HTTP 响应采集点工作。每个请求节点安装共享 backend，热路径只尝试写入容量 4096 的内存队列，不等待 Redis。后台按 256 条或 200 毫秒批量写入专用小连接池；Redis 全局保留至多 65536 条尝试，并同步维护按账号索引，保留最多 24 小时有效查询窗口。样本包含账号、状态码、时间、随机进程 ID 和递增序号，不含正文、用户凭据、代理或请求 URL。Lua 原子插入、裁剪和 `NX` 去重使同一批次重试不会多算；真实 503 后重试 200 是两次 HTTP 尝试。

查询仍按原精确时间范围过滤，并区分无样本 `percent=null` 与真实零错误 `percent=0`。不同进程生命周期、异步延迟及队列裁剪不能证明完整集群覆盖，因此共享窗口始终标记 partial；有效部分样本仍保持上游可评估行为。Redis 读取失败返回 `shared_http_responses_unavailable` 和未知值，不回落到 worker 本地样本，也不伪造零错误。其他共享 PG 指标继续返回，不受错误率存储故障影响。

`ProvideAccountPerformanceRepository(db, redis)` 必须在所有角色安装 backend；只有性能 evaluator 受单 writer 激活门控。HTTP 与后台探测排空后、Redis 依赖关闭前调用 `upstreamstats.CloseShared`，有独立截止时间，正常退出提交已接纳队列，超时终止网络等待并返回错误。最终 Wire 生成物及清理顺序由主集成检查。

已执行 `upstreamstats` 原有四项和新增四项；随后再执行真实 `NewHTTPUpstream.Do` 到两份独立 writer、第三份 worker `repository.Read` 的链路用例。跨节点 503+200 得到 50%、精确纳秒边界、NX 重放、全局裁剪与账号索引、过期未知、队列饱和不阻塞、Redis 故障未知和退出 flush 已通过。记录见 `.runtime/pk61/upstreamstats-shared-attempt1.log`、`upstreamstats-shared-chain-attempt1.jsonl`。测试仅用本机 HTTP 与 miniredis；没有真实上游调用或线上容量结论。

追加执行 `TestSharedResponseCloseReportsUnavailableStorage`，确认最后一批提交失败时退出返回错误、writer 已结束并拒绝新样本，记录为 `upstreamstats-close-failure.log`。

## 有序 API Key 分组与内部函数替代

上游 `validateAPIKeyGroups` 的行为由创建/更新入口的 `requestedAPIKeyGroups`、`NormalizeAPIKeyGroupIDs`、`validateCandidateGroups` 接续；逐组检查有效状态与用户绑定许可。最多二十组、保序去重、旧 `NULL` 继承单组、显式 `[]` 清空继续以 PK JSON 字段为权威。

上游 `entityWithGroupIDs` 由 `apiKeyEntityWithGroups` 和 `attachCandidateGroups` 接续：读取 JSON 顺序，批量补齐候选组信息，再按已配置顺序生成 `Groups`。`api_key_groups` 关联表只在 `mutateWithGroups` 同一事务中更新镜像，不反向覆盖 JSON 顺序。请求固定实际组后再做权限、RPM、扣费和重验证，不重写认证缓存母本。

旧 `updateEWMAAtomic` 已由每账号 mutex 保护的 `reportAt`/`snapshotAt` 接续：保留 alpha=0.2 的 TTFT/EWMA 更新，增加错误率两分钟半衰期与并发报告时间不倒退，数据仍由同一锁读取。

有序组、共享证据和历史保护专项有十五个顶层用例通过；仅旧认证快照版本硬编码 26 与本轮 34 不一致，改为断言当前版本常量后单项通过，顺序、分组权限与独立副本断言保持。日志为 `account-compat-targeted-attempt2.jsonl`、`apikey-ordered-cache-retry.log`。

## PK 会话保护与新明确身份

二次声明审查发现上游显式身份与精确请求指纹不能等价替代 PK 历史前缀保护。已恢复 `openai_cyber_transcript.go`：仅有模型生成历史后才记录最后用户回合前的上下文键，避免不同首轮模板互相影响。无明确身份且来源 scope 已因命中激活时，保留历史前缀/改写后续回合匹配和 256 候选上限溢出保护。

明确 thread/session 身份保留上游 v3 类型区分和 v2 读取兼容，不进入粗 scope/历史匹配；缓存亲和提示不是明确身份。匿名精确请求命中后即便补加 session ID，仍查上游精确指纹，不能绕过已有封禁。明确身份写入不激活历史 scope。HTTP/WS 使用带 scope 的统一读写方法，默认开关和失败策略保持。

service 定向回归覆盖恢复的六项 PK 历史行为及上游身份/指纹隔离。上游旧测试直接用底层方法把明确身份与粗 scope 同时激活，已改为经实际明确身份写入方法构造夹具；真实入口不应激活该 scope。独立的已激活 scope 溢出测试继续要求阻断。首轮与修复后复验日志分别为 `cyber-scheduler-compat-attempt1.jsonl`、`cyber-compat-retry1.jsonl`；handler/WS 接线及最终制品由主集成复核。
