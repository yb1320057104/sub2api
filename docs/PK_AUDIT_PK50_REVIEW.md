# pk.50 审计可用性、快速模式与计费可见性

从私库 main `da1ec74e` 对照 pk.49 继续。此文仅描述本轮审计部分，不替代同版本的代理/币安核对和正式发布验收。

## 行为与原因

提示词节点的网络超时、连接失败、401/403、429、5xx、无可用节点、密钥无法解密、并发满和配置未加载原先汇成同一 unavailable，后台无法区分。现在给管理员提供固定安全原因；仍不输出 token、URL 凭据、请求正文或不可信响应。网关技术故障仍拒绝严格审计请求，不能把失败冒充违规或安全结论。

worker 的最后错误原先成功后也永久展示。新增错误及恢复时间，只有在错误之后开始且完整成功的实际审计才恢复；更早开始的并发成功、移出检测范围、跳过任务不能清除新失败。同步评估也保存安全故障分类和恢复状态。节点目录探测文案明确它只证明目录可访问，不冒称真实模型审计成功。

2026-11-03 父代理对生产 pk.49 的只读核对证实：内容审计处于观察模式，提示词审计使用本地规则且未开启同步阻止。近期提示词任务存在两条历史 dependency failure，之后已持续处理成功，页面仍显示旧错误符合上述缺陷；不能据此断言当前节点一直不可用。此次抽样的提示词事件全部为正常放行，内容命中日志属于没有风险动作字段的旧观察结果。分组已打开自动扣款也不会把这些观察结果转成收费事件；本次不更改线上模式、不追溯收费。`charge_allow` 的实际行为由测试及每条完成事件核实，不把当前观察配置推导成前置模式实现错误。

提示词新增默认关闭的快速模式，需显式选择、开启同步阻止、保留启用的本地规则和远端节点。本地前置仍按风险分级执行；允许继续的请求将远端复核放入原有有界队列，不等待远端。远端复核仅记录和展示，不中途拦截、不计封禁、不额外扣款。旧严格模式保持同步等待和失败拒绝。内容审计新增 `keyword_async_api`：关键词和既有 hash 前置，本地分级动作保持；未命中才后台 API 观察。异步远端检查不再执行旧布尔违规累计副作用。

最终宽范围回归发现旧测试仍要求观察命中写入后续拦截用 hash，此要求与观察仅展示的契约冲突。现按实际阻断结果控制旧 hash 与违规副作用；观察/快速后台仍保存命中、分组和动作证据。进一步修复旧 `CountFlaggedByUserSince`：排除 `action=allow` 与 `mode=observe`，避免稍后严格命中把先前的观察结果追算成封禁次数。原 type4、同步关键词/API 拦截、豁免和封禁后重置条件保持。该查询位于 `backend/internal/repository/content_moderation_repo.go`，不属于前一份12源文件迁移证据，须单独标明 SQL 验证范围。

前置是审计时机，不是强制 block。已检查 `keywordRiskDecision`、`apiRiskDecision`、LegacyModerationAdapter、Coordinator、HTTP/WS ranking recorder、完成后 usage identity 关联：`allow` 正常放行，`charge_allow` 正常生成且以真实正常完成用量结算，只有 `block` 在生成前拒绝。没有发现此版本在有风险策略时仍无条件进入旧关键词 block 的证据，测试扩展覆盖三动作与快速策略。不能以用户看到零成本直接认定其请求属于 block；应使用每条事件动作、实际费用、分组及扣款原因核实。

自动扣款继续使用真实完成费用 × 分组破甲倍率，精确 NUMERIC、原有幂等与金融子事务；自动封禁和自动扣款开关独立。实际 block 没有生成成本，charge_allow 遇到真实成本0、余额不足、分组未启用或用户豁免时不扣款，明细显示原因；不补扣历史，不将观察命中变成收费身份，不扩大手动扣款资格。

## 入榜及显示

兼容新增迁移 `261_audit_visibility.sql`：内容/提示词审计新增命中的观察、warn、普通放行结果分别写入独立只展示证据表。仅新 INSERT 捕获，无历史回填。按照来源日志主键去重，不依赖客户端 request_id；两套观察可能各有一条命中记录，界面明确这是观察次数而非新增处罚次数。实际 block/charge_allow 继续既有按服务器请求/WS轮次去重的处罚链，不重复写入观察证据。观察表不注册金融、用户封禁或累计触发器，写失败只产生固定 warning，不损坏原审计日志；原日志清理不删除独立证据。

管理员主榜新增真实自动扣款笔数/金额和触发分组，接口额外返回观察次数；真实自动扣款汇总来自 `cyber_policy_auto_charges` 的成功记录，不能再因审计事件旧 `deducted_count=0` 看不到实际扣款。原手动待扣次数及资格保持。触发记录显示来源、动作、风险分、组快照和未扣原因；未开启组自动扣款时不展示破甲倍率公式，不改保存值。公开榜仍不返回管理身份、分组或财务信息。

## 验证状态

开发中定向验证、最终验证及正式隔离数据库验收分别记录，不将计划作为结果。初次前端5文件63项通过；其后的快速提示补充曾有类型检查缺少样式字段失败，已修正，最终复验如下。Go沙箱编译挂起已取消并获授权重跑；并行代理编辑曾导致 service 包临时未定义方法，失败日志保留。未调用真实模型、授权、币安，未部署生产。

最终前端审计定向测试 5 文件 65 项通过；英文榜单说明一致性修正后再跑榜单与 i18n 完整性 2 文件 5 项通过，`vue-tsc --noEmit` 通过。service 定向测试通过，涵盖前置三动作×三关键词策略、快速后台观察不增加旧违规计数、风险判断、排行榜查询映射及原 charge_allow 完成结算。handler 定向测试通过。新增 service 测试最初误用未持有配置的异步任务，已按真实 worker 的加载流程修正夹具；不改产品代码绕过测试。

提示词最终 `go test -tags unit -p 1 ./internal/securityaudit -run 'TestPrompt|TestGuard|TestEnqueuer' -count=1` exit0，包耗时0.281秒，日志 `.runtime/pk50-audit-security-final.log`。补测验证快速模式本地放行时仍在effective blocking下提交有界队列，并由后台远端完成观察；旧严格模式远端失败仍拒绝，本地block不触发远端。新增夹具初次误用具体构造器和并行服务中间态编译失败已修正/等待补齐后复跑，不作为产品成功证据。

独立PostgreSQL普通/分区两种usage布局的本轮工作树迁移验收共12组通过，其中审计部分每种布局运行六项观察断言和真实public/admin排名、records的六段PREPARE+EXECUTE；包含源主键去重、观察无处罚/余额副作用、源日志清理后保留及组快照。合成扣款0.125×2=0.25核实，未读写生产财务。当前迁移261与cyber_policy.go源哈希匹配，DinD及夹具清理均通过。脱敏报告 `.runtime/pk50-migrations-safe-evidence.json` SHA256 `4f51d2444e7bbc190aeccf0deb6149c8068c8db91480ebdefc4293ffc84c3fe6`，明确 `source_state=working-tree`、`release_evidence=false`；不能将该SQL验证当作全部Go repository方法或最终正式制品验收，后者由父代理继续完成。

首次宽范围最终 Go 日志已保全为 `.runtime/pk50-final-go-tests-attempt1.log`，整体失败，包含旧异步 hash 预期及管理配置测试夹具把同一 JSON 返回给所有 setting key 导致的500。后者已按真实设置仓储的 key 隔离修正夹具，没有放宽产品 allowlist 校验。观察测试改为同时验证纯观察/快速后台保留命中、分组，且不缓存、不封禁、不累计；随后严格命中只计自身一次，原严格模式缓存测试保留。补充的 `.runtime/pk50-audit-ban-counter-fixture.py` 直接抽取实际查询生成事务临时表 SQL，覆盖真实 PostgreSQL 的过滤及旧计数语义，生成不等于执行；最终执行结果由对应证据记录。

上述修复后 `go test -p 1 ./internal/service ./internal/repository ./internal/handler/admin -run '^(TestContentModeration|TestRecordCyberPolicy|TestApplyFlaggedAccountSideEffects)' -skip 'TestContentModerationTypeSafeLive' -count=1` 整体 exit0，三包分别2.515s/0.678s/0.353s，完整日志 `.runtime/pk50-audit-followup-go.log`。子进程明确清空可选真实PostgreSQL/Redis/TypeSafe测试环境变量，仅运行本地模拟；sqlmock覆盖排除条件，不能替代实际PG执行。独立代理只读审阅了实际阻断控制、观察过滤、原type4与重置边界、设置夹具以及SQL临时表方案，未发现重要问题；未将只读审阅冒称运行测试。
