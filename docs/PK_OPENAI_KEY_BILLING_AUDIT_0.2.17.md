# OpenAI API Key 账号计费审计（v0.2.17）

审计日期：2026-10-19（Asia/Shanghai）。

**结论：“官方”如果指原版 Sub2API，真实 OpenAI 支持的常规请求在缓存转换和 Kilo 桥接两个开关都关闭时，沿用上游 Sub2API 的原生计费流程；如果指 OpenAI 官方账单，不能保证在所有情况下完全一致。** 仅关闭缓存转换、仍开启 Kilo 的账号继续受 PK-028 的桥接与请求强度计费规则影响，不能归入“完全原版”条件。第三方兼容接口接受特殊强度的边界见下文。

对于直连 OpenAI 官方、模型和价卡正确、相关倍率为 1、没有额外定价策略的普通文本请求，输入/缓存读取/缓存写入/输出拆分与官方口径一致。本站自行根据 usage 和价卡计算费用，并非读取或照抄 OpenAI 的实际账单。本次也发现上游既有的 Ultrafast 和工具附加费覆盖缺口，见下文。

本报告只审计源码和公开官方文档，没有读取线上账号、分组、渠道或价格覆盖文件，没有访问生产数据库，也没有发送付费生成请求或核对真实 OpenAI 账单。本文不是完整源码测试通过证明；本次服务器隔离验证结果以主合并记录及绑定最终提交的报告为准。

## 审计基线与范围

- 合并前私库：`82689422e5bb1444769265534265f570fed274a2`，源码已按用户要求恢复至 `v0.2.16-pk.1`，清单保留回退记录。
- 固定上游：`v0.2.17`，commit `060d0160417782cd962a0c7f460f6cd4565d4916`。
- 审计对象：本次合并工作树中的 Responses、Chat Completions、Messages 兼容入口、统一 usage 结算、价格解析和账号统计；同时比较上游 `v0.2.16` 至 `v0.2.17` 的相关差异。
- 涉及保护项：PK-001～006、PK-028；PK-029、PK-030 已移除，未恢复隐藏倍率或下游 token 缩放。无新增二开。本次报告不修改计费代码或运营配置。
- 下文行号对应本次合并后的源码快照，用于定位；后续源码变化时应同时按所列函数名核对。

## 关闭缓存转换后的实际公式

`openai_gateway_usage.go:180` 的 `RecordUsage` 先拆分互不重叠的 token 桶：

```text
普通输入 = max(上游 input_tokens - cached_tokens - cache_write_tokens, 0)
缓存读取 = 上游 cached_tokens
缓存写入 = 上游 cache_write_tokens
输出     = 上游 output_tokens（包括上游已合并计算的 reasoning tokens）

基础 token 费用 = 普通输入 × 输入单价
               + 缓存读取 × 缓存读取单价
               + 缓存写入 × 缓存写入单价
               + 输出 × 输出单价
```

图片输入/输出另有互斥拆分和单价，不应把以上纯文本简式直接套用于所有多模态请求。长上下文、服务档位、自定义区间、渠道分时及推理强度规则会先调整相应价格或基础费用；用户/分组费率及高峰因子再参与最终客户金额计算。

`openai_cache_conversion.go:94` 的 `convertOpenAICacheCreationToRead` 在开关关闭时直接返回传入的原始 token，不执行缓存写入转读取。配置中的转换倍率即使留有旧值，关闭时也不参与计费。普通账号的 Kilo 开关关闭时，`openai_kilo_bridge.go:159` 的 `openAICustomerBillingResult` 直接返回原结果，不替换计费强度。两项开关互相独立。

官方缓存文档当前明确区分缓存写入和缓存读取，GPT-5.6 及之后模型具有独立缓存写入费；不能把“关闭本站缓存转换”理解为关闭 OpenAI 自身的缓存或取消缓存写入费用。[OpenAI Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)

例如，在 GPT-6.1 Sol 的 Standard 短上下文价卡下，总输入 10,000、缓存读取 6,000、缓存写入 2,000、输出 1,000，普通输入为 2,000。若无其他价格或倍率规则，费用为 `0.004 + 0.0006 + 0.005 + 0.01 = $0.0196`。该模型的本地回退价卡见 `pricing_service.go:77`、`resources/model-pricing/model_prices_and_context_window.json:2`，与审计时官方列出的 Standard 四类单价一致。[GPT-6.1 Sol 官方价格](https://developers.openai.com/api/docs/models/gpt-6.1-sol)

使用记录中的 `InputTokens` 保存的是拆分后的普通输入，官方响应里的 `input_tokens` 通常是包含缓存的总输入。因此记录里普通输入小于官方总输入本身不是丢 token；应比较 `普通输入 + 缓存读取 + 缓存写入`。

## 价格、倍率与模型选择仍由 Sub2API 决定

| 项目 | 源码事实及影响 | 定位 |
|---|---|---|
| 价格来源 | 默认从 `Wei-Shaw/model-price-repo` 拉取目录；本地 fallback 补充缺失模型，override 可覆盖目录字段。这不是实时调用 OpenAI 官方账单 API。 | `backend/internal/config/config.go:2383`；`pricing_service.go` 的 `buildPricingData`、`applyPricingOverrides` |
| 分组和渠道定价 | 命中的分组模型价优先，渠道可覆盖 token 单价、区间及按次模式；这些设置不受缓存转换开关控制。 | `model_pricing_resolver.go:72`；`billing_service.go:1555` |
| 客户倍率 | 系统默认费率为 1；实际请求可能使用用户在该分组的专属费率或分组费率，并叠加分组高峰因子。渠道分时和推理强度还可独立改变金额。 | `openai_gateway_usage.go:201`；`group.go:394`；`billing_service.go:1656`、`:1772` |
| 计费模型 | 请求名、渠道映射名、最终上游模型及启用后的响应模型策略均可能决定使用哪张价卡；模型别名不同并不自动保证价格相同。 | `openai_gateway_usage.go:219`、`:281` |
| 服务档位 | Fast/priority、Flex、Ultrafast 有独立处理；API Key 上游报告较低档位时可降价，报告较高档位不自动提高账单。分组“免费 Fast”还会使客户按 Standard 实扣。 | `service_tier_billing.go:25`、`:68`；`openai_gateway_usage.go:136`、`:312` |
| 长上下文 | 客户定价由分组开关和账号额外开启能力决定，原始上下文决定档位；API Key 不继承 OAuth 的 GPT-6.1 Sol 长上下文豁免。分组默认启用，但运营者可调整。 | `billing_service.go:1607`、`:2025`；`openai_gateway_usage.go:577`、`:588`；迁移 `221_group_model_pricing.sql` |
| 缺失定价 | 定价服务存在模型族回退；仍无法定价时，记录零费用并告警。因此“请求成功”不能证明已经按正确官方价格收取。 | `pricing_service.go` 的 OpenAI fallback 分支；`openai_gateway_usage.go:266` |
| 舍入 | 本站余额及额度金额统一量化到 8 位小数，不能据此承诺与官方所有汇总和舍入细节逐分完全一致。 | `usage_billing.go:60`、`:97` |

“API Key 类型”也不证明 Base URL 指向 OpenAI 官方；该类型同样能连接兼容服务。即使 usage 字段符合 OpenAI 协议，第三方提供商的实际收费也可能不同。

## 余额、订阅、API Key 额度与账号成本

统一结算使用 `RecordUsage` 算出的费用，不会因下游序列化再次计算缓存转换：

| 去向 | 使用口径 | 定位 |
|---|---|---|
| 用户余额 | `ActualCost` | `gateway_usage_billing.go:329` |
| 本站订阅额度 | `ActualCost`，仍包含客户费率；这是本站订阅，不是 OpenAI ChatGPT 订阅 | 同上 |
| 本站 API Key 总额度与限速金额 | `ActualCost` | `gateway_usage_billing.go:341` |
| 上游账号额度 | `TotalCost × AccountRateMultiplier`，与客户费率独立 | `gateway_usage_billing.go:347` |
| 账号成本统计 | 优先账号自定义规则；其次可按渠道开关用客户倍率前 `TotalCost`；否则用原始 token 和上游模型价格；无专用结果时用默认统计公式 | `account_stats_pricing.go:25`；`openai_gateway_usage.go:497` |

**账号统计的长上下文设置尤其需要区分。** 账号开关 `openai_long_context_billing_enabled` 缺失时为 false，新建账号表单也默认 false（`account.go:1390`、`CreateAccountModal.vue:4766`）。账号统计的模型价卡回退按该账号开关判断长上下文，客户收费则可以由默认启用的分组开关收取长上下文费用。于是存在“客户按长上下文计费、账号统计仍按短上下文估算”的正常配置组合；账号成本统计不是官方支出账单。相关入口为 `account_stats_pricing.go:80`、`:302`。

在途余额预留按请求体和输出上限估算，用来约束并发，不直接替代最终实扣。预留额不保证等于真实 token、缓存命中与服务档位计算后的账单（`billing_inflight_reservation.go:354`、`:508`、`:542`）。

## Responses、Chat、Messages 的 usage 行为

- Responses/Chat 共用 `openai_gateway_response_handling.go:1682` 的 `openAIUsageFromGJSON`：识别 `input_tokens`/`prompt_tokens`、`output_tokens`/`completion_tokens`，从明细读取缓存读取和写入数，显式嵌套 0 不被旧别名覆盖。OpenAI 已将推理 token 合入输出时，不再次相加。
- 原始 Chat JSON/SSE 先提取真实 usage，再写客户端副本；流式使用最后收到的 usage，客户端断开后继续尝试读取上游计费信息。定位：`openai_gateway_chat_completions_raw.go:378`、`:519`、`:559`。
- Messages 经原兼容适配器转为 Responses/Chat，返回 `OpenAIForwardResult.Usage` 交统一结算。Anthropic 风格 `input_tokens` 与 OpenAI 总输入字段语义不同，不能要求跨协议输出字段逐字相同。定位：`openai_gateway_messages.go:1422`，以及 `openai_gateway_messages_chat_fallback.go`。
- 缓存转换关闭时，PK JSON/SSE 改写器直接采用原输出路径：`openai_cache_conversion_output.go:20`、`:40`；底层 `cacheconversion/usage.go:79` 直接返回原 body。协议适配、模型回显和上游原有兼容处理仍照常运行，这不等于整个响应逐字透传。
- 上游没有返回 usage、流式连接未能收到最终 usage、未知提供商字段等情况，不能从本次静态审计推导出完整账单。这些情况没有通过真实付费请求验证。

## 已确认的官方对齐缺口与上游边界

### 1. GPT-6.1 Sol Ultrafast 仍落到 2 倍，而当前官方为 6 倍

当前官方模型页允许 GPT-6.1 Sol 在 Responses 使用 `service_tier: "ultrafast"`，并明确其价格为 Standard 的 6 倍。[GPT-6.1 Sol](https://developers.openai.com/api/docs/models/gpt-6.1-sol)、[Ultrafast mode](https://developers.openai.com/api/docs/guides/ultrafast-mode)

本次合入的上游 `v0.2.17` 中，`billing_service.go:145` 的默认 Ultrafast 倍率为 2；`:156` 只在 `UltrafastMultiplier > 0` 时覆盖；`:1957` 仅对 GPT-6 Astra 设置 6。GPT-6.1 Sol 没有对应赋值，而合法 `ultrafast` 会被请求解析放行（`openai_gateway_request_body.go:1837`）。因此在无其他定价策略、官方实际执行 Sol Ultrafast 的条件下，本站按 2 倍而官方按 6 倍，存在明确少计风险。

这是固定上游版本已存在的实现缺口，缓存转换关闭不会修复它；本次只记录，不静默改变正式费率，也不把既有测试通过当作官方价已完全覆盖。

### 2. 普通 OpenAI Responses 的内置工具附加费未完整接入

官方有模型 token 费之外的 web search、file search 和容器费用。[OpenAI Pricing — Tools](https://developers.openai.com/api/docs/pricing#tools)

`openai_gateway_forward.go:1359` 仅在 `account.IsGrok()` 时把搜索调用数写入 `SearchCount`；Chat/Messages 的对应分支也限定 Grok。另一个 `WebSearchCalls` 计费分支由 Codex `alpha/search` 独立接口产生（`openai_alpha_search.go:136`、`:222`），不能视作普通 OpenAI API Responses 的 `web_search_call` 计费。

因此普通 OpenAI API Key 通过 Responses 使用内置 web search 时，本次检查的路径只可确认模型 token 结算，不能确认官方按次搜索费被完整加收。在本次统一 usage/billing 路径也未发现 file search 存储/调用和容器会话费用的完整结算。此处是源码覆盖范围结论，不声称已经做过官方账单对账。

### 3. v0.2.17 的 `/messages` 搜索模拟走第三方搜索

新增入口位于 `openai_gateway_messages.go:72`。只有搜索管理器已装配、全局开启、请求仅有一个 web-search 工具且账号或渠道启用时才命中；OpenAI OAuth/setup-token 明确排除，API Key 可以命中（`gateway_websearch_emulation.go:60`）。关闭缓存转换不会关闭搜索模拟。

命中后执行第三方搜索，不访问 OpenAI 模型。返回给统一结算的 `OpenAIForwardResult` 只有模型、stream 与耗时，没有模型 token 或搜索计数（`openai_gateway_messages.go:80`）。默认 token 模式内部按 0 token 计算；若配置按次渠道，仍可能按次收取。客户端 usage 的输出数却是摘要字符串字节长度除以 4 的估计值（`gateway_websearch_emulation.go:246`、`:371`），不是官方 token，也不是内部实扣依据。该行为按上游完整吸收，不能描述为官方 usage 或官方搜索账单一致。

### 4. 区域附加费、特殊价格与未知模型不能自动保证对齐

官方模型文档注明区域处理可能有额外费率；本次核对的文本计费路径没有根据实际区域 Base URL 自动增加附加费的分支，仍由价卡和运营配置决定。未知模型还有模型族/默认模型回退，远端价格目录与官方变更也未必同时更新。这些均是“本站价格计算”与“官方实付账单”之间需要逐项核对的边界。[OpenAI Pricing](https://developers.openai.com/api/docs/pricing)

### 5. 第三方兼容接口的 ultra/persistent 倍率并非完全受 Kilo 开关隔离

`billing_service.go:252` 的 `reasoningEffortBillingMultiplier` 是共享函数，PK 为 Kilo 增加的 `ultra/persistent` 倍率键不以账号开关作为查询条件；`channel_service.go:702` 也允许保存这两个键。第三方兼容 API Key 的 Chat 请求走 Responses 上游时，`chatcompletions_to_responses.go:77` 原样复制这两种强度，`openai_gateway_chat_completions.go:473` 再将其放入计费结果；未知强度不会被普通分组策略替换。

因此，若第三方上游接受该强度且请求成功、运营者又配置对应的同名倍率，即使缓存转换和 Kilo 都关闭，仍可能使用 PK 扩展倍率；原版 Sub2API 对这类未知强度按 1 倍。真实 OpenAI 常规合法强度，以及未配置同名倍率的请求，不受这个特殊边界影响。本项为既有共享代码的静态可达性结论，未发送第三方付费请求；本次记录审计发现，不改变已保存规则或追溯重算。

## 本次可以交付的判断

真实 OpenAI 常规请求、缓存转换关闭、Kilo 关闭时，没有发现 PK 隐藏倍率或缓存二次换算进入正常文本收费；与固定上游相比，价格解析、服务档位结算和账号统计模块的相关运营规则保持上游行为。缓存转换开启路径、原始上下文选档及 Kilo 特例仍独立保留；不能将这一结论扩大到上节的第三方特殊强度配置。

如目标是让普通 Standard 文本请求按官方标价计费，应核对实际官方 Base URL、最终计费模型、有效价卡、用户/分组费率、分时/高峰/强度倍率及长上下文策略。**仅添加 API Key 并关闭缓存转换，不足以证明线上费用已经与官方完全相同；本次发现的 Ultrafast、工具费和搜索模拟边界也需要单独处理。** 本次不扩大为生产配置修改、追溯重算或新增计费修复。
