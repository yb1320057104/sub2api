# pk.58 下游费用接收开关的凭据状态识别

2026-11-08 定向验证完成，源码交接待提交。目标版本 `v0.2.19-pk.58`，基于私库 main `70b4d85856b861c7314b5e273a0644884e3eaacb`；当前正式 latest 为已完成发布的 pk.57。本轮 `EditAccountModal.spec.ts` 单文件136项测试全部通过，无失败或跳过，类型检查通过，测试源码哈希未变。本轮仅源码交接，版本分支／main／唯一新标签仍待主任务提交推送；不发布更新包、GitHub Release 或 GHCR，不部署或重启生产。

## 问题与代码依据

管理员在下游编辑 OpenAI／Anthropic 的 API Key 或 upstream 账号时，账号已配置 API Key，地址为合法根地址或 `/v1`，开启“接收上游审计追加费用”仍可能显示配置无效并阻止保存。

现有后端DTO通过 `RedactCredentials` 移除 `credentials.api_key` 等敏感原值，只用 `credentials_status.has_api_key` 表示密钥是否存在。这是正常的脱敏返回；编辑框留空通常表示保留服务器已保存的密钥，不能据此判定账号没有Key。

`frontend/src/components/account/EditAccountModal.vue` 的 `auditSettlementConfigValid` 原先只检查本次输入的 `editApiKey` 或旧返回结构中的 `credentials.api_key`，遗漏了 `credentials_status.has_api_key`。在新DTO下两个可见Key字段均可为空，因而错误地认定凭据不完整。该校验同时检查URL与Key，所以通用错误提示容易被理解为 `/v1` 不合法；实际原URL规则已经接受 `/v1`，本版不放宽地址规则。

## 修复规则

只调整账号编辑弹窗的凭据存在性判断并补充对应定向测试。新输入非空Key优先满足本次配置；未输入时优先使用后端明确提供的凭据存在状态，缺失该状态时兼容旧后端的 `credentials.api_key`。

| 本次输入与后端状态 | 启用接收费用时的凭据判断 |
| --- | --- |
| 本次输入非空API Key | 允许按新输入继续校验并保存；URL仍须通过原规则 |
| 本次留空，`has_api_key=true`，原Key字段已脱敏 | 认可已保存凭据，不要求重新输入，也不把原Key补回请求 |
| 本次留空，明确 `has_api_key=false` | 判定缺少凭据，旧 `credentials.api_key` 不能覆盖明确的新状态 |
| 凭据存在状态缺失，旧结构含非空 `credentials.api_key` | 兼容旧后端，认可原有凭据 |
| 新旧结构都没有有效凭据且本次留空 | 继续拒绝启用，不能仅因URL合法而放行 |

该修复不从服务器取回密钥，也不通过日志或错误提示返回密钥。新DTO下留空保留服务器原Key，保存请求不补入已脱敏的Key；管理员主动填写新Key时走原更新路径。旧DTO仍含Key时沿用既有兼容保存行为，不把旧返回结构冒称本版新增加的密钥回传。接收开关仍由管理员显式开启，原有 `extra` 中的其他设置须保留；不自动修改任何账号的持久配置。

URL准入继续限定HTTP／HTTPS、非空主机、根路径或 `/v1`，拒绝嵌入用户名／密码、查询参数、fragment及其他路径。账号类型与平台范围保持；OAuth等不支持的账号不因本次修复获得接收开关。

## 功能登记与账务边界

- 沿用 **PK-046：同款下游审计追加费用结算**，修复其账号接收配置入口。
- 沿用 **PK-021：二开登记与项目约束**，记录本轮范围、实际验证和源码交接状态。

本轮业务源码只涉及 `EditAccountModal.vue` 与对应测试文件，不修改后端DTO、API或数据库。无新增功能ID、迁移或依赖，不改首字计时。

追加费用仍遵循原PK-046显式协商与真实扣款回执合同：按原请求的下游本地用户归属处理金额，保留余额、幂等及终态约束。此界面修复不更改金额、倍率、协议、实际扣款条件或用户映射，不开启后台自动扣费、不重新导入旧回执、不追补历史费用。原生sub2api的标准接口兼容边界保持。

## 验证与交接状态

主任务已实际执行本轮组件回归与类型检查，运行脚本返回 `PK58_SETTLEMENT_UI_CHECKS_PASSED`。核对 `.runtime/pk58-ui-checks.json`、原始日志及两份当前源码SHA256一致，`source_unchanged=true`，两项退出码均为0。

实际命令为 `pnpm.cmd exec vitest run src/components/account/__tests__/EditAccountModal.spec.ts --reporter=verbose` 和 `pnpm.cmd run typecheck`，后者执行 `vue-tsc --noEmit`。Vitest报告1个文件、136项通过／136项总数，无失败或跳过；这是该文件全部现有及新增场景的合计，不将136项全部算作本次新增用例。组件测试覆盖OpenAI／Anthropic与apikey／upstream四种组合；测试使用模拟管理API，没有生产或真实上游请求。类型检查通过不等于正式前端构建或制品验收。

| 核对场景 | 实际结果 |
| --- | --- |
| 新DTO已脱敏、`has_api_key=true`、根地址、`/`、`/v1`及`/v1/`能启用并保存 | 通过；新DTO保存请求不包含api_key |
| 明确false不被旧Key字段覆盖；本次输入非空新Key仍可满足凭据条件 | 通过 |
| 状态缺失时兼容旧Key，无Key或仅空白Key时继续拒绝 | 通过 |
| 原URL拒绝规则、支持账号范围及关闭接收行为保持 | 通过 |
| 保存接收开关保留其他extra设置；已开启的脱敏账号再次保存不误关闭 | 通过 |
| 账号编辑组件全部定向回归 | 1文件136项通过，无失败／跳过，exit=0 |
| 前端类型检查 | `vue-tsc --noEmit`通过，exit=0 |
| 正式源码版本分支／main／唯一新标签交接 | 待主任务提交推送 |
| 更新包、Release、GHCR、生产部署／重启 | 本轮暂不执行 |

本轮证据绑定如下；运行前后两份源码摘要一致，文档补录没有改变测试输入。

| 证据／源码 | SHA256 |
| --- | --- |
| `.runtime/pk58-settlement-ui-checks.log` | `3f93ffd5f25b11d7c5ddc92bb769980ec8dd7b954042d40fa6491cc8a08301ce` |
| `.runtime/pk58-typecheck.log` | `cde80afb716f10d0ddec5ddc6d1ed8a846e4fdd737c4f6425516e3d474299796` |
| `frontend/src/components/account/EditAccountModal.vue` | `5c67c0d8d4bced25001c90837ed7b477a40adb107ab9acd57949f3a862a3e4ea` |
| `frontend/src/components/account/__tests__/EditAccountModal.spec.ts` | `170c72550e0add9a314b9975c45872c59b629b511a4749fd548f203c5296e5d8` |

后续按实际提交推送结果补录源码交接身份。旧pk.57及此前正式标签、制品、发布和生产启动许可记录保持，不因本轮本地测试通过而宣称线上费用接收已经恢复。

源码交接前已只读确认私库身份、远端main仍为上述基线、pk.58分支与标签未占用、对应Release及在途PR不存在，latest仍为pk.57。Packages查询返回HTTP 404，不能据此证明私有GHCR镜像版本不存在；本轮不构建或写入GHCR，不宣称完成镜像占用验证。未来若发布镜像仍须使用具备相应读取权限的凭据重新核对，不能覆盖既有版本。只读查询证据保存在本机忽略目录 `.runtime/pk58-source-slot.json`。
