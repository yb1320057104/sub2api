# v0.2.12 合并核对

## 来源与范围

- 合并前私库：`162a58972bee08078a43eefa761da1b245f667fa`，已发行 `v0.2.11-pk.2`。
- 上游输入：`Wei-Shaw/sub2api` 的 `v0.2.12`；实际 commit `5106065716e494204fc0e8db16f68f6e9d576be0`，annotated tag 对象 `cbe9966432317e6757b47f598a103d2132ec27db`。
- 独立分支：`sync/upstream-0.2.12`，使用双父提交合并保留上游关系。
- 用户要求先合并并发布新包，再调查其提供的计费绕过描述。本次发行不包含后续漏洞调查结论，也不自动更新生产。
- 无新增 PK ID；更新 PK-009 的新增异步用量入口、PK-017 的候选和云端覆盖、PK-022 的普通充值与币安隔离边界。
- 上游标签中的 VERSION 仍为 `0.2.11`，本分支对齐 `0.2.12`。正式目标为从未使用的 `v0.2.12-pk.1`。

## 文本冲突和新增入口

1. `backend/internal/server/router.go`：普通支付路由保留上游新增 Redis 参数和匿名查询限流，同时保留 `RegisterBinanceRechargeRoutes` 与原权限中间件。
2. `backend/internal/service/admin_account.go`：创建账号先保留 OpenAI 缓存转换 extra 归一化，再执行 TypeSafe 仅 API Key 类型校验，两者同时生效。
3. 新 `/v1/systemone` 使用独立 TypeSafe 平台，协议只支持同步、输入/输出 usage，不把 OpenAI 专属缓存转换和 SSE 占位套到该平台。模型列表、Composite 路由、额度、内容审核和提示词审计沿用上游接入。
4. 新增 TypeSafe 用量任务原先在异步闭包中访问 Gin context；改为入队前快照渠道字段，避免回收或复用后读错请求。回归扩展在该入口原有测试文件中。
5. 新上游两份 `241_*` 迁移和已有首包迁移文件名不同；迁移器按完整文件名和 SHA256 跟踪，保留历史文件，不改名或覆盖已执行迁移。

## 二开逐项核对

下表是静态核对结果，当前提交的动态证据见末节；静态结论不代替云端检查。

| PK ID | 静态结论 | 依据 |
|---|---|---|
| PK-001 | 已保留 | `admin_account.go` 冲突保留缓存配置归一化；创建/编辑页仍保存独立 OpenAI 开关与倍率，TypeSafe 条件不改变 false/0 和 extra 合并。 |
| PK-002 | 无影响 | `cacheconversion/usage.go` 与转换实现未变，decimal 向下取整、溢出、倍率 0 和创建明细清零保留。 |
| PK-003 | 已保留 | OpenAI 原始输入拆分、转换一次、余额/订阅/API Key/账号额度同源未变；TypeSafe 使用独立原始输入/输出 token 进入公共结算。 |
| PK-004 | 已保留 | `billing_service.go` 仅增加 TypeSafe 默认价格；原始上下文选档及账号原始/客户 TotalCost 统计分支保留。 |
| PK-005 | 已保留 | OpenAI JSON/SSE/WS/图片/embeddings usage 改写入口未被替换；Grok 身份头变化不改变 usage 与正文。 |
| PK-006 | 已保留 | 新 TypeSafe 同步协议不属于 OpenAI 转换平台；OpenAI 直通、桥接、回退与已接入出口保留，原 OAuth 图片和二进制 WS 边界不变。 |
| PK-007 | 已保留 | 设置 handler/DTO/API/UI 同时保留三模式、默认关闭、1–2 秒、局部保存与缓存；新增充值优惠字段不替换首字设置。 |
| PK-008 | 已保留 | 心跳/空格实现未变，Chat 三帧、真实 Responses ID、索引/序号/终态、结构化 JSON 与 Messages 回退仍在；非流式 TypeSafe 不注入 SSE。 |
| PK-009 | 已保留并修复新入口 | 双计时 SQL/DTO/异步快照保留；TypeSafe 渠道字段入队前快照，未测量 first_response_ms 仍为 null。 |
| PK-010 | 无影响 | 普通用户首字映射、管理员双计时、导出和公开渠道监控脱敏未变，新增平台沿用这些输出边界。 |
| PK-011 | 已保留 | Grok client header 改动保留 transport 重试门；comment/ping 扣除、space 正文提交/原子取消、排队与周期保活实现未变。 |
| PK-012 | 已保留 | writer 写锁、header 克隆、短写/取消、Stop 等待、晚到协议错误与 Server-Timing 范围未变。 |
| PK-013 | 已保留 | PK出版标识和 pk 数字排序未变；源码 VERSION 对齐 0.2.12，候选从该文件取版本。 |
| PK-014 | 已保留 | 更新/恢复来源、GitHub 凭据及缓存隔离未变；已实查源码仓库与 GHCR 包均 private。 |
| PK-015 | 无影响 | 私有下载授权、跨域去凭据、SHA256、固定安装路径及原子替换未变。 |
| PK-016 | 无影响 | Dockerfile/entrypoint/数据卷可执行文件与镜像种子机制未变。 |
| PK-017 | 已保留并扩展验证 | 私库同步分支纳入候选构建，版本自动读取；新增上游 UI 回归及 TypeSafe 上下文 race，正式发行仍手动、同二进制、私有镜像和正式附件。 |
| PK-018 | 无影响 | concurrency provider 及跳过启动清理开关未变，过期槽位回收保留。 |
| PK-019 | 无影响 | 19 项后台启动门和停止顺序未变，上游新平台/优惠未新增常驻后台服务。 |
| PK-020 | 已保留 | 维护文档保留独立卷、连接排空、后台交接和部署授权边界；发布不连接或更新生产。 |
| PK-021 | 已保留 | Git 内约束、唯一清单、PR模板和维护 skill 保留，新增合并记录并同步清单。 |
| PK-022 | 已保留 | 普通优惠仅走 payment_orders；币安独立表、倍率快照、八位舍入、同事务余额/累计充值/流水/去重及迁移保护未变。 |
| PK-023 | 无影响 | 每日消费嵌入 UsageView、默认今日/全用户、定时刷新和管理员权限未变。 |
| PK-024 | 无影响 | 实时账单只读、原精度、租约/冷却/错误边界未变。 |
| PK-025 | 无影响 | 专用香港网关、逐次预探测、固定令牌出口、失败关闭和配置注入未变。 |

## 首字整组行为检查

PK-007～012 按三模式配置、真实写出计时、协议输出及使用记录映射一起核对。既有流式 handler 到 writer 和 detached usage context 的链路保留；新增 TypeSafe 明确拒绝 stream，不能把其未测量值回填成上游 TTFT。space 仍只允许一次普通空格，真实 usage、工具参数和 Responses ID 保留；comment/ping 不改变正文和安全重试规则。上游 Grok transport 改动只更新身份头，PK 请求级 retry gate 保留。

## 验证记录

- 本地仅静态读取源码、diff、引用与格式，未运行任何构建或测试。
- 上游自带测试保留；本次回归只扩展已有测试文件，没有生成临时测试文件。
- 云端候选/PR CI 待执行：全部 Go unit/integration、lint、前端类型与关键回归、首字/计费/TypeSafe race、币安事务和候选构建。全部必需项通过前不合入或发布。
- 正式标签和 Release 工作流待执行：私有更新/下载、后台与首字并发、币安精度/事务、Docker 持久化及同二进制制品检查。
- 未覆盖范围仍见 PK_FEATURES.md；本次合并不等于生产更新、New API 串联、真实币安转账或全部严格 SDK 的验收。

## 升级边界

包含新增 payment_orders.bonus_amount 及 TypeSafe 平台约束迁移。升级前已发出的密码重置链接因上游改为哈希存储而失效，需重新申请。普通充值活动不影响币安的倍率和历史订单；既有币安倍率迁移的回退约束继续有效。
