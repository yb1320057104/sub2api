# v0.2.17 上游合并及计费审计（2026-10-19）

## 固定来源与范围

- 合并前私库 `main`：`82689422e5bb1444769265534265f570fed274a2`，包含恢复 v0.2.16-pk.1 源码的授权回退；PK-029/030 继续移除，不重新引入隐藏倍率或 Token 缩放。
- 上游正式版本：`Wei-Shaw/sub2api` 的 `v0.2.17`，固定 commit `060d0160417782cd962a0c7f460f6cd4565d4916`。Release API 确认为最新正式版，发布时间 `2026-10-16T04:41:43Z`；不以之后的 upstream/main 代替它。
- 私库 API 确认 `pengkang0322/sub2api` 为 private，开始时无在途 PR，本地/远端无该版本工作分支。从已核对的 origin/main 创建唯一分支 `codex/v0.2.17-pk.1`；历史分支未删除、改名或强制改写。
- 本次范围为上游源码合并、全部有效二开核对及普通 OpenAI API Key 计费审计。无新增二开；扩展 PK-017 验收清单和 PK-028 合并回归。正式发布和线上更新不属于本次操作。

## 冲突、交叉入口和上游更新

1. 仅 `openai_gateway_response_handling.go`、`openai_gateway_passthrough.go` 存在文本冲突。上游为合成 `response.failed` 增加 `created_at` 和 `sequence_number`；私版在同一调用前处理 Kilo 客户端错误。保留上游时间跟踪和序号，同时使用已脱敏的客户端错误副本，管理员诊断仍保留原错误。扩展原 `openai_response_model_rewrite_test.go` 的 `TestPKKiloBridgeSynthesizedFailurePreservesUpstreamMetadata`，检查两条真实处理路径的响应 ID、时间、序号、正文、客户端脱敏和内部原错误保留。
2. Responses JSON/SSE 的时间字段补齐继续经过既有缓存转换/Kilo 响应重写，space writer 继续在下游维护生命周期与序号。WebSocket HTTP bridge 先收集原始 usage，再经 ingress 下游 writer 转换；未新增重复转换。
3. OpenAI API Key 的 Messages Web Search 模拟保留上游入口和全局/账号或渠道开关，位于现有 handler 早响应 writer 与 Kilo 分组强度策略之后。它调用配置的搜索服务，不是 OpenAI 原生搜索；客户端估算 usage 和内部计费边界单列于专项审计，不冒称官方用量。
4. 上游 `gateway.min_output_tokens` 配置及协议转换接入完整保留。未改变 PK 缓存默认值、倍率、原始上下文和真实用量采集；GPT-5.5 订阅退休规则保持上游凭据判断，API Key 不受该退休规则限制。
5. Gemini 4 Argon/思想档位与模型目录更新、Gemini/Vertex 错误处理、Antigravity 工具及图片兼容、Codex 工作区导入隔离、composite 凭据路由和 Anthropic base URL 修复一并吸收。
6. 普通支付保留 EasyPay 主动确认与查单修复、未知订单类型拒绝、Stripe 从同订单本地快照读取 client_secret、待定退款保留扣减及确认失败后原子归还。币安独立订单表、精确金额、签名流水查询及入账事务保持独立；共享余额均通过原子 SQL 增减。
7. v0.2.17 没有新增迁移或周期后台；PK 的 20 项启动门和安全退出顺序不变。上游回退账号筛选折叠 UI 时删除其自有 `AccountTableFilters.spec.ts`，无 PK 依赖、默认测试清单无悬空路径；其余上游回归完整保留。现有 Stripe 安全回归补入 `FRONTEND_CRITICAL_VITEST`，确保默认九套件覆盖。
8. 上游 tag 内 VERSION 仍为 `0.2.16`，私版源码明确对齐 `0.2.17`；PK 发行后缀由 Jenkins 注入。没有启用 GitHub Actions、变更正式来源或扩大二开审计豁免。

## 全部有效二开核对

下表为源码及调用链静态结论。实际动态结果由本次完整暂存 tree 的服务器报告和最终提交 notes 证明；文档不预填通过，也不以历史报告替代本次验证。

| ID | 静态结论 | 依据 |
|---|---|---|
| PK-001 | 已保留 | 创建/编辑的缓存默认 false/1、倍率 0、局部 extra/批量校验不变；Codex 工作区导入修复未覆盖缓存设置，原显式默认覆盖边界仍在。 |
| PK-002 | 无影响 | decimal/Floor/int32 检查与创建明细归零算法未改，关闭直接返回原 token。 |
| PK-003 | 已保留 | RecordUsage 先拆普通输入、转换一次、溢出零扣费及原用量证据；余额/订阅/API Key 额度同一结算结果与预留生命周期保持。 |
| PK-004 | 已保留 | 原上下文选档与 rawTokens 账号成本分支保留；上游新增 Gemini 价格及 Claude 档位别名修复不改变 OpenAI 客户倍率/账号统计边界。 |
| PK-005 | 已保留 | 时间字段补齐后仍走下游副本转换，内部原 usage 不变；关闭不执行 PK usage 转换。 |
| PK-006 | 已保留 | Responses/Chat/Messages、桥接/回退、直通、WS、图片、embeddings 接入点保留；新 WS HTTP 时间补齐继续经过同一输出 writer；Web Search 模拟单列边界。 |
| PK-007 | 无影响 | 三模式枚举、默认关闭/comment、保存/读取/校验和中英文说明链未改。 |
| PK-008 | 已保留 | 三入口仍安装 early writer；真实响应 ID、空格生命周期、Messages/结构化输出回退、禁用仍测量保持。 |
| PK-009 | 无影响 | 非空真实写出、CopyFirstResponseTiming、异步上下文、全部 SQL 路径与 nullable 字段未改。 |
| PK-010 | 无影响 | 用户 DTO 映射首包、管理员双指标、导出和公开监控隐私边界保持，未测量值仍为 null。 |
| PK-011 | 已保留 | comment/ping 字节扣除和周期保活、space 原子重试门控仍覆盖 handler/forward/fallback；新搜索入口无新增模型重放。 |
| PK-012 | 已保留 | 写锁/响应头隔离/停止等待和晚到协议错误保持；合成终态新增时间字段兼容且不暴露 Kilo 内部错误。 |
| PK-013 | 已保留 | VERSION 对齐0.2.17，PK出版展示和数字修订排序逻辑不变。 |
| PK-014 | 无影响 | 私库更新/恢复来源和资产客户端保留，仓库 private 已核实，上游只作为合并输入。 |
| PK-015 | 无影响 | 下载授权/跨域去凭据、SHA256、重复更新固定路径和备份逻辑未改。 |
| PK-016 | 无影响 | 私版 Docker entrypoint、数据卷执行程序、种子校验和非 root 路径未改。 |
| PK-017 | 已保留 | 原九套件/preflight/最终提交 notes/Jenkins 同制品门禁保留，Stripe 安全回归补入关键列表；无本地发行构建、无 Actions。 |
| PK-018 | 无影响 | 启动时跳过全局并发清理与持续过期槽位清理 provider 未改。 |
| PK-019 | 无影响 | 20 项启动门、先取消等待再停止服务、Gemini 恢复 worker 取消传播及等待保持；无新周期后台。 |
| PK-020 | 无影响 | 私库恢复、端口/数据/密钥保护、排空和后台交接文档未改；本次不操作生产。 |
| PK-021 | 已保留 | AGENTS/项目约束/唯一清单保留，版本单分支规则及逐项核对落实，无新增 ID。 |
| PK-022 | 无影响 | 币安独立事务/表/倍率快照/五位金额/凭据加密不变，不复用普通支付确认与退款入口。 |
| PK-023 | 无影响 | 管理员每日消费仍复用全部使用记录和原筛选/刷新生命周期，未改账务来源。 |
| PK-024 | 无影响 | 实时账单只读 API/管理员权限/日期账户筛选/币安锁和冷却保持。 |
| PK-025 | 已保留 | 共享 config.go 新增上游网关设置，但币安专用配置/认证网关/HK探测/固定出口/失败关闭未改。 |
| PK-026 | 无影响 | 排行脱敏/管理员扣费预览/decimal 余额事务/逐条去重及原迁移保持。 |
| PK-027 | 无影响 | 默认关闭、独立累计、阈值触发器、管理员豁免、手动解禁重计与鉴权 outbox 保持。 |
| PK-028 | 已保留 | Kilo 默认关闭、强制HTTP、账号模型/代理、工具参数、原始强度计费与响应隐私保持；错误终态与上游字段兼容，扩展已有回归。 |

PK-029/030 的源码和迁移继续不存在，仅保留授权移除记录；没有重新加入隐藏倍率或原始 Token 趋势功能。

## 验证与交付证据

完整暂存 tree 使用 `python -B scripts/test-server.py` 上传到用户已指定测试服务器的随机隔离 DinD 环境，工具镜像 `pk-source-tests:20261015`。Windows 只进行源码编辑/格式化、调度和报告绑定；测试在服务器执行，不连接生产数据库/数据卷，不编译发行程序。

只有 preflight 和九套件全部通过、原报告 `source_eligible=true` 且测试前后 tree 一致，才允许提交相同 tree、绑定最终 commit 并推送 `refs/notes/pk-source-validation`。最终 commit/tree、原始日志 SHA256、真实执行时间和报告位置记录在 notes/PR；失败则保留源码及真实日志并修复后重验，不伪造通过。

本次新回归加入仓库原有测试文件，上游原有测试作为上游源码保留；自动产生的测试目录、容器、卷及临时夹具由入口清理。普通 OpenAI Key 计费详情见 [专项审计](PK_OPENAI_KEY_BILLING_AUDIT_0.2.17.md)。源码模拟和数据库回归不等于真实 OpenAI 付费账单核对、Kilo 模型请求、下游 SDK 串联或真实币安转账验收。

本次不执行 Jenkins 构建、正式发布或生产部署；线上实际版本未核实。
