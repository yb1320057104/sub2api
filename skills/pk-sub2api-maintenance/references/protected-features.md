# 二开功能保留清单

首次原样保存提交为 `6655dd8ca`，父提交为上游 `a60a29549f488a854966aaec9541abbe006cac22`。清单说明业务语义；每次以私库当前实际代码为准，不能还原为旧快照抹掉后续功能。

## 账号设置

- 只对 OpenAI 账号启用缓存转换；新增和编辑界面有独立开关及倍率。
- `accounts.extra.cache_conversion_enabled`、`cache_conversion_multiplier` 存储设置；默认关闭、倍率 1；倍率 0 与有限非负小数有效。
- 普通更新没有提供这些键时保留已有设置；创建、更新、局部 extra、批量 API 均有验证。不能丢掉导入、重新授权等账号保存路径的审查。
- 初次审计发现 Codex Session 重导会显式发默认值覆盖已有设置。这是既有边界；合并时记录，只有当前任务授权修复时才修改，不要默默改动。

## 算术、计费和统计

```
新缓存读取 = 原缓存读取 + floor(原缓存创建 × 转换倍率)
新缓存创建 = 0
```

- 原有读取、普通输入、输出不受倍率直接影响，创建的 5m/1h 明细清零。
- 先用原始上游 usage 拆出普通输入，再转换缓存，避免被转换掉的量又按普通输入收费。
- decimal 算法和 int32 溢出检查要保留，不能退回不精确的浮点取整。
- 记录、余额、订阅及 API Key 额度使用同一转换结果；账号/分组倍率仍为独立机制。
- `UsageTokens.OriginalContextTokens` 与 `InputContextTokens()` 保留原始上下文档位。
- 自定义账号统计定价使用原始 tokens；“客户定价应用到账号统计”分支使用转换后 TotalCost，这是当前语义。
- 初次审计发现部分非流式 Responses/embeddings 在转换溢出时返回 nil,error，可能丢失用量证据；不要称其已得到全面修复。

## 下游 usage

- 开启转换时下游也改写，关闭时保持原路径。
- 计费和下游改写分别从原始 usage 计算一次，避免双重转换。
- OpenAI 输入总量与 total_tokens 同步调整；Anthropic 的 input_tokens 保持普通输入语义。
- 别名、显式 0、5m/1h 创建明细、多行 SSE frame、强制上游 stream usage 都要保留。
- 覆盖 Responses、Chat、Messages 的 JSON/SSE、协议桥接和回退、直通、WS文本帧、图片和 embeddings。不能只保留新增转换模块而丢掉这些出口调用。
- WS 二进制帧沿用原先直通范围；没有执行探测时不得宣称所有 WS 都改写。

## 后续新增：首字提前响应

- 保留独立“首字设置”页签、默认关闭和随机毫秒范围校验；随机数只控制真实 SSE 注释发送时机。
- `first_response_ms` 与原 `first_token_ms` 分开存储；普通用户“首字”使用实测首包字段，管理员额外显示“上游首字”。用户 API、导出、渠道监控不能旁路暴露原上游 TTFT；历史缺少首包测量的记录不回填。
- 保留原上游 semantic/visible 计时、调度、缓存转换及账单结果。首包统计必须跨异步 usage worker 传递，不能保留已回收的 Gin context。
- 排队心跳、原有流内心跳和提前心跳分别计数，统一从正文提交判定中扣除；只发心跳仍允许换号，且不关闭后续周期保活。真实正文开始后不再插入提前心跳。
- 提前 HTTP 200 后使用入站协议对应的 SSE 错误终止事件；不能在 SSE 后追加裸 JSON，也不能把 Responses 错误帧发给 Messages/Chat。
- 私有响应头副本、写入锁和停止等待保护并发；UI Server-Timing 中间件不可被请求头启用到模型网关路径。

## 首次二开文件组（基线）

新增模块：

- `backend/internal/pkg/cacheconversion/usage.go`
- `backend/internal/service/account_cache_conversion_validation.go`
- `backend/internal/service/openai_cache_conversion.go`
- `backend/internal/service/openai_cache_conversion_output.go`

关键接入点：

- `backend/internal/service/admin_account.go`
- `backend/internal/service/billing_service.go`
- `backend/internal/service/openai_gateway_usage.go`
- `backend/internal/service/openai_gateway_response_handling.go`
- `backend/internal/service/openai_gateway_passthrough.go`
- `backend/internal/service/openai_gateway_cc_pipeline.go`
- `backend/internal/service/openai_gateway_chat_completions*.go`
- `backend/internal/service/openai_gateway_messages*.go`
- `backend/internal/service/openai_gateway_responses*{anthropic_native,chat_fallback}.go`
- `backend/internal/service/openai_ws_forwarder_ingress.go`
- `backend/internal/service/openai_ws_forwarder_v2.go`
- `backend/internal/service/openai_ws_v2_passthrough_adapter.go`
- `backend/internal/service/openai_images*.go`
- `backend/internal/service/openai_embeddings.go`
- `frontend/src/components/account/CreateAccountModal.vue`
- `frontend/src/components/account/EditAccountModal.vue`
- `frontend/src/i18n/locales/{zh,en}/admin/accounts.ts`
- `frontend/src/types/index.ts`

后续新增功能也要纳入当前差异清单；这些路径不是允许忽略其他文件的白名单。
