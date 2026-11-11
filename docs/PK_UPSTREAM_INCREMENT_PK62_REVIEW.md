# pk.62 上游截止范围与增量复核

本轮最终上游输入固定为 `8545bf9ffac5a870ade18fbe7c4b61a48755c8db`（production，2.20.4，2026-11-11 00:47:50 +08:00）。它在前次固定 `d42bb4fc4b3eef9821994b4ad170d4eb881eca95`（2.20.3，2026-11-10 21:56:15 +08:00）之后新增10提交、44文件变更。pk.61正式源码标签保持原提交 `79fa0fc375c52467cce3aecb47eba2085802122c`；新输入真实合并到唯一 `v0.2.19-pk.62` 分支，不移动旧标签，不把前次通过改名成新证据。

## 增量与兼容

1. 托管BPS的Responses选号按实际入口连接能力判断，与发送前投影一致。普通Responses/native compaction可选择原生BPS；旧compact、Chat等不支持入口提前排除。保留PK严格优先级、利润、组路由、代理及首字/记账上下文。
2. OAuth429永久停调度增加七天额度严格大于阈值的守卫，默认98%，五小时用量不参与。当前响应头优先，未知/无效/陈旧快照不永久关号；2FA轮换独立，BPS各429路径带完整头。配置/API/界面和既有场景均同步。
3. 新增API Key备用池，登记PK-076。默认关闭，OAuth非空池全部不可用或有效OpenAI SLA/健康指标不足时，按明确名单及配置revision原子开启账号调度；不清账号其他状态，不自动关闭恢复后的备用账号。
4. HA适配统一使用`sharedJobsEnabled`，取消上游另一个固定false的标记。reader构造只通知已存在的循环；激活唤醒，关闭撤销权限、取消并等待，迟到回调不能复活。管理API读取共享状态和配置revision/新鲜度，不启动后台任务，不误报进程内空状态为健康。
5. 独立复核发现并修复pk.61合并缺口：Responses结尾存在无条件与有条件两次防蒸馏采样。去除无条件调用，保留非审计拦截时单次采样，与Messages/WS一致。原request ID去重未变，不重算历史样本或费用。

本增量没有SQL迁移和Go/前端依赖变更，沿用442份迁移及现有锁文件；正式制品仍必须按本版真实tree重验。保留上游新增测试，未新增本轮独立临时测试文件。

## 验证记录

本轮 Windows/Go 定向执行 service、handler、admin、repository 四包，30个顶层测试通过、0失败/0跳过（含子项163个pass事件，不把子项与顶层重复累计）。覆盖备用池触发/名单/无样本、共享双API状态/过期/取消、HA激活与取消join、429周阈值、托管BPS选号、利润和审计交叉。`incremental-go-attempt1.jsonl` 为沙箱缓存访问拒绝，未执行成功；经批准使用现有缓存后的 `incremental-go-attempt2.jsonl` 为实际成功记录。

前端 AutoConfigView / OAuthDegradationActions 两文件90项通过、0失败/0跳过，证据 `incremental-frontend.json` 与日志。正式前端构建另由主任务执行并按本版输入哈希留证；Go Provider函数签名和生成图未变，不制造无意义wire_gen改动。现有包锁与pk.61审计快照相同，依赖审计依据 `frontend-audit-reuse.json` 真实复用，不声称重新扫描。

repository 的 `TestAPIKeyFallbackChecksSavedPolicyAndAccountEligibility` 已在网络隔离 DinD 内以真实 PostgreSQL/Redis 完成：应用442份迁移，1个顶层、9个子项通过，0失败/0跳过，退出0，隔离资源清理通过，未访问生产数据库。证据目录名 `pk62-sql-cas-b8bb6b353741` 的 `receipt.private.json` 绑定最终提交 `fdec871f36b965dfc272b46bd12eca76ba448300` 和 tree `ae82fc643023fd26667af0e8f9aa1b8de05a23e4`；测试程序在最终提交前编译，三份相关源码的SHA256已逐字节匹配该提交Git blob，`test.stdout.log` SHA256为 `f3b5b3b955eabf4c1a2380ada82afc692ce6aa746849b3477499e93bd1beb16c`，本次补录再次核对一致。该专项不是HA恢复或生产上线证明。正式普通/HA程序、十一项同制品、迁移/回退、私库发行与滚动上线以各自实际回执为准；不因本专项通过宣称这些阶段完成。


## 44文件处置与原64域边界

本次44文件均已纳入实际三方合并或PK兼容适配，无遗漏新增业务文件；上游新增测试保留。新增共享状态生产文件、两项既有测试扩展和激活生命周期测试属于本次HA适配，不另建临时测试文件。下面“相同”按LF归一比较；不是冒称工作区逐字节等于Git blob，最终源码归档仍以实际Git tree核验。

| 变化文件 | 处置 |
| --- | --- |
| `.github/scripts/model-billing-groups-browser.mjs` | 保留新上游内容 |
| `.github/scripts/oauth-degradation-browser.mjs` | 保留新上游内容 |
| `.github/scripts/oauth-initial-model-selection-browser.mjs` | 保留新上游内容 |
| `.github/scripts/oauth-initial-ws-preview.mjs` | 保留新上游内容 |
| `.github/scripts/oauth-initial-ws-screenshot.mjs` | 保留新上游内容 |
| `backend/cmd/server/VERSION` | 保留上游增量并兼容PK边界 |
| `backend/internal/handler/admin/account_auto_config_429_test.go` | 保留新上游内容 |
| `backend/internal/handler/admin/account_auto_config_handler.go` | 保留上游增量并兼容PK边界 |
| `backend/internal/handler/openai_gateway_handler.go` | 保留上游增量并兼容PK边界 |
| `backend/internal/handler/openai_managed_bps_scheduling_test.go` | 保留新上游内容 |
| `backend/internal/repository/account_apikey_fallback.go` | 保留新上游内容 |
| `backend/internal/repository/account_apikey_fallback_integration_test.go` | 保留新上游内容 |
| `backend/internal/repository/account_apikey_fallback_test.go` | 保留新上游内容 |
| `backend/internal/repository/ops_repo_window_stats.go` | 保留新上游内容 |
| `backend/internal/service/account_ops.go` | 保留上游增量并兼容PK边界 |
| `backend/internal/service/oauth_auto_config.go` | 保留新上游内容 |
| `backend/internal/service/oauth_auto_config_429_test.go` | 保留新上游内容 |
| `backend/internal/service/openai_account_runtime_block_fastpath.go` | 保留新上游内容 |
| `backend/internal/service/openai_account_scheduler.go` | 保留上游增量并兼容PK边界 |
| `backend/internal/service/openai_apikey_fallback.go` | 保留上游增量并兼容PK边界 |
| `backend/internal/service/openai_apikey_fallback_eligibility.go` | 保留新上游内容 |
| `backend/internal/service/openai_apikey_fallback_eligibility_test.go` | 保留新上游内容 |
| `backend/internal/service/openai_apikey_fallback_test.go` | 保留上游增量并兼容PK边界 |
| `backend/internal/service/openai_connection_scheduling.go` | 保留新上游内容 |
| `backend/internal/service/openai_connection_scheduling_test.go` | 保留新上游内容 |
| `backend/internal/service/openai_disable_scheduling_429.go` | 保留新上游内容 |
| `backend/internal/service/openai_disable_scheduling_429_weekly_test.go` | 保留新上游内容 |
| `backend/internal/service/openai_excel_bps.go` | 保留新上游内容 |
| `backend/internal/service/openai_excel_bps_images.go` | 保留新上游内容 |
| `backend/internal/service/openai_excel_bps_ratelimit.go` | 保留新上游内容 |
| `backend/internal/service/openai_gateway_scheduling.go` | 保留上游增量并兼容PK边界 |
| `backend/internal/service/ops_port.go` | 保留新上游内容 |
| `backend/internal/service/ratelimit_service.go` | 保留新上游内容 |
| `backend/internal/service/ratelimit_service_disable_scheduling_429_test.go` | 保留新上游内容 |
| `backend/internal/service/wire.go` | 保留上游增量并兼容PK边界 |
| `frontend/src/api/admin/autoConfig.ts` | 保留新上游内容 |
| `frontend/src/components/admin/operations/OpenAIAPIKeyFallbackCard.vue` | 保留上游增量并兼容PK边界 |
| `frontend/src/constants/newFeatureNotices.ts` | 保留新上游内容 |
| `frontend/src/i18n/locales/en/autoConfig.ts` | 保留上游增量并兼容PK边界 |
| `frontend/src/i18n/locales/zh/autoConfig.ts` | 保留上游增量并兼容PK边界 |
| `frontend/src/utils/openaiAPIKeyFallback.ts` | 保留上游增量并兼容PK边界 |
| `frontend/src/views/admin/AutoConfigView.vue` | 保留新上游内容 |
| `frontend/src/views/admin/__tests__/AutoConfigView.spec.ts` | 保留新上游内容 |
| `frontend/src/views/admin/__tests__/OAuthDegradationActions.spec.ts` | 保留新上游内容 |

原d42固定范围的64功能域、声明替代链、许可与静态入口证据沿用 [pk.61综合复核](PK_UPSTREAM_INTEGRATION_PK61_REVIEW.md)、[最终静态记录](PK_FINAL_STATIC_PK61_REVIEW.md) 与 [声明兼容复核](PK_DECLARATION_COMPAT_PK61_REVIEW.md) 的真实范围，不称这些测试在pk.62全量重跑。本轮对交叉的PK-047/048/050/058/061和新PK-076逐项适配并定向补验；以前复核未发现、此次发现的Responses无条件重复采样缺口已明确披露并修正，不宣称此前审阅绝无缺陷。

本轮最终上游截止为8545bf9f；冻结之后的新提交不自动纳入，不使用“与移动production永远完全一致”的表述。上游版本号2.20.4与私库发行版本0.2.19-pk.62分别记录，后端VERSION保留0.2.19，正式PK版本由受控构建ldflags注入。pk.61源码标签及历史失败/构建回执不移动、不覆盖。
