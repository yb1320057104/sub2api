# v0.2.19-pk.25：账号请求统计与关闭首字提前响应

- 日期：2026-10-31。基线：私库 main / v0.2.19-pk.24，e080d558963242ca4bbc619c6afdcede6be1cf0b。
- 范围：新增 PK-037，扩展 PK-007～010/021；保留 pk.24 及之前全部二开。
- 用户确认：关闭新增首字设置时，完整沿用合入原仓库的判定结果，保留原 semantic / visible 配置。

## 原因与固定上游比对

1. 旧实现即使关闭提前响应，也安装 earlySSEHeartbeatWriter 并记录第一次非空 body 写出；上游前导事件和原有周期保活均可能早于原上游 TTFT。因此两个数不相等不等于开关实际开启，也无法从旧记录判断那次首包具体是前导事件还是保活。
2. 本轮对照实际合入的上游 v0.2.19（dece26153621959ccd7635814f4b2d08304e681a）。原 openai_gateway_usage.go 与 gateway_usage_billing.go 均直接保存 result.FirstTokenMs。
3. 原 openAIStreamDataStartsTTFT 函数与当前工作区逐段文本完全一致，SHA256：29e7326edc3991d2ae393583e82550681479cadb70fdbd7638bd7f78e724a656。本轮未修改该函数、TTFT 模式读取或流解析时机。
4. 关闭时，StartEarlySSEHeartbeat / startEarlySSEHeartbeat 直接返回，不安装 writer、不创建原子首包状态或提前调度；原生 startOpenAISSEKeepalive 路径继续使用。
5. 新用量记录的展示首字：关闭或不适用提前响应时原样复制 result.FirstTokenMs（保留 null 与实际0）；开启时记录真实第一次非空下游写出。原 first_token_ms 不变，first_response_ms 字段继续承载兼容展示值。开启请求的快照随 detached usage context 传递，不重新读取全局设置。
6. 历史记录不回写，不用当前开关猜测旧请求。开启后的注释/ping/空格三模式与原计费/配额/重试逻辑保持。

## 账号统计口径与接口

- 最新完成首字：按 usage_logs 的完成入库时间、ID倒序取最新成功请求，读取该条 first_response_ms。没有记录或该条未记录首字时显示 —，不会取平均或借用更早的值。
- 连续失败：统计最近成功之后该账号的上游终态错误，provider 归因、upstream/network/account_auth 阶段、HTTP>=400且非499，排除 business_limited / count_tokens。
- 余额不足、平台限额、路由错误和客户端错误不计数且不清零；已恢复2xx过程记录不计数。按账号、API Key与请求ID去重，空ID回退客户端ID或错误行ID。
- 失败占位与安全策略记录不作为成功。部分计费失败按原始 / local: / client: 请求ID关联 ops_error_logs，不能因为有用量而清零；免费但有真实用量/首字的成功可清零。
- 数据来源限于系统实际保留的日志；错误日志关闭、跳过记录或历史清理会影响可恢复的统计，不新增影子计数器，不改既有日志/表结构。
- 复用管理员 accounts/today-stats/batch 的显式 include_request_stats，独立返回 request_stats；账号页每批一次SQL，使用现有索引，30秒缓存且与旧请求分键。查询失败显示不可用，不伪造零、不泄露底层错误。
- 两列默认显示，可隐藏；支持分页/筛选、手动与自动刷新。账号列表304也继续更新统计，使用请求序号阻止旧响应覆盖新页。

## 静态核对与未执行项

- Go源文件使用 gofmt 整理，git diff --check 已通过；新旧原上游TTFT函数文本比对一致。
- 人工核对SQL字段、已有索引、local:/client:关联、按账号隔离、nullable/zero、重复错误、免费成功、当前页批量、API类型、可见列和错误显示链。
- 既有服务/handler回归同步关闭时原生writer与计时、开启时请求快照、nil/zero、保活、账务不变断言；既有repo integration文件补充跨日、去重、平台错误排除、部分计费失败和免费成功案例。没有新增测试文件。
- 未运行本地或服务器构建、测试、数据库执行计划、浏览器视觉验收、真实模型请求、Jenkins或部署。静态检查不代表运行时验证通过。
- 未修改授权/凭据/测智分组、调度、认证权限、模型请求体、原用量计算或历史账单。

## 交接

- 版本分支/唯一新正式标签：v0.2.19-pk.25。提交后合入私库main，核对远端main与标签剥离commit及祖先关系；旧标签不移动。
- 用户自行Jenkins：ACTION=release，SOURCE_REF=v0.2.19-pk.25；COMMIT_SHA、PACKAGE_ID留空。新构建的制品验收继续使用原门禁，不自动部署。
