# pk.65 分组 Ultrafast 开关

## 请求与范围

用户要求在分组管理增加一个开关，使该分组的账号请求使用 Ultrafast，免去逐条配置全局档位规则。基于固定仓库 main `2ee0b7d30242938c4b798491795ab8359c8deb0c` 实施，扩展 PK-042，沿用 PK-004/021/031，无新增独立 PK ID。没有合并外部源码或升级依赖。

本次交付源码；未构建或发布正式制品，未修改生产分组、账号或部署。没有进行真实模型收费请求或实际速度测试。

## 行为

- 管理员在分组新增/编辑页的 OpenAI Fast / Ultrafast 区域开启“强制使用 Ultrafast”。适用于 OpenAI 和 Composite 分组，实际选中账号必须是 OpenAI。
- 只以当前请求的可信、完整加载的分组为准，不修改共享账号的全局属性；同一账号通过其他分组调用时仍按其他分组策略执行。
- 使用账号映射、协议归一之后的实际上游模型名。只有 `gpt-6-astra` 和 `gpt-6.1-sol` 自动写入 `service_tier=ultrafast`；API Key 上游未经映射的自定义别名不被推断为支持此档位。
- 客户端省略档位或指定其他档位均可被开关覆盖。其他模型保留原行为。关闭开关恢复原策略，并不禁止客户端自行申请 Ultrafast。
- UI 主动开启 Fast/Ultrafast 中一项时关闭另一项；已有或 API 写入的两个字段同时为 true 时如实显示并保存。两种目标模型优先 Ultrafast，其余模型继续原 Fast 行为。
- 分组先写入档位，全局规则随后执行；现有用户范围、模型范围、过滤、拦截及强制改写优先级保持。开关不会绕过全局禁止规则，也不会赋予上游账号新的套餐权益。
- HTTP 新请求使用最新分组缓存；已建立的 WebSocket 沿用连接上下文，管理员修改后需要重连。
- 界面说明 8 倍属于套餐额度消耗，不保证实际速度。费用继续使用现有出站/上游响应档位结算规则；不改价格、历史账单或“免费 Fast”适用范围。

## 保存与兼容

- 新增 `341_group_force_openai_ultrafast.sql`，以可重放 ADD COLUMN 增加 `groups.force_openai_ultrafast BOOLEAN NOT NULL DEFAULT FALSE`。已有组默认关闭，旧程序省略该列仍能写入；重放迁移不重置已启用值。
- Ent 使用现有锁定依赖生成。创建 bool、更新 *bool；省略更新保留已读取值，显式 false 关闭。非支持平台清除该开关。
- 管理员 DTO、分组复制、Group Repository、API Key 鉴权窄投影和候选组映射接入新字段。普通用户分组 DTO 不增加该管理策略。
- 认证快照从 v34 升至 v35，旧版本重新加载；JSON 缺字段默认 false。沿用现有组更新缓存失效和 outbox 行为。
- 最终实现不包含 settings 侧存、额外热路径设置查询、内部写入意图字段或 outbox 事务语义变更。

## 入口与提示

业务入口：`openai_gateway_forward.go` 的原生 Responses patch、`applyOpenAIFastPolicyToBody` 和 `applyOpenAIFastPolicyToWSResponseCreate` 共同调用 `openAIGroupForcedServiceTier`。复用现有 HTTP、普通 OAuth/API Key、Chat/Messages 桥接和 WebSocket 档位处理入口；不声称覆盖所有独立专用上游协议。

SIWC 最终回到共享透传路径，包含该策略。显式开启的 Prism Browser / Excel BPS 使用独立传输，当前不会应用本开关；普通 Pro500 不会因套餐类型自动进入这些路径。未核查生产账号是否开启了这些专用协议。

UI：`GroupsView.vue`、`groupsOpenAIFast.ts`、中英文管理文案及类型。新提示 ID 为 `group-openai-ultrafast-initial-announcement`，分组列表、新增和编辑共用，沿用当前管理员/浏览器的 localStorage 关闭记录。

## 核对与验证

- PK-042：实际请求组隔离、创建/更新省略语义、复制、候选组、鉴权缓存和全局策略优先级。
- PK-004：没有修改计费实现；既有 Ultrafast 默认倍率为 6，修正旧专项测试仍写 2 的陈旧断言；不扩大免费 Fast 的豁免。
- PK-007～012：没有修改首字计时、心跳、响应终态或用量转换。
- PK-031：新增/编辑/列表开关提示、中英文、旧字段缺省与可见状态。
- PK-021：清单、迁移与真实验证边界同步登记；正式发行/HA 升级须另按既有制品和迁移验收流程执行。

本机实际结果（不同轮次有重叠，不合并成独立测试总数）：

- 最终网关/缓存/分组定向：60 个顶层、144 个含子项 pass，无失败/跳过。覆盖 HTTP/WS、原生和透传 Responses、Chat/Messages 桥接、最终模型映射正反例、全局过滤/拦截/改写、旧 Fast 行为、缓存和复制。
- 后端字段接入：5 包 17 个顶层、63 个含子项 pass，无失败/跳过。包含 SQLite 创建/关闭、鉴权窄投影、多候选组、DTO/API 和迁移文本检查；v35 缓存专项另有 2 项通过。
- 前端：原有 helper 5 项、翻译键 3 项通过；补齐公告后分组组件 11 项通过，包括 3 入口同步关闭、持久化和管理员隔离。类型检查通过；全部 8 个修改的前端文件 ESLint 无错误或警告。
- `git diff --check` 通过。Ent 生成未改变 go.mod/go.sum；旧迁移未修改。

测试均为本机模拟或 SQLite。已扩展原有 PostgreSQL 集成用例验证迁移默认值、旧写入、重放和鉴权投影，但本轮没有可用的本机 PostgreSQL/WSL 环境，未执行真实 PostgreSQL 集成验证。未运行全套源码回归、浏览器端到端或生产性能测试。

开发期间 sidecar 草案的旧范围测试发现既有 `TestAPIKeyGroupsOmitLegacySoftDeletedAdditionalBinding` 失败；用 HEAD 原实现复测也失败，保留原断言及失败记录，不列为本次修复。最终物理列方案的定向测试不包含该无关用例，不把定向通过宣称整个仓库全绿。
