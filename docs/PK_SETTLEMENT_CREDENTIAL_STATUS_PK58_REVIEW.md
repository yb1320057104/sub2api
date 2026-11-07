# pk.58 下游费用接收开关的凭据状态识别

## 2026-11-08 最终发行及启动许可核对

用户后续授权发布，本节替代下文‘源码交接待提交／暂不发布’的阶段状态。正式Release已完成并核对；仅main补录结果，正式版本分支、标签对象、源码tree和封存制品保持不变。

正式commit `00af06c1e9a610cb6a59a2fbfb633ce003c0468a`，tree `84c0c3a49ddd87099d3eae6db9d41743f0f659ba`，annotated tag对象 `6d73cec682530a268f2801e0c896acb16247d029`；package_id `1794079458-00af06c1e9a6`，二进制SHA256 `094ac15f97a6ccbd208815cabd598c5854862a05d19cc5838693e518d560d564`，镜像ID `sha256:43b041b73d03f27c48f67340cc91acc6109e4b9fe4d66963400e723a70a95865`。私库 [v0.2.19-pk.58](https://github.com/yb1320057104/sub2api/releases/tag/v0.2.19-pk.58) 在发布核对时为latest，9项资产与本地封存的名称／长度／SHA256一致。

前端本轮重新构建，正式源码相对pk.57只改账号编辑弹窗及其测试两份文件；后端、迁移和依赖不变。原始组件日志为1文件136项全部通过、无失败／跳过；136项包含原有与新增场景，不能全部算作新增用例。另行 `vue-tsc --noEmit` 通过，前后源码摘要相同。管理API使用模拟，没有实际下游自动扣款验收。

前后端正式构建均绑定本版commit／tree；Go build-info为 `vcs.modified=false`。前端i18n 3项、正式类型检查及Vite构建通过：1219模块，54.38秒，198项产物。所有数量来自本版原始证据，没有挪用pk.57构建或测试结果。

隔离升级／回退运行基线来自本轮实际观测，为 `0.2.19-pk.57`、commit `211da2227f502a118d2df8751cfa1dd5522f622f`、binary SHA256 `29347934effe0b2c0a2e510dc04f3de59f2851ce49d798ee7b25a9678db3654e`；不把最新Release等同于生产运行版本。324份迁移及校验和保留，无新增迁移。金融函数由本版二进制安装后在隔离数据库检查；审计计数断言执行正式源SQL，不冒称调用了编译后的repository Go方法。隔离daemon及临时资源清理通过。

本轮首次启动许可只读检查因旧epoch返回 `production_epoch_changed`，原拒绝证据完整保留。后续只读观测确认生产在本轮发布操作前已运行正式pk.57；容器StartedAt为 `2026-11-07T18:46:34.210048036Z`、RestartCount为1，原容器／镜像／seed保持且healthy。在核对后仅更新本地预期epoch绑定，未把旧拒绝改为通过、未因此修改或重启生产。原拒绝SHA256 `808a18a788d1713d85a551a10581bc667255c9e8e44b1958c6021279c31bbde8`，新只读检查SHA256 `e9d1e7b6d289400dedad5af7a98d76630c35d54348edd04f6e841ba90fdeb7c9`。该既有重启不计为本轮发布执行的部署或重启。

发布流程已prepare并check生产启动许可，实际回执绑定本版manifest／commit／tree／package／binary；核对时healthy且epoch未变。宿主及容器守卫SHA256一致为 `6ea6fb65838905a93fa7ff586155a1b9eb86d7d250057adcdd8f1ea08eaadf23`；receipt SHA256 `a035ec0e9d5487be8fc673b94a0499d739a2e0f6d4f227316e88031df12d8d72`，journal SHA256 `faba3918324801c245caef484d85b18f3b1d139e6594fc7cf22daa783e60bc1b`，epoch证据SHA256 `a45e097266f2bb6132ef222776e979d0c672fd0023b0923a924728d514af6793`，规范JSON回执SHA256 `07989b08ca56bdf7b95a4ee2509ae986d37fec7270401a3b02288da5ffdde354`。发布流程确实更新了外部启动许可／回执，未替换应用二进制、未重启应用、未授HA仲裁权；不把许可准备等同于pk.58已部署，也不宣称HA监测或集群已恢复。

按用户同期交接，本轮保留新增主入口、Nginx分流及应用健康检测服务，不改原有端口归属。许可工具写入范围仅覆盖已钉住的主节点守卫及回执，不连接副节点或自动授予其pk.57／pk.58许可。发布检查不验证新入口链路或SQL／Redis探针语义，不能替代另一会话的路由验收。

本版修复已保存Key的存在性判断，不改变URL准入、实际扣款、倍率、费用归属、协议协商或幂等规则，不自动开启任何账号接收开关，不追扣历史费用。新DTO留空保留原Key且不回传已脱敏Key；旧DTO仍含Key时沿用既有兼容保存行为。真实生产费用接收仍需用户实际升级并按原规则开启接收配置。

本轮未发布GHCR，不执行真实模型／账号授权／币安请求，不运行全套源码回归。源码占位检查的Packages HTTP 404只表示未验证镜像占用，不能证明版本不存在；下文原始说明原样保留。

官方生产依赖审计：0 critical／2 high／12 moderate／2 low；审计exit=1，既有例外门禁exit=0。本版没有新增依赖，也没有将既有漏洞报告说成无漏洞。

| 本轮检查 | 实际通过数量 |
| --- | --- |
| 账号编辑组件 | 1文件136项，无失败／跳过 |
| 单独类型检查 | exit=0 |
| 同制品运行时 | 11 |
| 升级回退 | 7 |
| 多代理API | 7 |
| IP分组API | 7 |
| 模型目录API | 7 |
| 本版安装金融函数 | 8 |
| 正式源审计SQL断言 | 6 |

| Release资产 | 字节 | SHA256 |
| --- | ---: | --- |
| `artifact-validation.json` | 3797 | `8b58acfc85c7708bb158c3a7fc380688c5eb093bd375712a78df0cc021ba2d59` |
| `checksums.txt` | 106 | `9a1521fc40b2670a74333831d5971a9758c88eb9cc65af960f2725190c877567` |
| `isolated-smoke.json` | 84073 | `c2c756348f232f1b5965a410c94bada5b74e622d98603e6c8cf1e4fcb1291f6b` |
| `local-build.json` | 165989 | `f7e9a963d1395352fecd8f9cb4629c40b862c0cbf493d856b647f93ef759b9a4` |
| `manifest.json` | 1282 | `5d0f121c2248200a0b6f984a0e1d9ca7006dbe97c937be6de5d709f14543266c` |
| `migrations-validation.json` | 43893 | `deebe192eaaf0258ad16c601dc623142557db0bb402b8e755091194e002295fe` |
| `review.json` | 1309 | `14471c0bfe014196b04fb1afea8219955dc3758d2117694df171df96322316ed` |
| `sub2api_0.2.19-pk.58_linux_amd64.docker.tar.gz` | 50989519 | `3d149339a315d403e4a99834484d5e5db234e3d8f936ef3518585a09964c14c0` |
| `sub2api_0.2.19-pk.58_linux_amd64.tar.gz` | 40549212 | `4e41c0db2a2e7e3ee4404be9c0f5d5e387a112763e050dd355dd0820886f16d9` |

| 本地本轮证据 | SHA256 |
| --- | --- |
| `.runtime/pk58-publication.json` | `1bad798040519dfc5b4d45d1198c961f3578aff9891c2ae85f95efd3809e5fd6` |
| `.runtime/pk58-source-slot.json` | `704da813a0c45559fa652600bedd629b8c92ca76a005e778e00b483907846636` |
| `.runtime/pk58-ui-checks.json` | `515005ba4851d052b6a65763c98ce7223538714ca8d407be7a0a15ff50bf6982` |
| `.runtime/pk58-targeted-checks.json` | `63834e10cc4d5d30adfc0403a4fb058c85500dac1c45b6b6fabab6d92421053d` |
| `.runtime/pk58-frontend-build.json` | `79c0a3dcc501e48087ff2e4fd6c74aed3031301841aaeff19a18de918c8d4196` |
| `.runtime/pk58-local-build.json` | `f7e9a963d1395352fecd8f9cb4629c40b862c0cbf493d856b647f93ef759b9a4` |
| `.runtime/pk58-stage-metadata.json` | `084347396695667eadf8fcc90742f789b78c2284175da422019976a43eb6b85e` |

2026-11-08 定向验证及源码交接完成。版本 `v0.2.19-pk.58`，基于私库 main `70b4d85856b861c7314b5e273a0644884e3eaacb`；当前正式 latest 为已完成发布的 pk.57。本轮 `EditAccountModal.spec.ts` 单文件136项测试全部通过，无失败或跳过，类型检查通过，测试源码哈希未变。版本分支／main／唯一新标签已原子推送并核对身份。本轮仅源码交接，不发布更新包、GitHub Release 或 GHCR，不部署或重启生产。

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
| 正式源码版本分支／main／唯一新标签交接 | 已原子推送并从远端复核；剥离附注标签后提交一致，属于main历史 |
| 更新包、Release、GHCR、生产部署／重启 | 本轮暂不执行 |

本轮证据绑定如下；运行前后两份源码摘要一致，文档补录没有改变测试输入。

| 证据／源码 | SHA256 |
| --- | --- |
| `.runtime/pk58-settlement-ui-checks.log` | `3f93ffd5f25b11d7c5ddc92bb769980ec8dd7b954042d40fa6491cc8a08301ce` |
| `.runtime/pk58-typecheck.log` | `cde80afb716f10d0ddec5ddc6d1ed8a846e4fdd737c4f6425516e3d474299796` |
| `frontend/src/components/account/EditAccountModal.vue` | `5c67c0d8d4bced25001c90837ed7b477a40adb107ab9acd57949f3a862a3e4ea` |
| `frontend/src/components/account/__tests__/EditAccountModal.spec.ts` | `170c72550e0add9a314b9975c45872c59b629b511a4749fd548f203c5296e5d8` |

正式源码提交为 `00af06c1e9a610cb6a59a2fbfb633ce003c0468a`，tree为 `84c0c3a49ddd87099d3eae6db9d41743f0f659ba`，附注标签对象为 `6d73cec682530a268f2801e0c896acb16247d029`。原子推送后，远端main、版本分支及剥离附注标签后的提交均为该正式提交，`merge-base --is-ancestor`通过；交接后两份源码SHA256仍与本轮测试证据一致。最终结果只在main补录，不移动正式版本分支或标签。旧pk.57及此前正式标签、制品、发布和生产启动许可记录保持，不因本轮本地测试通过而宣称线上费用接收已经恢复。

源码交接前已只读确认私库身份、远端main仍为上述基线、pk.58分支与标签未占用、对应Release及在途PR不存在，latest仍为pk.57。Packages查询返回HTTP 404，不能据此证明私有GHCR镜像版本不存在；本轮不构建或写入GHCR，不宣称完成镜像占用验证。未来若发布镜像仍须使用具备相应读取权限的凭据重新核对，不能覆盖既有版本。只读查询证据保存在本机忽略目录 `.runtime/pk58-source-slot.json`。
