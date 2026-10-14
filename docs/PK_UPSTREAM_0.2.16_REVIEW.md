# v0.2.16 合并及 Kilo 桥接核对（2026-10-15）

## 来源与交付范围

- 合并前私库/原 main：`bcb257cbb9c54056468a1823e0ede558cc879bf3`；原上游 v0.2.14：`0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d`。
- 固定上游 v0.2.16 commit：`01842576b3d5c1e2545b5a99ad555d36e28ed306`；tag 对象：`c3cb8588ce6a5aef4dc29fa4f71ac91f3c0bdf3d`。
- 私库 API 已确认 private，开始时无在途 PR，无 v0.2.16 私版分支/标签；从干净 origin/main 创建唯一工作分支 `codex/v0.2.16-pk.1`。不删除旧分支，不改写历史。
- 上游功能、安全修复、依赖及审计策略完整合入；用户新增 Kilo 桥接登记 PK-028。目标发行 v0.2.16-pk.1，由用户操作 Jenkins；源码验收及正式发布成功后，按当次授权部署独立实例。凭据仅在 Git 忽略的本机受限文件，不进入本记录。

## 冲突和交叉行为

1. 上游 backend-ci/release/security-scan Actions 保持停用；上游 unit/integration、frontend critical、shell/release helpers/security 检查由现有服务器九套件承接。Makefile 前端关键用例取双方并集。Go 1.27.2、golangci-lint 2.14.x 及对应镜像校验同步。
2. VERSION 在上游 tag 内仍为0.2.15，私版源码明确对齐0.2.16。发行 PK 后缀继续由 Jenkins 注入。
3. wire_gen 同时保留 Binance 关闭与上游 Gemini Cached Content 关闭；新 Gemini 恢复 worker 接启动门、取消和等待。普通客户端修改脱离客户端取消的语义保留；后台上游请求传播停机取消，避免全局清理先关闭数据库。
4. openai_gateway_usage 同时保留转换溢出时原始 token/零扣费证据与上游 GPT-6.1 Sol 订阅长上下文豁免。账号统计继续用 rawTokens，自定义区间按 OriginalContextTokens；新豁免只影响符合条件的实际凭据/最终模型，不扩大至 API Key。
5. 新结构化 input 拒绝后的 Chat 回退允许仅 comment/ping 已写出的请求；回退前同锁取消尚未发送的 space。已发送正文/space、取消、响应提交、错误不完整等原边界保留。
6. Kilo 在既有请求构造末端转换；跳过会破坏其 custom_tool ID、summary 和原始子任务的 OpenAI 清理。启用时普通/透传均按账号模型映射，并保持调度一致；不添加模型前缀，不另建代理/HTTP客户端，不转换内部原始 usage。
7. Kilo的custom_tool_call在同步Chat/Messages及Messages的input.done中保留工具输入；按用户最新要求，使用记录和客户计费均按原始 ultra/persistent，实际转发字段记录 max。客户计费副本覆盖普通 token、按次和响应模型/Free Fast重算，不改写原转发结果；余额/订阅/API Key额度统一使用该费用，账号自定义成本规则继续使用实际转发强度和 rawTokens。前后端支持独立 ultra/persistent 定价，未配置按1倍，不借用 max 倍率。
8. 迁移按完整文件名记录，新增上游242/243/244/245与同编号 PK 文件共存；旧币安/首字/安全策略迁移未改写，不按数字前缀覆盖。
9. Kilo 专属强度在选中账号后补充既有分组映射、上限/拒绝检查，保留原请求模型作用域且不重复普通等级映射。普通用户使用记录、定价说明不显示桥接到 max 的信息；Responses JSON/SSE 的强度回显保留原请求值，不更改生成内容和原 usage。桥接错误响应与用户错误历史清除暴露内部 max 映射的文案，管理员原始上游错误字段保留；原请求为 max 及普通参数错误不受影响。

## 全部有效二开逐项审查

下表为源码与调用链结论，动态结果以本 tree 对应服务器报告/最终 commit notes 为准。未得到九套件通过证据前不能提交、推送或发行。

| ID | 静态结论 | 核对依据 |
|---|---|---|
| PK-001 | 已保留 | admin_account 创建、更新、局部 extra、批量预检与创建/编辑页面保留 false/1、倍率0及缺字段保留；Kilo 独立 boolean，不覆盖缓存字段。 |
| PK-002 | 无影响 | cacheconversion decimal/Floor/int32、创建5m/1h明细归零保持；未改算法。 |
| PK-003 | 已保留 | RecordUsage 原输入拆分、转换一次、溢出证据零扣费保留；余额/订阅/Key/账号仍统一实扣。Gemini 新存储费及作用域预留在独立非OpenAI路径，不套入缓存转换，不以预留替代实扣。 |
| PK-004 | 已保留 | InputContextTokens、OriginalContextTokens 与 rawTokens 账号统计保留；新增 Haiku5.5/Gemini定价及订阅模型长上下文豁免同时吸收。 |
| PK-005 | 已保留 | JSON/SSE 输出转换副本与内部原usage分开；Kilo在请求侧转换，响应继续原流程。同步/流式回归检查5创建+7读取、倍率0.5得到9读取，内部创建仍5。 |
| PK-006 | 已保留 | 新平台 profile/protocol routing 接既有原生/兼容转发出口；Kilo优先 Responses、禁用WS及raw Chat回退；Chat/Messages最终使用同一请求构造器和既有usage计费。 |
| PK-007 | 无影响 | 三模式枚举、默认关闭/comment、时间范围校验、局部保存及运行缓存仍在 setting/DTO/前端链中，未改早响应设置。 |
| PK-008 | 已保留 | early writer/space实现未改，Chat及真实Responses生命周期和ID约束、Messages/结构化输出回退保留；Kilo复用该链。 |
| PK-009 | 已保留 | FirstResponseTiming复制、非空真实写出及null、全部SQL持久化保持；上游WS每turn分组快照在入队前解析，不改变原首字快照。 |
| PK-010 | 已保留 | 普通dto FirstTokenMs仍映射FirstResponseMs，管理员保留两个指标；公共channel monitor仍清空TTFT。新TPS取当前权限dto，不回填或暴露上游首字。 |
| PK-011 | 已保留 | 新Chat fallback补充扣心跳判断与PrepareEarlySSESpaceRetry；已发空格不重放，纯心跳可安全回退，回归覆盖三模式与发前/发后。 |
| PK-012 | 已保留 | 写锁/头隔离/停止/context与晚到SSE错误保留；新增回退经过原子space门控，未引入未锁定的正文重试入口。 |
| PK-013 | 已保留 | VERSION=0.2.16；PK出版展示、pk数值排序和更新菜单逻辑保留。 |
| PK-014 | 已保留 | 私库更新来源、token和GitHub资产客户端未切换；upstream只作合并源；正式仓库private已核实。 |
| PK-015 | 无影响 | 私库资产授权、跨域去凭据、SHA256验证、固定安装路径及回退备份核心未改。 |
| PK-016 | 无影响 | 私版Docker entrypoint、非root、持久化程序/镜像种子规则保留，未本地编译发行包。 |
| PK-017 | 已保留 | 保留九套件源码门禁+notes/Jenkins同包运行时验收；更新上游要求工具版本和前端测试并集，Actions保持删除。未新增审计豁免。 |
| PK-018 | 无影响 | concurrency provider跳过全局启动清理与持续过期槽位worker保留。 |
| PK-019 | 已保留 | 20项启动门；新Gemini worker未启动停止、立即/并发停止安全，退出等待并传播后台上游取消，wire清理包含所有服务。 |
| PK-020 | 已保留 | 维护文档的独立数据、备份/私源恢复、连接排空与后台交接保留；新机部署独立数据库，发行后记录实际验收。 |
| PK-021 | 已保留 | AGENTS/约束/唯一清单保留，新增PK-028与受影响旧条目、当前逐项报告同变更登记；版本分支/单PR规则不变。 |
| PK-022 | 已保留 | 币安凭据加密、五位尾数、倍率及旧迁移未改；上游普通支付更新不介入独立币安事务；wire清理保留。 |
| PK-023 | 已保留 | 每日消费复用全部usage、管理员路由和过滤器保留；公共UsageTable新TPS不改变消费金额来源。 |
| PK-024 | 无影响 | 币安实时账单只读API和管理员权限、日期/账户筛选未改；不进入余额结算。 |
| PK-025 | 无影响 | 专用香港探测与全失败关闭、认证网关和固定成功节点逻辑未改；Kilo只用账号代理，不复用币安专用代理。 |
| PK-026 | 已保留 | 排行用户脱敏、管理员确认、decimal扣款/逐条去重及余额流水保持；新平台目录不扩展访问权限。 |
| PK-027 | 已保留 | 默认关闭、阈值/累计、管理员豁免、手动解禁重计、鉴权outbox及数据库触发器保留。 |
| PK-028 | 新增已登记 | 仅OpenAI所有账号类型可保存；默认false；普通/透传模型映射与账号代理；ultra/persistent→max；子任务字符串不擦除。 |

## 验证记录位置及边界

完整暂存树由 `scripts/test-server.py` 上传至指定测试服务器的随机隔离DinD环境；工具镜像 `pk-source-tests:20261015`，Go1.27.2、Node24、pnpm9、golangci2.14。仅挂本轮源树，禁止生产卷/数据库/凭据。返回 schema2/pk-source-v2 报告、preflight及九套件原日志，自动删除测试环境。Windows仅调度、格式化及绑定证据，没有本地构建/测试。

最终通过的 tree/commit、真实时间、原报告路径与哈希由私库 `refs/notes/pk-source-validation` 及版本PR记录；本文不预填“通过”。如果检查失败，保留修改及真实报告，修复后完整重验相同待提交tree。扩展仓库现有回归，未添加永久临时测试文件；上游原回归保留。

Jenkins正式构建及制品十一项运行时验收是后续步骤，不能以源码测试替代。真实Kilo模型生成、实际下游SDK链路、币安真实付款未在源码回归中执行。未核验的边界不标记为线上实测。
