# pk.61 上游全域集成与二次复核

记录日期：2026-11-11。本文是本轮源码适配、功能登记与验收范围记录；正式发行和生产更新必须有各自的实际回执。本文截止时尚未完成新版本正式发行、正式制品迁移回退验收或 HA 上线；源码迁移的隔离真实 PostgreSQL fresh/upgrade 已通过，不能据此宣称生产已经运行 pk.61。

## 固定输入、范围与当前阶段

| 输入 | 固定值 |
|---|---|
| 私库基线 | `6ed612909fef4510f63d93a8f2ead58bae35a3c8` |
| 上游 production | `d42bb4fc4b3eef9821994b4ad170d4eb881eca95` |
| 共同祖先 | `efe9aab1e4ec89a42ba45e8dac20e882c5409a6a` |
| 唯一目标版本 | `v0.2.19-pk.61` |
| 功能登记 | [PK_FEATURES.md](../PK_FEATURES.md)，有效 PK-001～028、031～075；029/030 保留移除历史 |

用户要求吸收固定上游缺失功能、完整保留私库二开、再次全量复核，并加入普通用户只读降智检测与搜索多选防蒸馏豁免。采用共同祖先上的三方差异；没有按整目录覆盖、机械全选一侧或恢复已移除的 PK-029/030。新增独立能力按产品功能登记 PK-049～075；已有 Key 请求策略与搜索多选豁免分别扩展 PK-042、PK-048，不重复发 ID。

文件清点共 8215 个联合路径：私库 4968、上游 7696；相同 3200、共有但不同 1249、上游独有 3247、私库独有 519。64 个功能域见下表。文件数是差异清点规模，不是独立新功能数，也不是执行过的用例数。

当前授权允许本机构建、服务器隔离同制品验收、正式私库发行与逐节点更新；它覆盖本轮旧的 Jenkins-only 限制。正式 tag/main、SHA256、十一项制品门禁、单 writer/worker 与旧标签不可移动仍有效。只执行必要定向检查，不运行全套源码测试。源码、构建、迁移、发布与上线分别记录，不挪用旧版本通过证明当前版本。

## 原 PK-001～048 逐项保留依据

“已保留（源码）”指三方适配后的保护合同和入口仍在，实际定向证据见右列及下文。它不等于该功能所有协议/历史数据路径均已做完整动态验收；表内明确未做的真实外部调用和待制品项目不计为通过。

| ID | 结论 | 必须保留的合同 | 入口与本轮证据 |
|---|---|---|---|
| PK-001 | 已保留（源码） | 新增/编辑、默认值、倍率 0、extra 保存与校验 | `backend/internal/service/account_cache_conversion_validation.go`；计费/耐久结算/仓储交叉定向；新增协议未全部真实调用 |
| PK-002 | 已保留（源码） | decimal 向下取整、溢出检查、创建明细清零 | `backend/internal/pkg/cacheconversion/usage.go`；计费/耐久结算/仓储交叉定向；新增协议未全部真实调用 |
| PK-003 | 已保留（源码） | 原始输入先拆分、转换一次、各账务口径一致 | `backend/internal/repository/usage_billing_repo.go`；计费/耐久结算/仓储交叉定向；新增协议未全部真实调用 |
| PK-004 | 已保留（源码） | 原始档位、独立倍率、原始/客户统计口径 | `backend/internal/service/billing_service.go`；计费/耐久结算/仓储交叉定向；新增协议未全部真实调用 |
| PK-005 | 已保留（源码） | JSON/SSE 无损、别名/显式 0、原始用量保留 | `backend/internal/service/openai_cache_conversion_output.go`；计费/耐久结算/仓储交叉定向；新增协议未全部真实调用 |
| PK-006 | 已保留（源码） | Responses/Chat/Messages、直通、桥接、WS、图片、embeddings | `backend/internal/service/openai_gateway_response_handling.go`；计费/耐久结算/仓储交叉定向；新增协议未全部真实调用 |
| PK-007 | 已保留（源码） | 独立页签、默认关闭、随机范围、局部保存与缓存 | `frontend/src/views/admin/SettingsView.vue`；首字/SSE 与 handler/server 定向；普通用户 timing 投影复核 |
| PK-008 | 已保留（源码） | 注释/ping/空格三模式、协议边界、禁用时沿用原上游首字 | `backend/internal/service/openai_early_sse_heartbeat.go`；首字/SSE 与 handler/server 定向；普通用户 timing 投影复核 |
| PK-009 | 已保留（源码） | 开启时真实首包、关闭时原上游首字、请求快照、异步传递、所有 SQL 路径 | `backend/migrations/241_usage_log_first_response_ms.sql`；首字/SSE 与 handler/server 定向；普通用户 timing 投影复核 |
| PK-010 | 已保留（源码） | 用户原文案、管理员上游指标、导出和监控不泄露 | `backend/internal/service/channel_monitor_v2.go`；首字/SSE 与 handler/server 定向；普通用户 timing 投影复核 |
| PK-011 | 已保留（源码） | 注释/ping不占正文提交判定，空格算正文，周期保活不丢 | `backend/internal/handler/gateway_helper.go`；首字/SSE 与 handler/server 定向；普通用户 timing 投影复核 |
| PK-012 | 已保留（源码） | 协议终止事件、响应头隔离、写锁、停止与 context 生命周期 | `backend/internal/handler/gateway_helper.go`；首字/SSE 与 handler/server 定向；普通用户 timing 投影复核 |
| PK-013 | 已保留（源码） | “版本号 + PK出版”、pk 修订按数值比较 | `frontend/src/components/common/VersionBadge.vue`；私库更新定向；Windows Unix语义跳过待 Linux 补验 |
| PK-014 | 已保留（源码） | 私库/私有镜像、只读凭据、无官方回退路径 | `backend/internal/service/update_service.go`；私库更新定向；Windows Unix语义跳过待 Linux 补验 |
| PK-015 | 已保留（源码） | 资产授权、跨域去凭据、SHA256、重复更新固定路径 | `backend/internal/service/update_service.go`；私库更新定向；Windows Unix语义跳过待 Linux 补验 |
| PK-016 | 已保留（源码） | 数据卷可执行文件、种子哈希、镜像回退边界 | `deploy/docker-entrypoint.sh`；私库更新定向；Windows Unix语义跳过待 Linux 补验 |
| PK-017 | 已保留（源码） | 正式标签、授权本机构建或 Jenkins、同制品验收、私库发布、amd64 | `deploy/local-release.py`；私库更新定向；Windows Unix语义跳过待 Linux 补验 |
| PK-018 | 已保留（源码） | 可跳过全局启动清理，继续清理过期槽位 | `backend/internal/service/wire.go`；生命周期/HA 源码与定向；新 HA 制品及迁移门禁待完成 |
| PK-019 | 已保留（源码） | 受控后台、普通文件激活、只启动一次、停止安全，含账号授权 | `backend/internal/service/background_runtime.go`；生命周期/HA 源码与定向；新 HA 制品及迁移门禁待完成 |
| PK-020 | 已保留（源码） | 保护端口/数据/密钥、请求排空、后台单实例交接 | `PK_MAINTENANCE.md`；生命周期/HA 源码与定向；新 HA 制品及迁移门禁待完成 |
| PK-021 | 已保留（源码） | Git持久化入口、唯一清单、增量登记、逐项合并审查 | `AGENTS.md`；登记机制、有效 ID 和固定三方输入已核对 |
| PK-022 | 已保留（源码） | 完整五位金额乘订单倍率、账户加密、流水去重、过期回查、订单权限 | `backend/internal/server/routes/binance_recharge.go`；原币安路径保留；未执行真实交易/出口探测 |
| PK-023 | 已保留（源码） | 复用全部使用记录、默认今日全用户、可配置自动刷新、仅管理员 | `frontend/src/views/admin/UsageView.vue`；原入口及品牌/展示保留；前端定向范围见下文 |
| PK-024 | 已保留（源码） | 直接只读查询收支、日期与收款账户筛选、金额原精度、与入账隔离 | `frontend/src/components/payment/BinanceLiveBills.vue`；原币安路径保留；未执行真实交易/出口探测 |
| PK-025 | 已保留（源码） | 每次操作重新探测、固定成功节点、香港校验、失败关闭、凭据隔离 | `backend/internal/config/config.go`；原币安路径保留；未执行真实交易/出口探测 |
| PK-026 | 已保留（源码） | 独立菜单、当天与日期筛选、实时刷新、用户只读、精确倍率、逐条防重复扣费 | `backend/internal/service/cyber_policy.go`；安全类别/豁免与风险服务合同定向；未处罚生产用户 |
| PK-027 | 已保留（源码） | 默认关闭、可配置次数、独立累计、管理员豁免、解禁可重计、鉴权失效 | `backend/migrations/245_cyber_policy_auto_disable.sql`；安全类别/豁免与风险服务合同定向；未处罚生产用户 |
| PK-028 | 已保留（源码） | 独立默认关闭、用户模型映射、账号代理、ultra 转 max、工具参数保真 | `backend/internal/service/account_kilo_bridge.go`；Messages/Key/Kilo 7 项交叉定向；原生开关隔离 |
| PK-029 | 已授权移除，未恢复 | 已授权移除；恢复 pk.1 计费与输出，保留历史记录 | 原ID与历史说明保持，不复用ID；未引入隐藏倍率或原始Token趋势旧功能 |
| PK-030 | 已授权移除，未恢复 | 已授权移除；恢复 pk.1 趋势口径，保留历史数据 | 原ID与历史说明保持，不复用ID；未引入隐藏倍率或原始Token趋势旧功能 |
| PK-031 | 已保留（源码） | 渐变标志、统一名称、胶囊导航、移除友链，保留权限、响应式与深色模式 | `frontend/src/components/layout/AppSidebar.vue`；原入口及品牌/展示保留；前端定向范围见下文 |
| PK-032 | 已保留（源码） | 全分组/类型并发、失败自动重试、保存清空重启、当次模型/强度、糖果文案/回复、每行6张、专用 Key 轮换、回环调用、最近30条 | `frontend/src/components/home/HomeIntelligenceResults.vue`；质量/授权/worker 交接定向；未真实授权或模型请求 |
| PK-033 | 已保留（源码） | 文字/二维码独立可选、上传校验、首页/登录/用户仪表盘、条件锚点及醒目展示 | `backend/internal/service/setting_contact.go`；原入口及品牌/展示保留；前端定向范围见下文 |
| PK-034 | 已保留（源码） | OpenAI 全部账号类型、固定账号直测、手动/独立定时、答案21、失败不转组、不合格移除全部原组 | `backend/migrations/251_account_candy_tests.sql`；质量/授权/worker 交接定向；未真实授权或模型请求 |
| PK-035 | 已保留（源码） | 分组/账号/授权巡检页签、账号卡片、多模型判定、出口IP、服务端筛选、新表隔离、旧历史只读兼容 | `frontend/src/views/admin/RadarObservationView.vue`；质量/授权/worker 交接定向；未真实授权或模型请求 |
| PK-036 | 已保留（源码） | 原生OAuth并发、独立代理预检、CF挑战页内完成与无头→后台Chrome身份升级、加密凭据历史、cron巡检/重授权、可配错误码、实时账号、缺凭据补全 | `backend/internal/service/account_authorization_browser.go`；质量/授权/worker 交接定向；未真实授权或模型请求 |
| PK-037 | 已保留（源码） | 最近完成首字、连续上游失败、五批错峰健康度快照、最快/最慢首字、平台错误排除、雷达统计进度 | `backend/internal/repository/account_request_stats.go`；共享错误率与账号兼容定向，旧健康口径保持 |
| PK-038 | 已保留（源码） | 可选额度/合格/非401条件、阈值与收件人、持久化cron、系统SMTP复用、结果展示与跨实例互斥 | `backend/internal/handler/radar_email_alert_handler.go`；通知入口/后台归属复核；未真实发信 |
| PK-039 | 已保留（源码） | 管理员勾选、OpenAI/Claude OAuth、3并发、逐项反馈、只查询不兑换 | `backend/internal/handler/admin/claude_reset_handler.go`；管理权限/批量入口复核；未真实批量执行 |
| PK-040 | 已保留（源码） | 勾选确认、最多500项、协议原子更新、完整管理员、逐项反馈 | `backend/internal/service/admin_proxy.go`；管理权限/批量入口复核；未真实批量执行 |
| PK-041 | 已保留（源码） | 明确选组、空范围跳过、关键词/RE2、七类模板、异步记录/同步拦截 | `frontend/src/components/admin/user/UserAllowlistSelector.vue`；安全类别/豁免与风险服务合同定向；未处罚生产用户 |
| PK-042 | 已保留（源码） | 最多20组、首组兼容、有序路由、Key队列/模型/强度/长上下文、倍率分列 | `backend/internal/service/api_key_request_policy.go`；有序 JSON/关系镜像、Key策略及 HTTP/WS 轮次定向 |
| PK-043 | 已保留（源码） | 最高风险分、可调阈值/三动作、独立选组豁免、真实完成费用、双审计数去重 | `docs/PK_AUDIT_MENUS_PK55_REVIEW.md`；安全类别/豁免与风险服务合同定向；未处罚生产用户 |
| PK-044 | 已保留（源码） | 每请求随机且流内固定、全局代理并发、账号总并发、批量/一键原子分配、影子继承、旧主代理兼容 | `docs/PK_PROXY_GROUP_SHARING_PK53_REVIEW.md`；全局代理/严格优先与区域交叉定向；无容量压测 |
| PK-045 | 已保留（源码） | 完整候选池共享、Priority/ID顺序、按并发分配IP子集、旧请求排空、跨组物理互斥 | `docs/PK_PROXY_GROUP_SHARING_PK53_REVIEW.md`；全局代理/严格优先与区域交叉定向；无容量压测 |
| PK-046 | 已保留（源码） | 显式协商、真实扣款回执、双端幂等、多跳终态、余额流水、原生接口兼容 | `backend/internal/service/cyber_policy_balance_worker.go`；耐久审计结算与仓储幂等定向 |
| PK-047 | 已保留（源码） | 独立数据、三机仲裁、单后台职责、共享准入、有序退出、私库干净发布包 | `docs/PK_CLUSTER_FLEET_CAPACITY_REVIEW.md`；生命周期/HA 源码与定向；新 HA 制品及迁移门禁待完成 |
| PK-048 | 已保留（源码） | 默认关闭、原始缓存证据、客户跨Key/组20条中18条零缓存、共享暂停、按组用户白名单、排队/WS复查、不改计费和账号健康 | `frontend/src/components/admin/user/UserAllowlistSelector.vue`；搜索多选名单、CAS、HTTP/WS 实际组与重复路由修复定向 |

## 新增能力与 64 个功能域的适配范围

新登记的完整用途、默认/配置、存储/权限、代码入口与边界见 PK 清单。下表列出所有功能域的兼容合同、入口和当前证据，包含实验与外置工具功能；“未真实调用”不等于删除功能。静态入口完整、定向测试通过和生产可用是不同结论。

| 功能域 | PK 对应/适配合同 | 代表入口 | 当前证据和限制 |
|---|---|---|---|
| R01 中央接线/DTO/配置与共享UI | 全部有效PK；统一DTO、配置、构造器和路由接线 | `backend/internal/service/lottery_wire.go`、`backend/internal/handler/admin/setting_handler.go` | DTO/构造器/路由复核，handler/server 六包及普通Wire cleanup复验通过；Linux交叉构建成功 |
| R02 缓存转换/长上下文/服务档位/模型定价 | PK001-006、042、046；关闭PK开关的路径按固定上游，保留PK500k门槛/显式覆盖及原始用量 | `backend/internal/service/api_key_long_context_admission.go`、`backend/internal/handler/api_key_long_context_policy.go` | 计费首轮20通过/4失败，修复旧门槛与时间夹具后4项通过；保留500000/500001边界 |
| R03 结算quote/交付/outbox/平台额度恢复 | PK003/046；一次扣款、真实完成、日志与各配额同步、重放不重复入账 | `backend/internal/service/billing_inflight_reservation.go`、`backend/internal/repository/batch_image_owned_money.go` | durable/audit 9顶层、repository billing 23顶层通过；源码迁移隔离PG已过，正式旧二进制回退待验收 |
| R04 首字/语义TTFT/分段计时/队列耗时 | PK007-012；上游语义计时不能覆盖私库三模式真实下游首包 | `backend/internal/service/account_connection_timing.go`、`backend/internal/handler/admin/latency_observability.go` | SSE pump 7顶层及 handler timing 定向通过；无真实模型流压测 |
| R05 API Key并发/等待/租约TTL | PK012/042；队列重验证、跨实例释放、多协议每轮准入 | `backend/internal/service/api_key_queue.go`、`backend/internal/server/middleware/api_key_admission_owner.go` | Key排队、HTTP/WS轮次和释放定向通过，含handler/service交叉；不等于3000并发 |
| R06 API Key模型映射/推理强度 | PK042；原始与目标模型权限和实际计费映射 | `backend/internal/service/api_key_model_policy_settings.go`、`backend/internal/server/middleware/api_key_model_policy.go` | Messages/Key/Kilo 7项与handler策略定向通过，保留原始/目标模型权限 |
| R07 API Key长上下文开关 | 默认true；成本控制与PK原始上下文/影子账号/不可验证历史相容 | `backend/internal/service/api_key_long_context_admission.go`、`backend/internal/handler/api_key_long_context_policy.go` | Key长上下文入口保留并定向；默认允许/显式关闭与原始档位核对 |
| R08 API Key多组/上游渠道声明 | PK042私库有序JSON最多20组；上游关联表无顺序，不得覆盖 | `backend/internal/service/api_key_group_anti_distillation.go`、`backend/internal/handler/api_key_group_router.go` | 私库有序JSON为事实，关系表事务镜像；账号兼容和前端/handler定向通过 |
| R09 组stream-only/Codex CLI/subagent与用户拒绝模型 | PK042多组实际路由、PK006多协议，默认关闭新限制 | `backend/internal/service/group_subagent_policy.go`、`backend/internal/server/middleware/group_block_subagents.go` | 所有组限制入口保留；实际组准入与本地拒绝归因有定向证据，未生产启用新限制 |
| R10 分组防蒸馏 | PK048已移植并有pk60白名单；取增量但完整保留按组用户白名单 | `backend/internal/service/api_key_group_anti_distillation.go`、`backend/internal/handler/admin/group_anti_distillation.go` | PK-048/60搜索多选名单与CAS保留；修复重复路由，类别/豁免定向通过 |
| R11 范围管理员Key/按张图片计费 | 两边已从官方v0.2.19集成；迁移310/311与私库247/248内容完全相同 | `backend/internal/service/custom_admin_key_accounts.go`、`backend/internal/handler/admin/custom_admin_key_accounts.go` | 已从官方基线等价吸收；异名同内容迁移单独登记，源码新旧迁移隔离PG已过，正式制品回退待验收 |
| R12 OpenAI多授权连接/刷新fencing/归属 | PK-050；PK036授权历史、PK044代理继承、PK003计费凭据归属 | `backend/internal/service/openai_auth_connection_egress.go`、`backend/internal/handler/admin/openai_auth_connections.go` | 多连接/归属/出口源码与账号兼容定向；未真实刷新授权 |
| R13 OAuth队列重登/浏览器runtime | PK-050；PK036已有浏览器授权；worker角色统一、凭据加密、出口一致，避免双套后台同时写账号 | `backend/internal/service/openai_oauth_reauth.go`、`backend/internal/handler/admin/openai_oauth_reauth_handler.go` | PK浏览器与上游重登接线/worker归属保留；未真实登录 |
| R14 2FA轮换/Session导出/SIWC与双OAuth | PK-050；PK036账号凭据、step-up权限、后台browser权限；新增操作默认不执行 | `backend/internal/service/openai_session_export.go`、`backend/internal/handler/admin/account_session_export.go` | 轮换/导出/SIWC与step-up入口保留；未真实执行敏感操作 |
| R15 凭证守护V1/V2/加密/重登预算 | PK-051；PK036/019；一个后台owner，持久预算，不因切主重复登录 | `backend/internal/service/account_token_guard.go`、`backend/internal/handler/admin/account_token_guard_handler.go` | 凭证守护预算/生命周期源码与定向核对；未真实触发重登 |
| R16 Codex采票/探针/并行/节点学习/固定出口 | PK-052；默认关闭；PK044全局代理容量、PK036会话身份，不共享私密凭据到文档 | `backend/internal/service/astra_ticket_lifecycle.go`、`backend/internal/handler/admin/account_codex_harvest_controls.go` | 采票/探针/并行/学习/出口入口齐全；未真实采票 |
| R17 Astra号池自动调度/门票糖果实验 | PK-053；Beta/实验保留入口和默认关闭；不能绕过PK045严格账号优先级/组并发 | `backend/internal/service/astra_account_scheduling.go`、`backend/internal/handler/admin/account_astra_gateway.go` | Astra池与实验入口保留，严格调度交叉核对；未启动实验池 |
| R18 账号性能阈值自动独立暂停 | PK-059；共享PG计算TPS/TTFT/质量轮次；原HTTP错误率仅本地，已接有界异步共享Redis，partial/unknown与单worker评估保持 | `backend/internal/service/account_performance.go`、`backend/internal/handler/admin/account_performance.go` | 有界共享Redis已适配；双HTTP writer→第三worker及关闭故障、前端partial/unknown通过 |
| R19 质量规则/模板/preset/归属/5xx/401清理/Jev | PK-049/PK-054；原5xx信号已跨节点Redis共享；质量/糖果保留PK语义，单writer门控，停机拒绝排队并等待已发结果保存 | `backend/internal/service/account_quality.go`、`backend/internal/handler/admin/quality_ownership_handler.go` | 恢复116顶层+单项时间fixture复验、合约51项及类别/名单11项（重叠不累加） |
| R20 渠道监控V2糖果/多模型/时间线/排序 | PK-049/PK-055；PK034/035及统计追赶Go修复保留；用户只读结果入口，不公开账号凭据 | `backend/internal/service/channel_monitor_v2.go`、`backend/internal/handler/channel_monitor_v2_handler.go` | V2聚合/糖果入口保留，持久追赶补丁在源码；worker排空定向通过 |
| R21 渠道监控V3组件/事件/空闲探测 | PK-055；新独立监控表，默认既有模式；单worker，故障归因和计费不重复 | `backend/internal/service/channel_monitor_v3.go`、`backend/internal/handler/channel_monitor_v3_handler.go` | V3独立事件/探测/页面保留；worker归属核对，未真实全模式探测 |
| R22 Pelican定时/分组测试/展示与公开API | PK-056；用户授权展示页面需脱敏、只读/公开开关和权限，不等同管理员质量后台 | `backend/internal/service/pelican_scheduled.go`、`backend/internal/handler/pelican_showcase_handler.go` | 定时/展示/公开API入口与权限保留；未生成真实模型作品 |
| R23 ModelTrace指纹与受控实验 | PK-057；实验须显式启动、配额/开销/目标限制，保留默认关闭 | `backend/internal/service/controlled_experiment.go`、`backend/internal/handler/admin/controlled_experiment_handler.go` | 指纹/实验计划/评分入口齐全；未执行真实实验或模型鉴定 |
| R24 优先调度/延迟反馈/公平/粘性会话管理 | PK-058；PK045严格Priority/ID及物理IP互斥是私库合同，需显式调度模式兼容 | `backend/internal/service/priority_analysis.go`、`backend/internal/handler/admin/priority_scheduling_handler.go` | 严格PK模式与优先/公平/粘性并存；EWMA及账号兼容定向通过 |
| R25 账号运维阈值/通知/webhook/运营分组 | PK-058；PK038邮件/多实例互斥保持，告警阈值去重与主动通知不能默认开启 | `backend/internal/service/account_ops.go`、`backend/internal/handler/admin/account_ops_handler.go` | 账号阈值/通知/运营分组与PK邮件并存；未发送真实通知 |
| R26 账号自动配置/日志/调度定时窗口 | PK-058；PK019/034/036/047；持久cron统一owner，定时配置不覆盖用户手工设置 | `backend/internal/service/account_schedulable_timer.go`、`backend/internal/handler/account_auto_config_outcome.go` | 自动配置/日志/定时窗口与单worker接线核对；未批改生产配置 |
| R27 Mihomo内核/订阅/动态地区/节点静态导入 | PK-060；PK044/045共享容量与静态组绑定，外置进程资源/只读root/持久目录 | `backend/internal/service/admin_mihomo_node.go`、`backend/internal/handler/admin/system_mihomo.go` | Mihomo/动态/静态导入入口齐全；代理交叉定向，外置内核未生产验收 |
| R28 按域名/账号的区域出口 | PK-060；默认关闭，无静默直连；PK044已选代理与PK045出口身份要明确优先级 | `backend/internal/pkg/upstreamroute/context.go`、`backend/internal/pkg/upstreamroute/router.go` | 区域出口与既有代理容量交叉定向；首轮缺缓存夹具修复后单项通过 |
| R29 BPS核心OAuth/转发/工具目录与代理任务 | PK-061；默认关闭；PK001-012/042/044/046必须覆盖新网关入口，图片/工具/续接不是旁路 | `backend/internal/service/account_excel_bps_403_marker.go`、`backend/internal/handler/openai_excel_bps_image.go` | BPS OAuth/转发/工具/图片入口保留，PK交叉接线；未真实授权/工具请求 |
| R30 Prism浏览器桥/Copilot SDK | PK-062；保留新通道但默认关闭，与PK028 Kilo独立，凭据/模型/代理/计费不可混用 | `backend/internal/service/account_prism.go`、`backend/internal/pkg/prismbridge/.keep` | Prism/Copilot独立通道与Kilo分离；构造器编译通过，未真实外部调用 |
| R31 Anthropic身份模拟/抹除 | 身份抹除优先；可选估算token扣减不能污染PK真实usage | `backend/internal/service/gateway_anthropic_mimic_claude_code.go`、`backend/internal/service/gateway_anthropic_strip_claude_code_identity.go` | 身份模拟/抹除和真实usage边界源码核对；未真实Claude身份探测 |
| R32 DeepSeek工具文本/图片schema/compact | 基础已在私库；新增兼容修复需PK缓存/usage/首字完整接线 | `backend/internal/service/openai_deepseek_compact.go`、`backend/internal/pkg/apicompat/deepseek_tool_text.go` | DeepSeek工具/图片/compact保留；compact定向结果与更新器平台跳过分开记录 |
| R33 Mistral Chat与历史回传 | 新增适配；保留工具参数/多模态和PKusage/首字 | `backend/internal/service/mistral_usage.go`、`backend/internal/service/openai_gateway_mistral.go` | Mistral历史/工具/多模态入口保留；协议源码与handler编译核对 |
| R34 OpenAI HTTP上传/首输出预算/恢复/身份/时区 | PK007-012禁用/启用各模式、取消和终态计费与新deadline一致 | `backend/internal/service/openai_context_limit_error.go`、`backend/internal/service/openai_encrypted_content_lineage.go` | 上传/首输出/恢复/取消预算与PK首字交叉核对；SSE与计费定向通过 |
| R35 WebSocket SSE加速/智能协议/代理续接/owner | 默认关闭新加速/owner开关；每轮Key/组/配额复验与语义终帧交付 | `backend/internal/service/openai_ws_agent_recovery.go`、`backend/internal/config/ws_node_owner.go` | WS新入口/owner/续接保留；实际组、每轮准入、终帧交付定向通过 |
| R36 OpenAI其他网关/模型/配额恢复/鉴权回退 | 残余OpenAI共享域逐文件复核，不能因已匹配此域就称行为等价 | `backend/internal/service/openai_403_counter.go`、`backend/internal/handler/admin/openai_auth_connections.go` | 共享OpenAI残余逐文件登记；旧声明替代链复核，未宣称全部真实协议覆盖 |
| R37 Anthropic/Claude/Antigravity多协议 | 基础已有，吸收新兼容/失败归因且保留PK首字/缓存 | `backend/internal/service/anthropic_apikey_auth.go`、`backend/internal/handler/admin/antigravity_oauth_handler.go` | Claude/Antigravity与PK首字/usage接线保留；reasoning-only直连27顶层通过 |
| R38 Gemini兼容/缓存/图片与多模态 | 官方v0.2.19大量已等价，仍核新桥和长上下文/计费 | `backend/internal/service/antigravity_gateway_gemini.go`、`backend/internal/handler/admin/gemini_oauth_handler.go` | Gemini原功能/缓存资源与实际组归属保留；handler/front定向；未真实媒体请求 |
| R39 Grok/XAI协议/音视频/403/模型目录 | 已有官方功能与新403策略区分；保留计费模型及PK多代理 | `backend/internal/service/account_grok_media_eligibility.go`、`backend/internal/handler/admin/grok_import_probe.go` | Grok目录/403/音视频入口保留；账号/模型/usage源码核对，无真实外部调用 |
| R40 MiniMax-H3/Seedance等音视频 | 新增或增强多模态入口需定价、配额、权限、body上限，不能绕过PK审计 | `backend/internal/service/grok_audio.go`、`backend/internal/handler/grok_audio.go` | MiniMax-H3/Seedance等媒体入口保留并核PK鉴权/账务交叉；未真实媒体生成 |
| R41 网络/连接诊断/流量/观察者使用记录 | PK-063；PK010管理员上游指标与普通用户脱敏边界；真实流量/firstbyte不回填 | `backend/internal/service/admin_observer_setup.go`、`backend/internal/handler/admin/latency_observability.go` | Observer强制本人及普通用户脱敏/timing定向通过；管理员上游指标保持 |
| R42 请求采集HTTP/WS/导出 | PK-064；管理员显式任务；限制大小/保留期与脱敏，不能捕获凭据直接展示给普通用户 | `backend/internal/service/openai_ws_request_capture.go`、`backend/internal/handler/admin/request_capture_handler.go` | HTTP/WS采集/导出和权限入口保留；未开启生产请求采集 |
| R43 AI Ops Codex诊断/提案/受控执行 | PK-065；需工具/执行权限与回滚边界，默认关闭；不能让AI绕过私库发布/HA守卫 | `backend/internal/service/ai_ops_service.go`、`frontend/src/views/admin/AIOpsView.vue` | AI Ops工具/提案/执行入口保留，不能绕过HA/发行门禁；未生产执行 |
| R44 内容审计/TypeSafe/Jev/安全提示/证据/用户恢复 | PK026/027/041/043/046；风险等级、按组范围、扣费放行与真实扣款回执保持 | `backend/internal/service/content_moderation.go`、`backend/internal/handler/admin/content_moderation_handler.go` | 原PK风险动作、扣费回执与上游审计共存；cyber匿名scope恢复与定向通过 |
| R45 IP风控/匿名鉴权失败/注册IP/邮箱别名 | PK-066；可信代理、Turnstile、现有认证与管理员豁免；新默认不得阻断正常用户 | `backend/internal/service/invalid_auth_abuse_limiter.go`、`backend/internal/handler/admin/ip_risk_handler.go` | 可信IP、无效鉴权/注册/邮箱规则入口保留；未自动扩大生产封禁 |
| R46 Key风险巡检/告知/确认 | PK-067；管理端与用户只读确认隔离，多实例worker通知去重 | `backend/internal/handler/admin/ip_audit_handler.go`、`backend/internal/handler/admin/key_risk_handler.go` | 管理巡检与本人确认路由分权；未真实巡检通知/批改Key |
| R47 请求惩罚与个人安全记录 | PK-068；与PK027自动禁用/PK043风险动作不重复处罚/扣费 | `backend/internal/service/security_penalty.go`、`backend/internal/server/middleware/security_penalty.go` | 处罚/个人记录源码核对，保持同请求去重与PK风险合同；源码迁移隔离PG已过，正式制品验收待完成 |
| R48 BEpusdt链上支付/扫码/扫链依赖 | PK-069；新增支付渠道，保留PK022币安账务；事件签名、金额精度/幂等、后台单活 | `backend/internal/service/payment_crypto_checkout.go`、`backend/internal/handler/admin/payment_bepusdt.go` | 隔离真实PostgreSQL多API订单/钱包/配置、断锁join/重取与稳定token通过；无真实扫链 |
| R49 EasyPay确认恢复/订阅续期/其他支付 | 现有支付行为保留，恢复不重复到账/续期，PK022-025独立 | `backend/internal/service/payment_amounts.go`、`backend/internal/handler/admin/payment_bepusdt.go` | EasyPay/续期/退款与币安路径保留；未真实付款，源码新金融迁移隔离PG已过，正式制品验收待完成 |
| R50 抽奖与邀请奖励 | PK-070；新增营销，余额账本幂等和用户隔离，默认不自动发奖 | `backend/internal/service/lottery.go`、`backend/internal/handler/lottery_handler.go` | 抽奖/邀请/退款恢复入口保留；未发生产奖励，源码迁移隔离PG已过，正式制品金融门禁待完成 |
| R51 VIP自动授信/恢复/outbox | PK-071；新增资金操作显式配置，PK003真实余额/配额及PK046回执不可旁路 | `backend/internal/service/auto_credit.go`、`backend/internal/handler/admin/auto_credit_handler.go` | 授信/恢复/outbox入口保留，管理员明确配置；未真实授信，源码迁移隔离PG已过，正式制品金融门禁待完成 |
| R52 余额耗尽预测与提醒 | PK-072；预测不改账单；复用SMTP/通知预算并跨节点去重 | `backend/internal/service/balance_forecast_service.go`、`backend/internal/balanceforecast/delivery.go` | 预测/提醒入口、设置与worker归属保留；未真实发信，不改账单 |
| R53 独立上游渠道/绑定/利润运营/成本探测 | PK-073；PK004原始/客户统计和PK046追加费用不同维度，不能混账 | `backend/internal/service/account_cost.go`、`backend/internal/handler/admin/account_new_api_authorization.go` | 上游渠道/成本/利润入口齐全；前端/handler选择检查，未真实成本探测 |
| R54 渠道运营/定价导入导出/账号模型同步 | PK-073；保留PK定价/倍率/范围与现有渠道，导入严格校验 | `backend/internal/service/channel_operations.go`、`backend/internal/handler/admin/channel_operations.go` | 定价导入导出/模型同步与PK倍率分列；入口和选择前端检查，无生产导入 |
| R55 支持工单/用户和管理员会话 | PK-074；新增，用户隔离/附件/权限，保留私库品牌 | `backend/internal/service/setting_support_ticket.go`、`backend/internal/handler/support_ticket_handler.go` | 用户/管理员工单与附件入口保留；权限和前端接线复核，无生产消息 |
| R56 Serverless地区到gateway路由 | PK-075；默认关闭，共享DB/Redis，owner失败拒绝；保留PK047与入口带宽事实 | `backend/internal/service/serverless.go`、`backend/internal/handler/admin/serverless_handler.go` | Serverless地区路由默认关闭；代理/区域定向，未生产启用 |
| R57 独立区域调度/鉴权/并发槽位/资金账本 | PK-075；实验/预览，不忽略；显式配置、费用上限、migration epoch、outbox、对账，不能默认开放 | `backend/internal/service/regional_auth_sync.go`、`backend/internal/regional/contracts.go` | 区域鉴权/计量/outbox/epoch保留；区域定向通过，源码迁移隔离PG已过，跨区域运行未验收 |
| R58 runtime角色/生命周期/快照发布/readiness | PK018/019/020/047；统一API/worker单活、writer门控、排空/ready、持久统计追赶 | `backend/internal/service/astra_gateway_runtime.go`、`backend/internal/handler/admin/setting_handler_runtime.go` | 统一API/worker角色、排空及ready源码适配；普通Wire cleanup复验通过，HA新制品门禁待完成 |
| R59 共享调度/并发/热路径和模型目录 | PK-058；与PK045严格优先和PK044容量/PK042组路由逐入口适配 | `backend/internal/service/account_authorization_scheduler.go`、`backend/internal/handler/admin/account_concurrency_summary.go` | 共享并发/模型目录/快照与PK代理/严格优先/有序组交叉定向；无生产容量结论 |
| R60 私库发行/更新/部署与外置工具 | PK013-021/047；不恢复公开来源/GitHub Actions发布，外置browser和Node/Codex工具按角色配置 | `backend/internal/service/update_service.go`、`backend/internal/repository/github_release_service.go` | 私库更新/发行保留，README入口纠正；Unix更新器语义尚有平台补验项 |
| R61 依赖/锁文件/审计例外/本地第三方修补 | 导入新依赖及修补并保留私库安全门禁；Go replace的实际版本必须核，不只看require | `.github/audit-exceptions.yml`、`backend/.golangci.yml` | 依赖/锁文件及本地补丁已清点；官方前端audit为0 critical/0 high/12 moderate/2 low，旧例外未匹配且未扩大，非风险清零 |
| R62 SQL/Ent schema与生成代码 | 保持324份私库历史SQL字节不变；118新增含3份异名同内容，不能伪造migration记录 | `backend/ent/schema/account.go` | 324旧迁移字节不变、118新增（含3异名同内容）；隔离PG新装/并发升级/旧FS重启和再升级通过，正式旧二进制回退待执行 |
| R63 私库专属功能与品牌/用户UI | PK-049；PK全部保护；PK029/030已移除历史不恢复；用户明确新展示需求适配私库主题 | `backend/internal/service/account_authorization_actions.go`、`backend/internal/handler/account_authorization_handler.go` | 全部原PK逐项登记，029/030保持移除；品牌、只读测智、外链无令牌边界保留 |
| R64 其他共享核心/前端/文档与测试 | 兜底不是忽略项；每文件保留状态/3方类别/冲突归属，实施时必须清空未核条目 | `backend/internal/service/account.go`、`backend/internal/handler/admin/account_codex_import.go` | 文件全集和残余域均保留清点；前端/handler入口零遗漏不能代替全部业务动态验证 |

## 二次入口复核与有意适配

全量前端及 handler/server 业务源检查：上游 1183、私库 853、当前 1295 文件，两侧缺失均为 0，前端别名 import 无未解析目标。后端 1001 个上游 route/group 词法项均保留，52 个路由注册函数均有调用。TypeScript AST 复核上游 92 路由/65 菜单节点、私库 72/47、当前 102/73，双方路由零缺失；所有 `/admin/` 组件路由都有管理员或账号管理权限元数据。这些计数只说明入口清点结果，不能推导所有后端行为已经执行。

- 私库 `group_ids` 有序 JSON 仍是 Key 路由事实，上游关联表只作事务镜像；最多 20 组、首项旧字段、NULL/显式空数组和实际组审计保持。新 Key 并发/等待、模型映射/强度、长上下文策略同时适配 HTTP 与 WS 每轮。
- 两个上游审计菜单配置并入 PK AuditCenter；运营页保留为 `/admin/risk-control/operations` 与 `/admin/prompt-audit/operations`。旧 `?tab=ip` 在权限守卫前跳转到 IP 审计，其他旧配置链接进入统一审计中心。原运营功能未被配置页覆盖。
- 新用户测智质量 API 使用 JWT、限流与公开展示范围，只有 GET 结果入口；计划修改与手动测试仍是管理员权限。Observer 强制本人 ID；普通用户 first_token_ms 保留真实下游首响应投影，不暴露管理员上游/排队细节。
- 外部 iframe 不追加 JWT/user_id，外链 `src_url` 的查询/哈希剥离；同源内嵌认证仅显式 opt-in。自定义菜单与外部站点故障不合并成应用功能已通过的结论。
- 上游明确会话身份/精确指纹保护与 PK 匿名 transcript/scope 保护融合。明确身份不激活匿名 scope，但仍保留精确指纹；匿名请求保留历史前缀、后续轮次和活跃 scope 上限。HTTP、WS 首轮/后续轮及记账前写入统一走 WithScope，异步前冻结 Gin 上下文中的身份数据。
- Chat→Messages 直连仅有 reasoning 时，buffer 返回 502，stream 返回 error 且不伪造 message_stop/可见答案；原 usage 仍返回记账，不重试已发上游，不改变首字扫描。见 [声明兼容复核](PK_DECLARATION_COMPAT_PK61_REVIEW.md)。
- HTTP 错误率从进程本地 ring 改为有界异步共享 Redis；单活 worker evaluator 能读取其他 API 的样本。partial 窗口可评估已有样本，读取失败显示 unavailable，不回退本地 0%。质量 5xx 信号本来已有共享 Redis，保留该队列，没有另造相互隔离的本地 tracker。
- BEpusdt HA 共享 PostgreSQL 配置/订单，API 不扫链；独立会话锁丢失后 cancel/join 再取锁，连接池禁止透明重连造成双 scanner。既有 SQLite/WAL 拒绝静默迁移或分叉；普通 SQLite/外接支付保留。

旧内部声明的替代或恢复不能只靠名称存在判断。完整的 34 个私库声明及 3 个上游声明说明见 [PK_DECLARATION_COMPAT_PK61_REVIEW.md](PK_DECLARATION_COMPAT_PK61_REVIEW.md)。handler 范围关键映射如下：

| 旧声明 | 当前实现与保留行为 |
|---|---|
| `bindOpenAIReasoningEffortPolicyForMessagesRequest` | `prepareOpenAIReasoningEffortPolicyForMessagesRequest`，保留请求/Key/group 强度和显式 ultra，返回错误且只映射一次 |
| `modelListingSource` / `defaultModelIDsForPlatform` | `service.ModelListingSource` / `service.DefaultModelIDsForPlatform`；账号白名单、显式空目录、默认回落与平台合并保持 |
| `mergeModelIDs` | 移至 `service/model_listing.go` 的同名内部函数，保持顺序、去重和空白过滤 |
| `ConcurrencyHelper.withAPIKeySlotFromGin` | 入口明确传 Key ID/limit，`AcquireAPIKeySlotWithWait` 与 `ReserveWSAPIKeySlotWithWait` 完成 HTTP/WS 每轮准入，释放组合只执行一次 |
| `cyberSessionBlockWritePlan` 类型及构造函数 | `MarkCyberSessionBlockedForRequestWithScope`，同步冻结 identity/IP/UA/body 后持久保护 |
| `findBlockedCyberSessionKey` | `findBlockedCyberSessionForRequest` → `FindCyberSessionBlockedForIdentityAndBodyAndScope` |
| `cyberSessionScopeKey` | `service.CyberSessionScopeKey`；可信客户端 IP 与规范化 UA 计算保持 |
| `suppressOpsUpstreamAttributionForLocalModelConfiguration` | `suppressOpsUpstreamAttributionForClientRejection`；本地配置/策略拒绝归为 request/client，清空账号与上游归因 |

二次检查修复了重复 DTO 字段、重复 GroupHandler 字段、重复防蒸馏 GET/PUT 注册造成的 Gin panic，以及构造器/错误写入函数调用适配。旧测试夹具补齐明确账号组、范围、API 返回字段及共享来源文案，业务断言保留。两个重复的上游防蒸馏测试文件经主任务审阅后并入更完整的既有文件，私有证据保留原稿；未以删断言或 skip 隐藏问题。

## 本轮实际定向结果

以下日志保存在 Git 忽略的本机证据目录，只列不含凭据/服务器身份的文件名。测试报告与源快照绑定；跨轮重复场景不合并成虚高总数。源码测试没有使用生产数据、真实授权、真实模型、链上付款或真实通知。

| 范围 | 实际结果 | 证据/说明 |
|---|---|---|
| 前端合并与二审 | 65 文件、1551 项通过，0 失败；原 37 个冲突测试文件均覆盖；类型检查通过 | `frontend-final-results.json`、`frontend-final-source-sha256.json`、`frontend-typecheck-second-review.log` |
| handler/dto/admin/server/middleware/routes | 六包编译通过；391 个去重顶层、800 个含子项 pass 事件，0 fail/skip；原选择 381 项全部执行 | `handler-server-targeted-final.jsonl`、`handler-second-boundaries-attempt2.jsonl`、`handler-server-final-results.json` |
| 服务恢复与质量 | 116 顶层通过；同 Windows 时钟 tick 的夹具修复后 1 项复验通过；contracts 51 项、类别/豁免 11 项通过且相互重叠 | `service-recovery-targeted-attempt2.jsonl`、`quality-recent-plan-retry2.jsonl`、`service-contracts-targeted-attempt1.jsonl`、`security-category-retry1.jsonl` |
| 质量前端补充 | 2 文件 18 项通过；不与前端累计表重复相加 | [质量/worker报告](PK_QUALITY_WORKER_PK61_REVIEW.md) |
| 账号兼容/共享性能 | 账号兼容 15 项与快照版本单项复验；共享统计原4+新4、本地HTTP双writer链路1项、关闭故障1项通过 | [账号兼容报告](PK_ACCOUNT_COMPAT_PK61_REVIEW.md)；`upstreamstats-shared-attempt1.log`、`upstreamstats-shared-chain-attempt1.jsonl`、`upstreamstats-close-failure.log` |
| 匿名 cyber 兼容 | 6 个 PK 场景及上游身份/指纹用例通过；handler 实际写入→再准入已覆盖 | `cyber-scheduler-compat-attempt1.jsonl`、`cyber-compat-retry1.jsonl` |
| reasoning-only 直连 | 27 个顶层通过：apicompat 25、service 2，0 fail/skip | `reasoning-direct-compat-attempt1.jsonl`；[声明兼容报告](PK_DECLARATION_COMPAT_PK61_REVIEW.md) |
| 计费与长上下文 | 首轮20通过/4旧门槛或PlatformUsageAt夹具失败；第二次因缓存沙箱拒绝；第三次4项全过，含500000/500001与旧272001边界 | `runtime-billing-protection-attempt1.jsonl`、`runtime-billing-protection-attempt3.jsonl` |
| 耐久结算/审计与仓储 | durable/audit 9顶层通过，repository billing 23顶层通过 | `durable-audit-cross-attempt1.jsonl`、`repository-billing-targeted-attempt3.jsonl`；不是所有新金融迁移验收 |
| SSE 与 Messages/Key/Kilo | SSE pump 7顶层、Messages/Key/Kilo 7顶层通过 | `sse-pump-merged-attempt1.jsonl`、`messages-key-kilo-cross-attempt2.jsonl` |
| compact/更新器 | 首轮30顶层通过、1旧fixture失败；该项修复后通过；仍有2顶层/3个skip事件 | `compact-update-cross-attempt1.jsonl`、`compact-update-cross-attempt2.jsonl`；Windows缺Unix rename/执行位语义，不能算作通过 |
| 代理/区域仓储 | 首轮9顶层通过、1新fixture缺缓存失败；补齐夹具后该项通过 | `proxy-regional-cross-attempt1.jsonl`、`proxy-regional-cross-attempt2.jsonl` |
| 源码迁移隔离 PostgreSQL | fresh 2.02s、upgrade 2.51s通过，324→442；两新节点并发迁移/旧读写、旧FS重启和新FS再次升级，余额/额度/配置保持；临时测试已私有保全后清理 | `migration-linux-fresh-20261110T165658Z.json`、`migration-linux-upgrade-20261110T165811Z.json` 及日志；首轮Windows经SSH每事务延迟触8分钟失败保留；不是正式旧二进制回退证据 |
| BEpusdt 共享 PG | 隔离真实 PostgreSQL 双API：6唯一订单、钱包保护、配置刷新、断锁join/重取、稳定token和Stop交接通过 | `bepusdt-pg-20261110T161757Z.json`；[实现合同](../backend/internal/bepusdt/README.md)；无真实扫链/支付/生产写入 |
| 普通Wire与构建阶段 | cleanup第二轮通过；本机Linux交叉构建成功，12031份Go源码与Linux镜像副本哈希一致；前端安装/构建已通过一次；依赖override尝试因网络/离线元数据不足撤回，package/lock哈希恢复到该构建快照 | `wire-final-cleanup-attempt2.jsonl`、`linux-cross-final.log`、`frontend-final-install.log`、`frontend-final-build.log`；不是最终封存或十一项制品门禁 |
| HA正式发行准入合同 | 普通production-primary/schema1保持；显式production-ha/schema2绑定review SHA、双API ready、单worker=writer与稳定epoch，且不重启/替换程序/改变运行授权；6项新合同检查通过 | purpose为ha-source-release-admission；实际私有hook仍准备中，旧ordinary已退出且原hook拒绝container_not_healthy，不伪造原guard hash，不等于prepare/check或发行已完成 |
| 发行对应源码门禁 | pk.61起完整同commit源码归档与gzip/tar双SHA强制绑定；原publisher/源码分发/HA准入合计20项通过 | `corresponding-source-independent-review-attempt3.log`；control按实际blob/mode重建完整Git tree，local publisher按同commit且固定行尾配置的git archive原tar再核；初轮失败日志保留，不代表正式同制品已验证或已发布 |
| 新后台启动门补验 | AuditRetention与CodexHarvester构造后等待激活门的定向检查通过 | `activation-final-gap-tests.log`；仅对应启动门边界，非所有后台生产验收 |
| 更新器与排空回执 | 隔离两批23+12项通过，不当作新HA制品已上线 | `updater-isolated-tests-20261110T153241Z.json` |

最终静态复核见 [PK_FINAL_STATIC_PK61_REVIEW.md](PK_FINAL_STATIC_PK61_REVIEW.md)：全局冲突标记 0、Linux/Windows AST 无重复声明、上游新增业务源无遗漏，64/64 功能域有证据索引；当前本轮临时新建测试文件 0，固定上游已有测试保留。官方前端依赖审计真实结果为 0 critical、0 high、12 moderate、2 low，旧例外未匹配且未扩大；兼容 override 尝试因网络/离线元数据不足撤销，package/lock 哈希完整恢复。该结果不称风险清零，也不把未匹配例外当作门禁通过。

所有失败尝试原日志保留，包括第三方依赖/缓存拒绝、构造器与重复字段/路由、旧 API 字段/范围夹具、cyber scope、R18 旧文本断言、计费时间和旧门槛夹具。最终通过不删除失败历史。临时生成的测试文件不入库；已有测试继续保留并扩展。

## 运行故障、资源与浏览器的因果范围

先前生产故障和已实施恢复见 [运行恢复](PK_CLUSTER_RUNTIME_RECOVERY_REVIEW.md)、[资源复核](PK_CLUSTER_FLEET_CAPACITY_REVIEW.md)、[集群更新器](PK_CLUSTER_UPDATER_REVIEW.md)。这些是旧版生产实际结果，不能写成新 pk.61 已部署。

- 渠道“没有流量”与后台统计停滞有关，不能仅凭渠道页面断言计费已停止。先前从原始用量补齐派生统计并对账，未重放扣费；本轮继续保留持久水位追赶与粗桶重算 Go 修复，待新制品部署。
- 定时糖果、巡检及聚合共享后台角色；worker 启动失败/交接预算不足会让多个定时功能同时停滞。已有 supervisor 临时 Docker 超时重试与资源角色分配修复；本轮再次补齐已发探针保存和未发任务交接，禁止用强杀完成升级。
- 授权浏览器报错来源于 HA 运行镜像缺 Chromium/Xvfb，且只读用户环境不能在容器内现场安装。先前独立浏览器运行镜像已完成启动与授权代理预检；本轮发布仍需验证新候选使用登记镜像及外置工具，不关闭只读根或向业务容器授予安装权限。
- 全项目资源优先级、API 流量向较强节点倾斜、数据库限额上调和冷目录迁盘已有旧版实证；存储 writer 切换未完成。已发生过维护未及时中止导致停流，恢复脚本释放 gate 的事故记录保留。新优化不等于已证明 3000 并发，也不把系统盘换成数据盘描述为物理内存增加。
- 生产导航独立热修复当前涉及三个菜单，CDK 页面交互检查已通过；另一外部页面仍由其他维护方处理 503。临时 HTML 桥接清理须等新 API 自然输出菜单，不能把外部 503 写成已修复。

## 剩余发行和上线门禁

下列项目属于当前尚待完成的执行阶段；应由主任务在实际完成后补具体版本、哈希、环境和结果，不能用上面的源码定向代替。

1. 普通 Wire cleanup 复验与本机 Linux 交叉构建已完成；最终 HA overlay 与新制品门禁仍需执行。旧 r4 工具包及旧正式制品不覆写。Unix 更新器 rename/执行位场景需要 Linux 实证。
2. 324 份历史迁移字节不变、118 份新增迁移的源码 fresh/upgrade 已在隔离真实 PostgreSQL 通过，包含两新节点并发迁移、旧读写、旧 FS restart/new reupgrade、余额额度配置保持。仍需对正式同制品完成旧 pk.59 二进制回退、候选 fresh 和关键金融函数门禁；不能伪造迁移记录或把应用回退当成撤销数据库变更。
3. 最终锁文件/Go replace 实际版本、依赖审计、既有例外适用范围与截止日期核对，完成前后端正式构建并绑定源码/产物哈希。
4. 对同一实际制品执行十一项运行时门禁及新增关键支付/统计/HA交叉验收；正式私库 tag/main、附件清单和 SHA256 回读一致。pk.61 必须分发完整同 commit 的 `sub2api_0.2.19-pk.61_source.tar.gz`（git archive 固定前缀，gzip mtime=0），manifest 绑定源码归档与原 tar 双 SHA；完整第三方源码/许可/构建说明包含在内，publisher 使用固定 core.autocrlf=false/core.eol=lf 的 git archive，并从全部目录、blob 和 mode 重建同 commit Git tree，拒绝 export-ignore/export-subst 造成的缩减或改写。Docker context 同时保留 THIRD_PARTY_NOTICES 与 BEpusdt 许可。`deploy/local-release.py` 不构建或部署，不推 GHCR。
5. 新 worker 最长批次约 12 分钟且结果保存预算 30 秒，部署停止预算需至少 15 分钟。此前生产 systemd 仍为 90 秒，必须在滚动前完成明确 drop-in/daemon-reload 并核实有效值；未安装不能只凭候选配置声称满足。
6. 逐节点摘流、在途请求和记账排空、浏览器/共享状态就绪、单 writer 单 worker 与账务/聚合/五分钟糖果持续验证；每节点恢复后再动下一节点，不同时关闭两个 gate，不强杀连接。新金融、采集、通知、安全与区域实验功能不自动开启。

本轮文档同步修正 README 和 `.github/release-tools/README.md` 的公开 Actions、强制 Jenkins 和必有 GHCR 表述，保留上游归属与旧历史记录。本文不记录真实服务器身份、SSH 凭据、专属容器名或私有运行路径。
