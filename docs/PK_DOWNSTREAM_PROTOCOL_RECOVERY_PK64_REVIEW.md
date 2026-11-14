# pk.64 下游工具协议与加密会话恢复修复

日期：2026-11-14。源码基线 `4704f54680c4d4b47c870725c2de8eced81d2891`，目标唯一版本分支 `v0.2.19-pk.64`。固定源码仓库 `yb1320057104/sub2api`，按用户既有选择保持公开；旧标签、正式资产与 GHCR 可见性不变。

## 生产证据

用户确认报错发生在接入本站的其他下游。此次仅只读查询托管生产数据库、API 日志及实际 API 网络命名空间下的 Redis；未发送真实模型请求、修改账号或生产配置。私有连接信息与原始证据只保存在工作区外 Git 忽略的运维目录。

- 实际 API 版本仍为 `0.2.19-pk.63-ha.20261113.1794589548`，程序 SHA256 `e345ba9217b82e44096413bee105f14f019ddf1031b231402b6a226de23e4f6a`。
- 工具协议错误有本地 guard 和真实 `upstream_http` 两类来源；支持 Responses 的账号也收到过上游原样返回的拒绝，不能仅修改本站能力探测标记，更不能用普通文本成功证明工具调用可用。运维日志记录可能包含同请求的多个阶段，记录数不当作独立用户请求数。
- 24小时采样有948条409记录，包含共享兼容存储不可用、owner不可用及冲突等不同类别。后续近2小时采样以共享存储不可用为主；日志出现 `agent_recovery_publish_failed` / `commit_failed` 与 `context deadline exceeded`。
- 在两个 API 的实际网络命名空间内，通过三 Sentinel 一致发现、mTLS 验证及 ROLE 验证 writer：靠近 Redis 的 API 单次 GET 约0.06～0.31ms，另一 API 约32～150ms；30个样本为27个合法记录、3个冲突，未见格式错误，rejected_connections/evicted_keys均0。这是有界采样，不是全库完整性证明。
- 同一批历史在原代码中逐项 GET/EVAL，但整批期限固定3秒；入站、发送前、发布/保留及输出交付均有串行路径，长历史跨节点延迟会耗尽期限。额外只读计时前两次停在发现阶段TLS超时；第三次成功核对三Sentinel一致和writer角色后，同一连接/同一30键逐项GET合计2510.60ms，三个pipeline采样为53.25/70.29/79.94ms。对比仅验证真实链路的批量读取收益，新应用尚未上线；保留失败证据，不宣称网络故障均已解决。

## 修改与边界

1. 选号上下文只保存是否带工具、入站协议、显式关闭推理等路由事实，不保存工具正文。使用渠道 forward model 和账号映射推导实际模型；高级和旧调度、权威候选复查共用准入。已知纯 Chat 账号不承接现有校验要求 Responses 的工具请求；普通文本仍允许 Chat。专属 Kilo/Copilot/BPS/Prism、托管连接和需要动态目录的供应商保持已有路径，不宣称本次重写所有适配器。
2. 上游完整 JSON 的明确工具协议拒绝按账号路线不兼容处理，允许现有有限重试预算内换号，不在原账号反复重试，不改全账号 Responses 标记。匹配完整错误和允许的类型/码，避免把一般 schema、加密、鉴权或回显字段误判。无路可选时以503说明服务端无兼容通道；已有错误透传规则保留。真实通道均不支持工具时，仍需管理员修复供应商能力，不能制造支持。
3. 加密会话批量接口兼容原单键适配器。每批128个独立 GET/EVAL，所有键先验证，逐命令检查错误；nil不掩盖后续WRONGTYPE。不是MGET或跨键事务，每个键的 Lua CAS 和 Cluster slot 独立。写入结果不明仍跟踪所有可能写入项，finish只比较本次token，已完成兼容记录不能被失败producer撤销。
4. 批量等待仅重查未完成pending项，已见pending后消失/失败仍拒绝；冲突、过期、路由/凭据变化仍拒绝。历史查询键去重，但原请求历史不重写。存储错误为503/server_error及WS1013，真实冲突继续409；不清兼容记录，不绕过原owner，不自动重放已执行工具结果。

## 二开保留

| 范围 | 依据 |
|---|---|
| PK-006 | 三入口JSON/SSE、WS恢复、原生/Chat转换及加密归属路径定向测试；新增规则不删除原forward guard |
| PK-042、045、058 | 请求策略后的实际模型准入；只在可用候选中保留优先级和原选号，权限/模型/分组/最新账号复查不旁路 |
| PK-047 | Redis键格式/TTL/CAS兼容，未变更HA角色、writer、后台职责或运行配置 |
| PK-001～005、007～012、028、044、046 | 无费用/usage/首字/桥接/代理/结算实现修改；已有账务与正文提交相关定向断言保留 |
| PK-021及其他有效条目 | 本轮为已有能力修复，无新增ID、前端、迁移或依赖；其他条目无代码变更，不把静态无影响当作全功能动态验证 |

## 验证

Windows、本机 Go 1.27.3 工具链，Go 临时目录和本任务构建缓存移至有空间的盘。只跑定向源码回归，不跑全套源码测试、不连接生产数据用于测试。

最终命令：

```text
go test -p 2 -json -tags unit ./internal/service ./internal/repository ./internal/handler -run 'Test(PK64|Issue715|Issue758|ResolveUpstreamProtocol|OpenAIGatewayService_SelectAccountWithScheduler|GPT61SolOnlyChatFallback|ForwardAsChatCompletions_ResponsesSupported|PKCacheConversion|IsOpenAICompatibleModelNotFound|PoolCapacity|OpenAIManaged|OpenAIResponses.*Capability|OpenAIGatewayHandler.*Image)' -count=1
```

| 包 | 顶层通过 | 含子项pass事件 | 失败 | 跳过 |
|---|---:|---:|---:|---:|
| service | 118 | 269 | 0 | 0 |
| repository | 12 | 17 | 0 | 0 |
| handler | 51 | 224 | 0 | 0 |
| 合计 | 181 | 510 | 0 | 0 |

新增与既有测试覆盖：300键/每次往返30ms模拟中只需3次pipeline、旧逐项策略在短期限内失败；600项历史读/发送前/发布/TTL走批量；pending完成/消失/失败/冲突/短批次/取消；外来token与已完成记录保护；三协议×流/非流×已知不兼容/上游拒绝/全拒绝的真实handler链路；已提交工具结果遇到协议拒绝仍不自动重放。所有上游调用为本地模拟，未产生模型费用。

失败记录保留：首次构建临时盘不足，之后两个新测试调用签名不匹配，均未作为通过；接口回归初版夹具遗漏Messages分组开关并错误假定原生透传会重写模型，修正夹具后再验证。额外独立子代理审阅因供应商403无法启动，本轮未获得独立审阅通过，主任务完成静态复核。`git diff --check`通过。

## 交付状态

此文记录源码修复与本轮定向验收，不表示正式Release、HA制品、同制品运行时门禁或生产部署已完成。生产仍使用上述pk.63程序；正式发行与逐节点更新需在本次明确授权后执行，沿用实际同制品验收、SHA256及单writer/worker和正常排空退出规则。历史强杀授权不适用于本次。
