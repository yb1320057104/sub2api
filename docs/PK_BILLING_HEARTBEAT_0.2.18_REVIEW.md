# v0.2.18 首字与计费复核

## 用户要求与固定来源

2026-10-26 用户在合并后要求再次逐字节核对：保留缓存转换和 Kilo 开关，两者关闭时计费与上游一致；之后在服务器跑回归，确认首字设置开启不会改变计费，再提交版本。

- 上游：`ac1b8f1271be0d674fa6f074403c1410f30a3a81`，`v0.2.18`。
- 被复核私库：`7e0cbaca685a69212ec1959fbceb5788619b6c08`，`v0.2.18-pk.1`。
- 本轮唯一工作版本：`v0.2.18-pk.2`；旧标签不移动。无新增二开，沿用 PK-003、PK-007～012、PK-017、PK-028、PK-033。
- 对齐目标是模型调用自动计费：相同原始 usage、选中账号、模型、有效价格/倍率及定价时刻下的费用和额度处理。品牌、首包记录、管理员人工扣费、币安充值等独立功能继续保留，不宣称整个私版仓库与上游相同。

## 原始字节比较

比较直接读取两个固定提交的 Git blob，不使用工作区 CRLF，不忽略空白，不运行应用或测试代码。后端生产 `.go/.sql/.json` 文件共计1593个与上游同字节、110个修改、46个私版新增、0个删除。按 billing/pricing/quota/usage/subscription/reservation/cacheconversion/kilo 文件名筛选，219个同字节、7个修改、5个新增；该筛选只提供清点范围，额外逐项审查了输入、调度、协议、上下文和所有关联调用路径，不能把关键词统计冒充全部行为证明。

同字节核心文件包括 `usage_billing.go`、`usage_billing_repo.go`、`account_stats_pricing.go`、`billing_token_cost_request.go`、`billing_context_schedule.go`、`billing_inflight_reservation.go`、`usage_record_worker_pool.go`、模型价格 JSON，以及本次新增的原生 Anthropic 兼容断流实现。

三路独立函数核对：

- `billing_service.go` 的57个既有函数同字节，包括标准强度倍率、token费用、缓存拆分及媒体价格。4个不同函数展开“两开关关闭”的分支后，运算与调用文本与上游一致。
- `openai_gateway_usage.go` 的25个函数同字节；`RecordUsage` 关闭分支排除独立首包记录字段后，token、价格输入、求值顺序和结算路径与上游一致。11个 `CostInput`、2个 `TokenCostRequest` 构造点已逐项检查。
- 14个 usage/别名/显式0/include_usage 函数同字节，覆盖 Responses、Chat、Messages、embeddings、图片；8个共享强度/政策函数同字节，9个输入/预留/调度/断流核心文件同字节。

原始清单、SHA256、函数位置及差异依据保存在本机 Git 忽略的 `.codex/audits/20261026-v0.2.18-disabled-billing-review/`；固定输入均为上述两个完整 commit。它们是静态审计证据，不是动态通过报告。核心函数证据 SHA256 为 `82a795149fbfd42aa04744c4e4980709a7a64484059d178a9f41e34e36816b85`；输入/上下文为 `51e66b375a6c334e71bc9e5a1d86ef6639a5160e645c82dd1652663ee58acdea`；协议usage为 `c0f2ff6b58dabe1cbcd69b14545f9200e83bfb4c7c5e6c7535a6db73b944c0e1`。

## 差异的关闭路径

- 缓存转换关闭直接返回原始 token，不设置 `OriginalContextTokens`，上下文档位仍按原输入三桶求和；输出转换返回原字节，SSE包装器委托上游扫描器。
- Kilo关闭不改请求、模型映射、客户计费强度或响应。共享强度函数已在pk.1恢复为上游原样；只有Kilo开启的客户定价子context可以使用ultra/persistent扩展倍率，不传入账号成本。
- 原始请求强度元数据仍允许保存ultra/persistent，配置接口仍允许保存对应倍率，但关闭Kilo不使用这些扩展倍率；元数据不进入余额预留计算。
- 用量入库的既有金额/token参数顺序保持，新增 `first_response_ms` 追加在末尾。用户首字显示与管理员上游首字权限不是计费金额。
- SQL字节核对确认旧62个插入参数及类型前缀不变，第63项仅为首包时间；`prepareUsageLogInsert` 去掉这一个新增参数后与上游函数字节相同，SHA256均为 `9239fcafb7dd50c60eb0e4508c0a5cf37e41d6295d96080ade5a02cd1a66b175`。SELECT列同样仅末尾追加，未挪动费用/token列位置。
- 入队前保存渠道字段修复Gin上下文复用风险，保留当前请求的真实模型，不复制上游可能发生的跨请求竞态错误。
- OpenAI旧投影版本检查可能触发既有回源或等待重建；这影响缓存可用性，不新增价卡、token或额度倍率。

## 首字与异常边界

comment/ping只写下游保活和首包计时，不写账务用量；space额外输出空格及协议生命周期，不携带虚假usage，真实终态usage保持。本站不按输出空格估算收费。

首字功能独立于上述两个账号开关：comment/ping按既有规则不视为业务正文，space已经写出（包括部分写出）后不能重试，否则会重复输出。因此不保证所有首字模式的网络结果、最终账号或上游实际生成量完全相同；确认的是同一真实usage的计费规则不变、没有首字额外扣费或重复记账。

上游自身部分OpenAI Responses/透传错误包装会返回 `nil,error`，reader观察到partial usage不等于handler一定提交账单；本轮保持其上游边界，不为“断流全部收费”擅自增加收款。原生Anthropic兼容流及其他入口按各自上游结果规则核对。

## 服务器回归范围与证据规则

只使用 `scripts/test-server.py` 的指定服务器隔离环境。源码通过 HEAD bundle 与暂存对象传输，验证前后tree必须相同；测试容器不挂生产数据、宿主Docker socket、环境凭据或服务卷。本机仅执行控制脚本和静态字节比较，不执行应用测试、类型检查、lint或构建。

- 在既有 `openai_gateway_usage_integrity_test.go` 扩展四种首字状态的真实响应reader→RecordUsage对账：同一原始usage、固定定价时间、费用/各类额度及余额/订阅命令；reader层断流证据和handler最终记账分别表述。
- 首字矩阵同时核对默认账号成本与账号倍率，不把账号额度断言冒充自定义成本规则验证。账号自定义成本/按客户价格统计路径另由既有相关回归及本轮静态调用链核对覆盖，未声称穷尽所有首字与定价配置组合。
- 在既有 `openai_gateway_first_output_timeout_test.go` 扩展真实handler的换号/已提交空间占位边界及最终账务提交次数，保留既有首字帧、生命周期、取消、短写、错误、计时和隐私回归。
- race选择器保留所有既有范围，将 `PKKiloBridge` 扩为 `PKKilo`，增加 `PKDisabledExtensions`；对普通关闭计费及Kilo RecordUsage专项要求实际匹配，不能用零用例成功冒充通过。
- 分组定价的既有数据库往返测试保留 `ultra/persistent` 配置值及全部常规强度断言；普通Anthropic计费没有Kilo专用context，未知强度应按上游基础倍率1结算，不能因数据库保存了扩展键就全局启用。开启Kilo的原始强度另由既有48个RecordUsage用例覆盖。
- 执行独立preflight和完整九套件：backend-unit、backend-integration、backend-race、backend-lint、frontend、shell、binance-proxy、release-helpers、security。integration包含真实隔离PostgreSQL，不能把service stub对账描述成数据库已验证。

服务器首轮因SSH连接中断未完成，重新连接后保留部分日志并清理本轮所属容器、卷和目录。第二轮完成全部九套件：unit、race、frontend、shell、binance-proxy、release-helpers通过；integration暴露上述旧强度预期，lint提示测试变量类型可推断，security发现联系方式WebP解码路径实际调用有漏洞的 `golang.org/x/image v0.41.0`。失败报告保留，不作为最终tree的通过证据。测试夹具的配置存储补全接口和锁，Messages输出用量改放上游读取的 `message_delta`，保留原用量/金额断言。

PK-033的图片依赖最小升级至 `v0.45.0`，修复官方列出的 [GO-2026-6222](https://pkg.go.dev/vuln/GO-2026-6222)、[GO-2026-5061](https://pkg.go.dev/vuln/GO-2026-5061)、[GO-2026-4961](https://pkg.go.dev/vuln/GO-2026-4961)。版本元数据来自Go官方模块代理，checksum来自 `sum.golang.org`，仍由服务器Go模块校验与安全扫描验证；既有Go 1.27.3、x/sys v0.48.0及x/text v0.42.0已高于此模块最低要求，无需提升其他依赖。保留PNG/JPEG/WebP支持、2MiB和4096px限制、完整解码及管理员设置权限，不添加安全豁免，不修改计费生产代码。既有 `setting_service_update_test.go` 补充官方固定tag的完整WebP样本接受，以及仅头部、像素截断和错误签名拒绝；它验证格式兼容与损坏输入保护，不冒充三项CVE利用样本复现。

本文件不预先宣称通过。实际结果只以本轮完整原报告、套件日志、SHA256、执行环境及对应tree/commit为准。通过后提交被测tree，按需要绑定可选 `refs/notes/pk-source-validation`；失败或tree变化须修复并重新验证，不移动pk.1标签或挪用旧报告。新增临时测试文件执行后删除，仓库原有回归文件的扩展继续保留。

正式交付仍由用户自行启动 Jenkins：`ACTION=release`、`SOURCE_REF=v0.2.18-pk.2`，`COMMIT_SHA`、`PACKAGE_ID`留空。源码回归不代替同次制品十一项验收，不自动发布或升级生产。
