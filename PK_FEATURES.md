# PK出版二开功能清单

2026-11-02 pk.42 发布方式追加：用户确认当前没有 Jenkins，并要求采用其他构建方式继续完成生产更新源迁移。本轮沿用已使用过的本机前后端构建、服务器隔离 Docker 同制品十一项验收、私库 Release 归档发布和运维部署流程；该授权替代本轮此前“仅静态/仅 Jenkins/用户点击”的限制，不自动执行全套源码回归或真实账号/模型请求。现有 GitHub 授权无 packages scope，本轮仅发布私库 Release 的更新包和 Docker 镜像归档，服务器 docker load，不宣称 GHCR 已发布，不降低原 Jenkins 的私有包门禁。仍保留新私库、正式标签/main 归属、未占用版本、SHA256、同一二进制与完整制品证据；部署保留现有 CF/token/加密密钥，先排空并备份。构建和上线结果仅按实际执行记录填写，详见 docs/PK_REPOSITORY_MIGRATION_PK42_REVIEW.md。

2026-11-02 v0.2.19-pk.42 追加：按用户指定迁移正式来源至私有 `yb1320057104/sub2api`，扩展PK-013～017/020/021，无新增ID。更新服务、发行工具、可信Jenkins SCM、镜像和安装指引同步新库；旧封存只以原repository/profile/证据验证，禁止改身份重发。旧标签对象保留，生产更新源需安装含新固定源的程序才能生效。详见docs/PK_REPOSITORY_MIGRATION_PK42_REVIEW.md。

2026-11-02 v0.2.19-pk.42：扩展PK-019/021/034/035/036，账号糖果测试改为按来源分组的有序规则，合格可选多个目标、不合格选单目标，无规则不测试；授权增加独立保存的测智开关并共用同一规则，保留任务快照和全部原生准入。模型下拉补齐内置及账号已配置模型，可手填ID，仍为1～8个模型。完整多组转移结果单独保存，历史不回填；功能初稿只作静态核对；后续构建验收以文首流程及实际报告为准。详见docs/PK_CANDY_GROUP_RULES_PK42_REVIEW.md。

2026-11-02 v0.2.19-pk.41：沿用PK-031/036/021，账号授权工作台现有“授权后的测智关卡”蓝色卡片移至左侧账号输入框下方，右侧保留参数、分组及提交按钮。文案、模型绑定、样式及业务逻辑不变，无新增ID；仅静态核对，未构建/测试或部署。

2026-11-02 v0.2.19-pk.40：扩展PK-031/032/035/036/021，智商测试/雷达/账号授权删除内容区重复标题，保留顶栏及移动端标题，原按钮移至页签工具栏；SVG卡片支持鼠标/键盘放大，复用同一安全预览和已加载作品，局部Tab焦点圈定。控制台及公开首页同步，无新请求、依赖、后端或数据变更。仅静态核对，未构建/测试或部署；详见docs/PK_INTERFACE_PREVIEW_PK40_REVIEW.md。

2026-11-01 v0.2.19-pk.39：扩展PK-017/021，Jenkins Agent的Compose启用init，由docker-init作为PID 1收养并回收孤儿进程；补充只重建空闲Agent的维护步骤。用户授权线上维护后234个僵尸清空至0，节点已在线，镜像/挂载/节点配置及其他容器保持。不改账号授权浏览器或应用业务，不新增ID；未构建/测试或触发Jenkins作业，详见docs/PK_JENKINS_AGENT_INIT_PK39_REVIEW.md。

2026-11-01 v0.2.19-pk.38：新增PK-038雷达邮件提醒，扩展PK-035管理员入口，沿用PK-019/021。三个可选AND条件、数量阈值、收件人和周期，复用系统SMTP；最新账号及原生额度/测智状态只读计数，持久化cron与跨实例互斥，默认关闭。仅静态核对，未构建/测试、真实发信或部署。

2026-11-01 v0.2.19-pk.37：扩展PK-037/021，修复账号统计轮次INSERT的PostgreSQL参数类型冲突；cron实际运行但每次事务回滚，导致首次调度/下一轮时间停留在迁移初始化时刻。显式时间及计数类型，失败日志保留安全SQLSTATE；不改五批策略、旧配置/数据或统计口径。

2026-11-01 v0.2.19-pk.36：扩展PK-036/021，将账号授权工作台缺省账号并发/优先级/每IP账号上限统一为20/1/3；独立工作台返回字段避免通用巡检配置覆盖，已保存参数优先，无迁移或历史数据修改。仅静态核对，不运行构建/测试或线上操作。

2026-11-01 v0.2.19-pk.35：扩展PK-034/035/036/021。只读应用日志确认自动测智TLS握手超时，未发现调度开关拒绝路径；新账号立即测智、已有账号重授权等待60秒。糖果发送层同出口最多3次总握手尝试，重建body并使用取消/发送保护，失败固定脱敏提示；不重试HTTP错误、完整回复或读取阶段失败，不改变普通连接测试/网关。保留用户pk.34及后续浏览器回收改动，无新表/历史数据改写。详见docs/PK_AUTHORIZATION_TLS_PK35_REVIEW.md。

2026-10-31 v0.2.19-pk.34：修复授权浏览器遗留僵尸进程（PK-036 扩展）。Chromium 的辅助进程（zygote、GPU、网络服务、crashpad）在浏览器退出后会被挂到容器 PID 1（即本进程）下，原先无人回收，每次授权累积约 4～12 个僵尸项。新增 `account_authorization_reap_linux.go`：仅在 `os.Getpid()==1` 时启用，每 30 秒且仅在没有授权尝试在飞时调用 `Wait4(-1, WNOHANG)` 回收孤儿，`account_authorization_reap_other.go` 为其它平台空实现；尝试入口 `authorizationChildStart/Done` 守护并发，保证不会抢走 `exec.Cmd` 仍在等待的退出码。Linux/Windows 均编译通过，服务器容器内实测：子进程退出后调用回收，`cmd.Wait()` 返回 `no child processes`，证明僵尸已被收走。另有运维侧处理：旧镜像部署（`sub2api-pk`）已补装 chromium 136 + Xvfb 并生成 `--no-sandbox` 包装器，宿主机新增 `/usr/local/bin/pk-browser-ensure.sh` 与 `/etc/cron.d/pk-browser-ensure`（每 5 分钟，覆盖 `sub2api-pk` 与 `pk-radar-preview-app-1`），容器被旧镜像重建后自动补装。不改表结构、不改授权判定与分组规则。完成后提交 main 并推送唯一新正式标签 v0.2.19-pk.34；旧标签不移动。Jenkins 由用户自行选择 `ACTION=release`、`SOURCE_REF=v0.2.19-pk.34`，`COMMIT_SHA`/`PACKAGE_ID` 留空。

2026-10-31 v0.2.19-pk.33：按用户最新要求扩展PK-031/034/035/036/021。授权保存后等待60秒再测智；已有账号凭据提交时恢复系统错误停调且保留原分组，测智失败不再次关闭调度；首次新账号继续无组停调准入。提交时账号快照在等待后、逐模型及分组事务核对；菜单换认证徽章并同步中英文状态。保留pk.32及全部用户浏览器实现，无迁移或历史数据批改，仅静态核对。详细依据与未测边界见docs/PK_AUTHORIZATION_RECOVERY_PK33_REVIEW.md。

2026-10-31 当前任务 v0.2.19-pk.31：基于用户更新后的私库 main / v0.2.19-pk.30（098aebfaabc25658d4c32f447bd7b6d631a1ddc9），修复已保存新测智分组仍提示“测智目标分组不存在或已删除”。工作台初次加载、糖果配置保存后刷新及提交前均用最新可选组清理隐藏的旧勾选ID，保留有效选择和账号输入；提交前发现旧选项被移除时仅刷新并提示确认，不自动提交。新重试任务重新读取当前糖果不合格组和已保存工作台及格组，保留原任务证据、并发/优先级/入库代理及模型快照；立即/定时重授权及其重试仍优先保留账号当前分组，无组时统一回退实际工作台pass_group_ids，旧group_ids仅兼容未保存过工作台的环境。校验失败显示具体不存在/停用ID，不跳过准入或绑定已删除组。复用既有表和JSON，沿用PK-034/035/036/021，无新增ID；完整保留用户pk.28～30浏览器实现，未修改账号/测智历史或数据。仅静态检查，不执行本地或服务器构建、测试、真实登录/模型请求、Jenkins或部署。按既定授权提交版本分支、合入并推送main及唯一新正式标签v0.2.19-pk.31，旧标签不移动；用户自行Jenkins选择ACTION=release、SOURCE_REF=v0.2.19-pk.31，COMMIT_SHA/PACKAGE_ID留空。详见docs/PK_AUTHORIZATION_GROUPS_PK31_REVIEW.md。

2026-10-31 历史任务 v0.2.19-pk.27：基于私库 main / v0.2.19-pk.26（749a4a9a796365b66eeb5cd0746a1f27c1c7f26d），补齐授权前网络失败换代理、失败任务重试、授权代理批量删除及雷达授权巡检逐账号“立即授权”。同任务按不同代理依次尝试，实际预检通过的代理最多5条，预检失败不占通过名额；保留密码提交后不重放、错误凭据/封禁/限流/需人工验证终止规则。任务45分钟总预算，展示代理编号及检查/通过计数，代理池耗尽或超时明确结束。重试使用最新未删除凭据并保留原任务参数；立即授权从最新账号状态及配置错误码校验，只绑定该行账号，执行前再次复核，已有同邮箱任务不重复创建，缺凭据须补全。手动操作唤醒现有持久化worker、保留并发/启动门/数据库锁及3/10分钟cron。授权代理支持当前页勾选与确认批量删除，不涉及原生IP管理。保留多模型测智/分组、凭据加密归档、原账号出口和浏览器实现；复用现有表及JSON，无迁移，沿用PK-019/021/035/036，无新增ID。仅静态核对，不执行本地或服务器构建、测试、登录/模型请求、Jenkins或线上操作。完成后按既定授权提交版本分支、合入并推送main及唯一新正式标签v0.2.19-pk.27，旧标签不移动；用户自行Jenkins选择ACTION=release、SOURCE_REF=v0.2.19-pk.27，COMMIT_SHA/PACKAGE_ID留空。详见docs/PK_AUTHORIZATION_RETRY_PK27_REVIEW.md。

2026-10-31 历史任务 v0.2.19-pk.26：基于私库 main / v0.2.19-pk.25（ec365b729b5f3441a7e3690c6e6c80e7cb064e2e），扩展账号健康度后台统计、雷达“账号统计观测”、入库账号原生IP分配及首字/连续失败标签。每轮读取账号管理最新非删除账号，按总数均分5批（不足5号不建空批，余数放前批），300号为60/60/60/60/60；按配置周期错峰，上一批结束后才开始下一批，批内串行、每次cron最多取100个队列项续跑而非新增第6批。默认30分钟周期/近24小时窗口，健康度为成功/(成功+上游失败)，平台错误不计；保存快照，列表和雷达不按访问重扫健康度日志，展示最快/最慢首字。新增迁移254的四张独立统计表，不改原业务表或历史账单。授权工作台可选直连或原生IP管理池，设置每IP账号上限，新入库按ID选择首个有效未满代理并在创建事务内锁定/复核，无容量不直连兜底；已有账号重新授权保留原代理，保留pk.24重授权测智与全部准入规则。沿用PK-019/021/035/036/037，无新增ID。仅静态核对，不运行本地/服务器构建、测试、真实请求或线上操作。完成后按既定授权提交版本分支、合入并推送main及唯一新正式标签v0.2.19-pk.26，旧标签不移动；Jenkins由用户选择ACTION=release、SOURCE_REF=v0.2.19-pk.26，COMMIT_SHA/PACKAGE_ID留空。详见docs/PK_ACCOUNT_HEALTH_PROXY_PK26_REVIEW.md。

2026-10-31 历史任务 v0.2.19-pk.25：基于私库 main / v0.2.19-pk.24（e080d558963242ca4bbc619c6afdcede6be1cf0b），新增账号列表“最近完成首字”和“连续失败”。统计复用保留的 usage_logs / ops_error_logs，按完成时间取最新成功请求、不取平均；只累计该账号上次成功后的上游失败请求，平台余额/配额、客户端错误与已恢复的2xx尝试不计数，按账号及请求去重。新增 PK-037，扩展 PK-007～010/021。用户确认：关闭首字提前响应时完全使用合入上游的原判定 result.FirstTokenMs，保留 semantic / visible；关闭时不安装提前响应 writer 或启动其调度，原生保活保留；开启三模式、计费及原上游指标不变。仅新请求使用新口径，不回写历史，不改表结构。仅静态核对，不运行本地/服务器构建、测试、真实模型请求或线上操作。完成后提交版本分支、合入并推送 main 与唯一新正式标签 v0.2.19-pk.25；旧标签不移动。用户自行 Jenkins 选择 ACTION=release、SOURCE_REF=v0.2.19-pk.25，COMMIT_SHA/PACKAGE_ID 留空。核对记录见 docs/PK_ACCOUNT_REQUEST_STATS_PK25_REVIEW.md。

2026-10-31 历史任务 v0.2.19-pk.24：基于私库 main / v0.2.19-pk.23（25af35a06），按用户最新要求让**定时重授权也走测智关卡**，并在任务列表标出重授权。`queueReauthorization` 读取糖果测试配置后写入 `intelligence_gate=true`、全部启用模型与推理强度、`fail_group_ids`=糖果设置的不合格目标；`pass_group_ids` 取该账号当前分组（`account_groups`），账号无分组时回退到配置 `group_ids`；没有可用糖果配置（无启用模型或无不合格目标）时保持旧行为，只换凭据、不清分组，避免凭据刷新被卡住。授权成功后由既有 `applyAuthorization` 分支清空分组、暂停调度并按结果分组：及格回到原分组/配置分组，不及格进糖果设置的不合格分组，请求失败或取消保持未分组。前端 `AuthorizationRecords.vue` 对 `source=reauthorization` 增加“定时重授权”标签与创建时间徽标。沿用 PK-034/035/036/021，无新增 ID，不改表结构。此条取代此前“定时重授权不套用工作台关卡”的描述。完成后提交 main 并推送唯一新正式标签 v0.2.19-pk.24；旧标签不移动。Jenkins 由用户自行选择 `ACTION=release`、`SOURCE_REF=v0.2.19-pk.24`，`COMMIT_SHA`/`PACKAGE_ID` 留空。

2026-10-31 历史任务 v0.2.19-pk.23：基于私库 main / v0.2.19-pk.22（1c7d60ba4），把授权码交换失败的真实原因显示出来并加重试。`exchange_failed` 消息附带 `authorizationExchangeDetail`（单行、截断 200 字，来自 OAuth 客户端的 `token exchange failed: status %d, body: %s` / `request failed: %v`）；`authorizationExchangeRetryable` 只在无 HTTP 应答（`request failed`/5xx/超时/连接重置/EOF/TLS）时重试一次，4xx（code 已消费、无效或过期）不重试；trace 增加 `exchange retry` / `exchange failed` / `exchange completed`。实测：同一账号经授权代理，正常客户端配置换 token HTTP 200，同 code 重放 400，Firefox 伪装 200；90 秒后才交换仍 200（code 存活超过 114 秒）。沿用 PK-034/035/036/021，无新增 ID。完成后提交 main 并推送唯一新正式标签 v0.2.19-pk.23；旧标签不移动。

2026-10-31 历史任务 v0.2.19-pk.22：基于私库 main / v0.2.19-pk.21（302bfc7c4），把授权追踪改成默认开启，不再依赖容器环境变量。`authorizationTraceEnabled` 默认返回 true，只有 `PK_AUTH_BROWSER_TRACE=false/0/off/no` 才关闭；追踪走应用日志（`logger.LegacyPrintf("service.authorization", ...)`），输出 attempt start（模式、浏览器路径、代理仅 host:port）、每个登录阶段、`/api/accounts/*` 上游状态与正文、以及 attempt failed（阶段、noReplay、完整消息）。邮箱、密码、2FA 统一替换为 `<redacted>`，单行压平并截断 600 字，代理 URL 只记录 scheme://host:port。实测（本机代理、故意改错密码）：默认态 `trace enabled = true`，日志按上述三段输出，密码未出现在日志中。沿用 PK-034/035/036/021，无新增 ID。完成后提交 main 并推送唯一新正式标签 v0.2.19-pk.22；旧标签不移动。

2026-10-31 历史任务 v0.2.19-pk.21：基于私库 main / v0.2.19-pk.20，把登录失败的上游依据显示到任务记录。新增 `authorizationUpstreamDetail`：任务消息附带 HTTP 状态、接口（`password/verify`、`mfa/verify` 等）、上游错误标识和一句上游说明（≤120字、去换行），并按接口而不是只按错误码归属阶段，修正 `incorrect_code` 被误判成 `login_http_failed` 的问题；新增默认关闭的追踪开关 `PK_AUTH_BROWSER_TRACE`（写 stderr，邮箱/密码/2FA 脱敏、单行≤600字），用于抓取上游原始响应；`needs_input` 拆成“邮箱/短信验证码”和“页面停滞（stalled）”两条独立提示。服务器实测（授权代理 socks5、同一正确账号）：正确凭据 21.0s 回调成功；错误密码 `password/verify` 返回 401 `invalid_username_or_password`；错误 2FA `mfa/verify` 返回 403 `incorrect_code`，三种结果均按上述格式显示。沿用 PK-034/035/036/021，无新增 ID，不改表结构、分组与准入规则。完成后提交 main 并推送唯一新正式标签 v0.2.19-pk.21；旧标签不移动。Jenkins 由用户自行选择 `ACTION=release`、`SOURCE_REF=v0.2.19-pk.21`，`COMMIT_SHA`/`PACKAGE_ID` 留空。

2026-10-31 历史任务 v0.2.19-pk.20：基于私库 main / v0.2.19-pk.19（f28aac6a59195f9b70eb6ee1d81b30d8af9dea53），补齐导入授权测智到雷达账号卡片、账号管理糖果列和测试详情的只读同步；按实际时间选择最新记录，卡片及筛选沿用真实判定，新任务记录时间、出口观测及逐模型正文，旧缺失数据明确标注。凭据历史改为任务结束后按邮箱更新单条记录，排队/执行中不提前展示；补全与编辑立即保存供下轮使用，删除清除密码/2FA并取消该邮箱未完成任务，保留账号与测智证据。使用加密任务快照和已有JSON/历史表，不改表结构、不回填伪造观测。沿用PK-034/035/036/021，无新增ID；保留全部启用模型并行、准入/分组、代理及cron规则。仅静态核对，不执行本地/服务器构建、测试、真实请求或线上操作。完成后提交版本分支、合入并推送main和唯一新正式标签v0.2.19-pk.20；旧标签不移动。Jenkins由用户选择ACTION=release、SOURCE_REF=v0.2.19-pk.20，COMMIT_SHA/PACKAGE_ID留空。

2026-10-31 历史任务 v0.2.19-pk.19：基于私库 main / v0.2.19-pk.18（14993c87a73956c2aae3ca693a6666de55251b1d），补齐授权后的多模型糖果测智。提交时保存糖果设置中全部启用模型及推理强度，同账号最多8个模型并发；一个模型答错不跳过其他模型。逐模型展示进度、答案、判定、脱敏错误及耗时，等待本轮结束后按全部及格/任一完整答错/未形成结论进行准入，取消不分组。沿用pk.18新版目标读取、及格多组、当前不合格组锁定、账号快照与准入保护；旧任务保留原模型快照，定时重授权不新增测智。复用jobs.settings，不改表结构、历史记录或浏览器逻辑。沿用PK-034/035/036/021，无新增ID；仅静态核对，不执行本地/服务器构建、测试、真实请求或线上操作。完成后提交版本分支、合入并推送main及唯一新正式标签v0.2.19-pk.19；旧标签不移动。用户自行Jenkins选择ACTION=release、SOURCE_REF=v0.2.19-pk.19，COMMIT_SHA/PACKAGE_ID留空。

2026-10-31 历史任务 v0.2.19-pk.18：基于私库 main / v0.2.19-pk.17（74ffd046b0a3674af9f4b001531b61c2638c9740），修复糖果测试设置已保存但授权工作台仍提示未配置的错误。工作台展示、提交校验和不及格转组统一读取新版雷达账号糖果配置；单次授权测智使用按顺序首个启用模型及其推理强度，提交时快照，转组时重新读取并锁定当前不合格目标。保留既有及格多分组、未分组隔离、准入校验、账号状态保护及定时重授权行为，不修改表结构或历史配置。沿用PK-034/035/036/021，无新增ID；仅静态核对，不运行本地/服务器构建、测试或真实请求，不操作线上。完成后提交并推送版本分支、合入main及唯一新正式标签v0.2.19-pk.18；旧标签不移动。Jenkins由用户自行选择ACTION=release、SOURCE_REF=v0.2.19-pk.18，COMMIT_SHA/PACKAGE_ID留空。

2026-10-31 历史 v0.2.19-pk.17 正式交接要求：用户确认已自行串通全部业务，并明确要求遵守不带origin前缀的版本交接规则。复用唯一版本工作分支 v0.2.19-pk.17，完成静态核对、凭据排查和清单同步后，提交并推送私库同名分支、合入main，并在确认未占用后创建及推送同名正式标签。Jenkins中的origin/v0.2.19-pk.17是远端分支显示；正式打包选择不带origin/的标签v0.2.19-pk.17。交接参数为ACTION=release、SOURCE_REF=v0.2.19-pk.17，COMMIT_SHA/PACKAGE_ID留空，由用户亲自点击；助手不触发Jenkins、不发布制品、不部署或重启线上。此要求取代本轮此前仅交付工作分支的描述，旧正式标签不移动。用户联调结论记为用户确认，不冒充助手独立执行的完整回归报告；助手已有服务器启动逻辑模拟检查仍保留其原验证范围，不运行本地构建或测试。

2026-10-31 未发布工作区（基于私库 main / v0.2.19-pk.16，保留本地 v0.2.19-pk.17 分支名）：扩展 PK-036/034/021。工作台授权成功后清空分组并暂停调度，执行一次糖果测智；及格进入工作台所选多个分组，不及格在实际转组时读取糖果测试设置中的“不合格账号移入分组”，不再单独选择不及格分组。请求失败、取消或准入失败保持未分组，已完成判定单独留存。巡检只同步最新账号管理错误状态及配置状态码，不主动发送hi。区分凭据拒绝、2FA错误、安全验证、限流与明确封禁。此前阶段要求只保留工作区；现按文首最新要求完成版本分支、main及正式标签交接，不自动打包或部署。CF自动验证按用户 2026-10-31 追加要求自行实现：沿用 Kilo 参考流程，Cloudflare 挑战不再直接判失败，而在页面内完成（Turnstile 组件用 CDP 真实鼠标事件点击、轮询到挑战清除后重开授权链接），无头身份失败即切换同代理的后台真 Chrome 会话，Linux 无 DISPLAY 时自启私有 Xvfb；密码提交后不再重放或换出口。已用用户提供的正常账号经本机代理实测两次完整授权回调成功（无头 21.4s、后台会话 20.7s），Turnstile 组件识别与点击坐标专项验证通过；未做服务器端回归，不宣称测智分组已实测通过。详见 docs/PK_AUTHORIZATION_GATE_REVIEW.md。

- 2026-10-31 v0.2.19-pk.15：沿用 pk.14 外置浏览器、版本回退和 Turnstile 保护，去除 Turnstile 默认值重复注册并补充无 config.yaml 时的 `TURNSTILE_REQUIRED`/`TURNSTILE_EXPECTED_HOSTNAME` 环境加载回归覆盖。无新增 ID，未运行本地/服务器构建或测试。

- 2026-10-31 v0.2.19-pk.14：按用户新要求改为外置浏览器，不再内嵌更新二进制/应用镜像或生成完整浏览器部署包。沿用 PK-016/017/020/021/036；旧封存验证保留，pk.12 安全修复与账号业务保持。用户已授权本轮服务器安装与一次 Jenkins 打包，实际结果单独记录。

- 2026-10-31 v0.2.19-pk.9：新增 PK-036 账号授权与巡检；扩展 PK-019/031/035/021。原生并发授权链接、独立代理预检、加密历史、3/10分钟 cron、可配重授权错误码、实时账号名单、补全后等下轮。新增五张隔离表和 Chromium 镜像依赖。仅静态核对，未运行构建/测试或真实接口，未操作服务器。

- 2026-10-31 v0.2.19-pk.12：基于 pk.11 继续沿用浏览器网页更新资源、PK-036 账号授权/巡检与 PK-032 空号池行为；扩展 PK-013～017 的私库正式版本检测/回退边界和 Cloudflare Turnstile hostname/action 校验、上游 fail-closed、部署环境配置。无新增 ID，未运行本地/服务器构建或测试。

- 2026-10-31 v0.2.19-pk.13：沿用 pk.12 的版本回退与 Turnstile 保护，补齐 `TURNSTILE_REQUIRED`/`TURNSTILE_EXPECTED_HOSTNAME` 的 Viper 环境可达性注册，确保纯 Compose 环境注入值进入配置结构。无新增 ID，未运行本地/服务器构建或测试。

- 2026-10-30 v0.2.19-pk.8：沿用 PK-031/035/021；雷达标题单行省略、无整条白底的圆角分页、不合格底色进一步弱化为 #fffafa。仅雷达展示层与维护文档调整；未运行构建/测试，用户自行打包更新。

- 2026-10-31 v0.2.19-pk.10：沿用 PK-013～017、PK-021 及既有认证验证码行为；在线更新/回退仅允许 release 构建和私库正式 `vX.Y.Z-pk.N` 标签，缓存同样拒绝其他来源，backup 回退校验普通可执行文件并修复 chunked 空请求判定。Turnstile 增加服务端 expected hostname、登录/注册/验证码/找回密码 action 绑定、上游非 2xx/nil 响应 fail-closed 和前端 action 传递；site/secret 继续保存在设置库，不复制 SN-team 密钥。未新增 ID。

- 2026-10-30 v0.2.19-pk.7：沿用 PK-031/035/021；雷达卡片标题14px、正文12px、辅助信息至少11px，桌面每行4张；去掉顶部状态条，不合格整卡浅红背景，含深色适配。仅页面样式及维护文档调整；未运行构建/测试，用户自行打包更新。

- 2026-10-30 v0.2.19-pk.6：沿用 PK-034/035/021；模型并行、完整非21立即转组、逐模型脱敏错误、紧凑卡片与账号列表独立糖果测试列。仅静态核对，不执行构建或测试；正式标签交由用户点击 Jenkins 并自行更新。无新增 ID、表结构或线上操作。

- 2026-10-30 v0.2.19-pk.5：新增 PK-035 雷达观测站；账号测试、出口探测和多模型判定复用 PK-034，分组配置复用 PK-032，二开选择框复用现有 Select。未改账务或现有表结构。本机未构建或测试；服务器构建、制品验收与独立部署结果以本轮记录为准。

**本清单是二开功能的唯一登记表，受 [项目约束](PROJECT_CONSTRAINTS.md) 保护。** 新增、扩展、修复二开必须同步登记；合并上游必须逐项核对。清单遗漏不代表可以删除已有功能，发现遗漏应立即补齐。

## 基线与阅读方式

- 2026-10-30 目标 `v0.2.19-pk.4`：从已发布 pk.3 / `3e9efa8fb2163dbef06b770d21732881d937c759` 修复 PK-034 账号糖果响应解析与原生 OAuth 连通测试之间的协议兼容差异。保留共享认证/模型映射/代理路径、完整最终答案判定和失败不转组；沿用 PK-034/021，无新增 ID。用户明确本轮不跑回归、直接修改并提交新版本；仅静态核对，不复用 pk.3 的测试结论，不自动触发 Jenkins 或部署。

- 2026-10-30 目标 `v0.2.19-pk.3`：用户要求服务器隔离回归后，账号糖果专项在 `v0.2.19-pk.2`（`216898306bb6d76ee2a86e81d869642fbd7a897d`）复现 OpenAI upstream 类型报“无 API Key”。本轮修复 PK-034 的该类型凭据读取，并将既有文件中的新回归纳入 PK-017 race 选择器。无新增 ID；本机不构建或测试，当前 tree 的实际结果以本轮服务器原报告为准，旧失败结果保留且不改写为通过。

- 2026-10-30 目标 `v0.2.19-pk.2`：从私库 main / `v0.2.19-pk.1`（`7866a0e14e43a17b379a0659f82475ca755fe70a`）继续，按用户要求移除代码中的固定服务器地址/SSH 用户名及文档、skill 中的实际部署身份信息。沿用 PK-017/020/021，无新增 ID；PK-034 账号糖果测试和其他业务实现保持。仅静态核对，不运行构建、测试或服务器连接，不移动已有标签。

- 2026-10-30 目标 `v0.2.19-pk.1`：从私库 main / `v0.2.18-pk.2`（`d8d1c0a1fefed3b720d60afed29c1ef0055c3eab`）合并上游正式 `v0.2.19`（`dece26153621959ccd7635814f4b2d08304e681a`），新增账号级糖果测试 PK-034；现有分组测试 PK-032 保持独立。用户确认先覆盖 OpenAI 全部账号类型，完整回答不合格时移除全部原分组、仅加入指定组，请求失败不转组。逐项静态核对及兼容修复见 [0.2.19 合并记录](docs/PK_UPSTREAM_0.2.19_REVIEW.md)。本轮不运行本地构建或测试，不将历史服务器报告用作本轮通过证据。

- 2026-10-26 后续目标 `v0.2.18-pk.2`：用户要求再次逐字节核对并在服务器回归通过后提交，特别确认首字开启不改变原始usage的计费/额度和记账次数。从pk.1提交 `7e0cbaca685a69212ec1959fbceb5788619b6c08` 继续，不移动旧标签；沿用PK-003/007～012/017/028/033，扩展既有回归及race选择器，修复服务器安全扫描发现的WebP依赖漏洞，无新增ID。固定字节依据、首字边界和服务器验证范围见 [首字与计费复核](docs/PK_BILLING_HEARTBEAT_0.2.18_REVIEW.md)，结果以匹配本轮tree的服务器报告为准，不预先声称通过。

- 2026-10-26 从私库 `v0.2.17-pk.16` / main（`f992259daaa56f4719bc7fc38420edde4ae09ee7`）合并上游 `v0.2.18`（`ac1b8f1271be0d674fa6f074403c1410f30a3a81`），唯一目标 `v0.2.18-pk.1`。无新增 ID，保留全部有效二开和 PK-029/030 已授权移除状态；按上游行为为主，在 PK-008/009/028 交叉处作必要适配。逐项静态依据、冲突处理、迁移及升级注意事项见 [0.2.18 合并记录](docs/PK_UPSTREAM_0.2.18_REVIEW.md)。本轮未运行任何测试或构建，未自行生成测试文件；不将上游或历史回归当作本次通过结果。按既定授权提交、合入 main 并准备正式标签，用户自行点击 Jenkins release，未操作生产。

- 首次二开上游基线：`a60a29549f488a854966aaec9541abbe006cac22`（0.2.10）。首次二开保存：`6655dd8ca`，恢复标签 `pk-baseline-20260930`。
- 2026-10-08 在唯一在途历史分支 `codex/jenkins-build` / 草稿 PR #7 合并上游 `v0.2.14`（commit `0363b8cdba8cec3e2ba4b2dbd49c4481143fa55d`，tag 对象 `1400a7b482974d98db5b284a8b2afbe3eaf9aaef`），合并前私库为 `3902278043ab5edb1bf590cd755b87e043be4d49`。逐项静态核对及当时待验证项见 [0.2.14 合并记录](docs/PK_UPSTREAM_0.2.14_REVIEW.md)。源码 VERSION 对齐 `0.2.14`，未创建发行标签。合并提交 `fc468013` 生成时仅作静态核对；后续 Jenkins #4 候选 `4-fc46801394b0` 已构建成功，十一项实际制品验收通过，证据只绑定该旧 commit。当前流程新 tree 的完整源码测试以新报告及 notes 为准，旧候选成功不代表当前已合入 main、正式发布或线上更新。
- 2026-10-02 合并上游 `v0.2.12`（commit `5106065716e494204fc0e8db16f68f6e9d576be0`，tag 对象 `cbe9966432317e6757b47f598a103d2132ec27db`），合并前私库为 `162a58972bee08078a43eefa761da1b245f667fa`。逐项核对见 [0.2.12 合并记录](docs/PK_UPSTREAM_0.2.12_REVIEW.md)，正式发布目标为 `v0.2.12-pk.1`；发布与线上更新分别记录。
- 2026-10-02 合并上游 `v0.2.11`（commit `96f4c115c9749078f90cbf210a01d39baf3f53b6`），合并前私库为 `32f068dcbdc75063ff7f6488aef6c85912d7bf3f`。逐项核对及本次验证见 [合并记录](docs/PK_UPSTREAM_0.2.11_REVIEW.md)；合并源码不代表已发布或更新线上。
- 本次清点覆盖从该上游基线到私库 `23e5fe663338c86cd0cebbf2f688c8a75dd8c152` 的全部二开提交；应用发行参照 `v0.2.10-pk.7`，另登记本次新增的项目约束机制。此处是代码核对基线，不代表线上当前版本。
- 以下 PK-001 至 PK-028 及 PK-031 至 PK-038 均为有效保护项；PK-029、PK-030 已按用户授权移除，保留原 ID 与回退记录，不得复用。文件路径用于定位，不是完整修改白名单；所有新调用入口、迁移和旁路都要检查。
- 每项说明“必须保持”的行为。文末另列验证入口与已知边界；测试存在、历史验证通过和本次候选通过是三件不同的事。
- 2026-10-21 用户最新要求取代 2026-10-08/19 的源码测试及 notes 前置门禁：核对二开保护和变更后直接提交、合入私库 main 并推送不带 `origin/` 的正式版本标签，交给用户点击 Jenkins `ACTION=release`。不再默认运行完整源码测试，不要求源码验证 notes；Jenkins 继续执行构建、同制品运行时验收和私库发布，不使用 GitHub Actions。可选测试如实记录，未执行不能声称通过。下文历史门禁仅说明当时状态，现行规则以 PK-017 为准。

- 2026-10-15 在 `codex/v0.2.16-pk.1` 合并上游 `v0.2.16`（`01842576b3d5c1e2545b5a99ad555d36e28ed306`），增加 PK-028；逐项依据见 [0.2.16 合并核对](docs/PK_UPSTREAM_0.2.16_REVIEW.md)。最终源码验证以绑定提交的 notes 及服务器原报告为准，发行与部署另行记录。
- 2026-10-16 用户明确要求“所有代码都回退到 v0.2.16-pk.1”。从 `d1b1dbf650b69292cb3998a9f1d32821c769dfd2` 逆序撤销 pk.2～pk.5 的四次提交，源码、原有测试和验证入口恢复到私库标签 `v0.2.16-pk.1`（`f0671148bc54d87a95623035e23a94ad5c74be0e`）；仅本清单补充撤销记录。无新增二开；PK-001～028 的实现与目标标签一致，PK-029/030 按下文退出。源码提交以本次隔离服务器完整验证报告及绑定 notes 为准，不沿用旧版报告冒充本次执行；本次不发布、不部署、不改业务数据。

## 功能索引

2026-10-19 在唯一目标分支 `codex/v0.2.17-pk.1` 合并上游 `v0.2.17`（`060d0160417782cd962a0c7f460f6cd4565d4916`），合并前私库为 `82689422e5bb1444769265534265f570fed274a2`。无新增二开，PK-029/030 继续保持已授权移除；全部有效 ID 的静态核对及源码验证要求见 [0.2.17 合并记录](docs/PK_UPSTREAM_0.2.17_REVIEW.md)，普通 OpenAI Key 计费边界见 [专项审计](docs/PK_OPENAI_KEY_BILLING_AUDIT_0.2.17.md)。本次合并不代表正式发行或线上更新，动态结果只以当前 tree 的服务器原报告及最终 commit notes 为准。

| ID | 二开功能 | 必须保留的重点 |
|---|---|---|
| PK-001 | 账号缓存转换设置 | 新增/编辑、默认值、倍率 0、extra 保存与校验 |
| PK-002 | 缓存转换算法 | decimal 向下取整、溢出检查、创建明细清零 |
| PK-003 | 费用与额度结算 | 原始输入先拆分、转换一次、各账务口径一致 |
| PK-004 | 长上下文与账号统计 | 原始档位、独立倍率、原始/客户统计口径 |
| PK-005 | 下游 usage 改写 | JSON/SSE 无损、别名/显式 0、原始用量保留 |
| PK-006 | 多协议与通道接入 | Responses/Chat/Messages、直通、桥接、WS、图片、embeddings |
| PK-007 | 首字设置 | 独立页签、默认关闭、随机范围、局部保存与缓存 |
| PK-008 | 首字提前响应 | 注释/ping/空格三模式、协议边界、禁用时沿用原上游首字 |
| PK-009 | 双计时与持久化 | 开启时真实首包、关闭时原上游首字、请求快照、异步传递、所有 SQL 路径 |
| PK-010 | 首字显示与权限 | 用户原文案、管理员上游指标、导出和监控不泄露 |
| PK-011 | 心跳与排队/换号 | 注释/ping不占正文提交判定，空格算正文，周期保活不丢 |
| PK-012 | 提前响应后的错误与并发 | 协议终止事件、响应头隔离、写锁、停止与 context 生命周期 |
| PK-013 | PK版本标识与排序 | “版本号 + PK出版”、pk 修订按数值比较 |
| PK-014 | 私库更新与恢复来源 | 私库/私有镜像、只读凭据、无官方回退路径 |
| PK-015 | 更新下载和文件替换 | 资产授权、跨域去凭据、SHA256、重复更新固定路径 |
| PK-016 | Docker网页更新持久化 | 数据卷可执行文件、种子哈希、镜像回退边界 |
| PK-017 | 私有发行与部署入口 | 正式标签、Jenkins 打包、同制品运行时验收、私库发布、amd64 |
| PK-018 | 新实例启动并发保护 | 可跳过全局启动清理，继续清理过期槽位 |
| PK-019 | 后台启动与停止交接 | 受控后台、普通文件激活、只启动一次、停止安全，含账号授权 |
| PK-020 | 切换与恢复运维流程 | 保护端口/数据/密钥、请求排空、后台单实例交接 |
| PK-021 | 二开登记与项目约束 | Git持久化入口、唯一清单、增量登记、逐项合并审查 |
| PK-022 | 币安 UID USDT 自助充值 | 完整五位金额乘订单倍率、账户加密、流水去重、过期回查、订单权限 |
| PK-023 | 管理员每日消费页签 | 复用全部使用记录、默认今日全用户、可配置自动刷新、仅管理员 |
| PK-024 | 管理员币安实时账单 | 直接只读查询收支、日期与收款账户筛选、金额原精度、与入账隔离 |
| PK-025 | 币安专用香港出口探测 | 每次操作重新探测、固定成功节点、香港校验、失败关闭、凭据隔离 |
| PK-026 | 耻辱排行榜与手动扣费 | 独立菜单、当天与日期筛选、实时刷新、用户只读、精确倍率、逐条防重复扣费 |
| PK-027 | 安全策略触发自动禁用 | 默认关闭、可配置次数、独立累计、管理员豁免、解禁可重计、鉴权失效 |
| PK-028 | OpenAI 账号 Kilo 桥接 | 独立默认关闭、用户模型映射、账号代理、ultra 转 max、工具参数保真 |
| PK-029 | 账号隐藏计费倍率及下游用量缩放 | 已授权移除；恢复 pk.1 计费与输出，保留历史记录 |
| PK-030 | 原始 Token 趋势统计 | 已授权移除；恢复 pk.1 趋势口径，保留历史数据 |
| PK-031 | DP API 品牌与柔蓝主题 | 渐变标志、统一名称、胶囊导航、移除友链，保留权限、响应式与深色模式 |
| PK-032 | 服务端定时智商测试 | 全分组/类型并发、失败自动重试、保存清空重启、当次模型/强度、糖果文案/回复、每行6张、专用 Key 轮换、回环调用、最近30条 |
| PK-033 | 联系方式配置与展示 | 文字/二维码独立可选、上传校验、首页/登录/用户仪表盘、条件锚点及醒目展示 |
| PK-034 | 账号级糖果测试与自动转组 | OpenAI 全部账号类型、固定账号直测、手动/独立定时、答案21、失败不转组、不合格移除全部原组 |
| PK-035 | 管理员雷达观测站 | 分组/账号/授权巡检页签、账号卡片、多模型判定、出口IP、服务端筛选、新表隔离、旧历史只读兼容 |
| PK-036 | 账号授权与巡检 | 原生OAuth并发、独立代理预检、CF挑战页内完成与无头→后台Chrome身份升级、加密凭据历史、cron巡检/重授权、可配错误码、实时账号、缺凭据补全 |
| PK-037 | 账号请求健康统计 | 最近完成首字、连续上游失败、五批错峰健康度快照、最快/最慢首字、平台错误排除、雷达统计进度 |
| PK-038 | 雷达账号数量邮件提醒 | 可选额度/合格/非401条件、阈值与收件人、持久化cron、系统SMTP复用、结果展示与跨实例互斥 |

## PK-001

**账号缓存转换设置。** 引入：`6655dd8ca`。

- OpenAI 账号新增/编辑页保留独立缓存转换开关及倍率，默认关闭、倍率 `1`；`0` 和有限非负小数有效。不能合并到分组倍率或账号计费倍率。
- 存储键为 `accounts.extra.cache_conversion_enabled`、`cache_conversion_multiplier`。保留非法类型、负数、NaN/Inf 等校验；普通更新未提交这些键时保留已有值，显式关闭和倍率 0 不被当成缺省。
- 创建、普通更新、局部 extra、批量 API、导入/重新授权的保存入口都要审查；批量 API 支持不等于批量页面已有相应 UI。
- 平台限制为 OpenAI，不限制为 API Key 类型；OAuth 兑换、Codex Session/PAT 导入和批量 RT 创建的接入也要保留。批量 API 预检目标存在且属于 OpenAI，保存后继续刷新调度快照；局部 extra 保留 JSONB 合并语义。
- 入口：`frontend/src/components/account/{CreateAccountModal,EditAccountModal}.vue`、`frontend/src/types/index.ts`、中英 `admin/accounts.ts`；`backend/internal/service/{account_cache_conversion_validation,admin_account}.go` 及账号 repository 的 extra 更新逻辑。
- 验证：设置往返、缺字段保留、显式 false/0 和非法倍率；已有 `TestPKCacheConversionSettings`。重导边界见文末。

- 2026-10-15 新增同平台的 PK-028 独立开关，缓存转换默认值、倍率0及保存行为保持；创建/编辑现有回归覆盖两个开关独立保存。

## PK-002

**缓存转换算法。** 引入：`6655dd8ca`；输出缓冲写入整理：`74c372bcc`。

```text
新缓存读取 = 原缓存读取 + floor(原缓存创建 × 转换倍率)
新缓存创建 = 0
```

- 使用 decimal 计算、向下取整及 int32 范围检查；不能改成不精确的浮点四舍五入。
- 原读取量不再乘转换倍率，普通输入和输出不直接受此倍率影响。缓存创建总量、5m/1h 明细转换后清零；倍率 0 仍清零创建量，但不新增读取量。
- 开关关闭保持原行为。计费和出口分别使用原始 usage 各计算一次，不对已经转换的 usage 再转换。
- 入口：`backend/internal/pkg/cacheconversion/usage.go`、`backend/internal/service/{openai_cache_conversion,openai_cache_conversion_output}.go`。
- 验证：原读取存在、倍率 `0/0.5/1`、小数边界、关闭、非法输入与溢出；已有 `TestPKCacheConversionDisabledPreservesOriginal`、`TestPKCacheConversionBillingTokens`。溢出错误的结算边界见文末。

## PK-003

**费用与额度统一结算。** 引入：`6655dd8ca`。

- 先从原始上游 usage 拆出普通输入，再转换缓存并定价；不能将转换后减少的缓存量误算为普通输入，再收一次费用。
- usage 记录、余额、订阅额度、API Key 额度/限速额度及账号额度使用同一转换结果；开启/关闭的两条路径都保留。
- 2026-10-26 `v0.2.18-pk.2` 服务器复核同步既有分组定价集成测试：`ultra/persistent` 仍完整保存，但普通路径按上游未知强度的基础倍率1结算，仅Kilo专用客户计费context启用扩展倍率；保留数据库往返、常规强度和全部金额断言。该测试调整对应本次两开关关闭时遵循上游的明确要求，不恢复旧的共享倍率泄露。
- 2026-10-08 合入上游 v0.2.14 的在途请求 API Key 删除结算修复：共享 `usage_billing_repo.go` 仅在该 Key 已不存在时跳过它自身的额度/限速持久化，用户余额或订阅、账号额度和幂等结算继续按原转换结果执行；真实数据库错误仍回滚。预留与实扣口径不变。上游删除 Key 的余额回归保留，转换倍率与删除 Key、订阅、账号额度的组合仍待后续获准动态验证，本次只作静态核对。
- 下游展示改写不反向污染上游原始计费证据，重试、桥接、stream usage 收集也不能重复转换或漏记。
- 2026-10-02 合入上游余额在途预留：预留只占用 Redis 中的请求额度，不直接扣款；最终账务仍用转换后的 `ActualCost`。请求及异步计费共同持有预留，计费先同步扣余额缓存，再释放预留，异常/丢弃/panic 的引用也须归还。
- 预留发生在选账号之前，以请求体估算普通输入与输出上限，不读取选定账号的缓存转换倍率或真实 usage，也不精确覆盖服务档位与长上下文倍率。预估可能高于或低于最终账单；低余额并发可能比旧版更早被拒绝。不能把该上游防并发机制描述为与缓存转换实扣严格相等，不能用预留金额替换实扣。开关与参数仍由上游 `billing.inflight_reservation` 控制，默认开启。
- `RecordUsage` 已收到原始 usage 但转换溢出时，保留原始 token 证据、`ActualCost=0`、跳过后续扣费并返回错误；不能默默改收原价。上游出口提前返回错误而无法到达此分支的缺口见文末。
- 入口：`backend/internal/service/{openai_gateway_usage,billing_service,openai_cache_conversion,openai_cache_conversion_output}.go` 及各转发结果的 usage 传递。
- 验证：普通输入/读取/创建/输出拆分、费用及各额度结果、关闭时原账务。已有 `openai_gateway_usage_integrity_test.go`；合并上游余额预留等新机制时必须另查预估与实扣是否使用兼容口径。
- 2026-10-02 在该既有文件扩展 `TestPKCacheConversionInflightBillingPreservesOriginalContext`：倍率0/0.5/30与原始上下文272000/272001组合，检查预留独立、异步实扣前持有、各额度统一、原始上下文选档以及幂等释放；候选与正式发行CI均纳入race选择。
- 上游 `TestInflightEstimate_AccountMappingNoDBAndBoundedMemory` 在同一测试二进制的独立子进程内测量，避免整个 service 测试进程的残留状态混入堆内存差值；保留 100 次预热、20000 次随机模型请求、数据库/快照断言和 8 MiB 阈值，并确保被测服务存活至末次采样。子进程失败仍使原测试失败，不改变生产预留逻辑。

## PK-004

**长上下文档位与账号成本统计。** 引入：`6655dd8ca`。

- 保留 `UsageTokens.OriginalContextTokens` 和 `InputContextTokens()`，价格档位依据转换前原始上下文，不能使用转换后缩小/放大的 token 总数选档。
- 这项适用于 token 阶梯、按次计费上下文档位和整会话长上下文倍率；保护的是二开转换后的兼容语义，不把整套上游定价功能当作 PK 新增。
- 账号/分组倍率与缓存转换倍率相互独立。
- 自定义账号价格统计及模型价格文件回退使用原始 token；启用“将客户定价应用于账号统计”时使用转换后、客户倍率前的 `TotalCost`。不能在合并中把两个分支统一成另一种算法。
- 入口：`backend/internal/service/billing_service.go`、`openai_gateway_usage.go` 中的 usage 构造、价格选择与账号统计分支。
- 验证：原始长上下文恰好跨阶梯、转换倍率 0/小数/大于1、两种账号统计价格分支。现有用例不能代替所有模型价格及预留机制的场景验收。
- 2026-10-02 上游新增 `gpt-6.1-sol` 的 272000 原始上下文阈值，继续使用 `InputContextTokens()` 选档；转换后 token 数量跨过阈值不得改变该判断。组合回归扩展在原有 `openai_gateway_usage_integrity_test.go`，按 PK-017 的提交前源码验证入口执行并记录真实环境。

- 2026-10-15 兼容上游 v0.2.16：实际 OAuth 类凭据及最终 GPT-6.1 Sol 模型决定模型长上下文豁免，自定义区间仍按原始上下文选档；API Key 不继承订阅豁免。账号原始 token 统计与转换溢出零扣费分支保留。

## PK-005

**下游 usage 与无损输出。** 引入：`6655dd8ca`。

- 开启缓存转换时，下游实际 JSON/SSE usage 同步改写；关闭时保持原输出路径。
- OpenAI 输入总量和 `total_tokens` 按对应协议语义调整；Anthropic `input_tokens` 保持普通输入语义。兼容 usage 字段别名、显式 `0` 和 5m/1h 创建明细。
- 只修改目标 usage 字段，保留模型文本、未知字段、事件类型及 SSE 多行 frame。保留原始 usage 供计费；客户端需要 usage 的场景继续向上游请求 stream usage。
- 保留 canonical 字段与已有别名同步、嵌套显式 0 优先、Anthropic 5m/1h 创建量回退。typed Responses/Chat 序列化不能让 `omitempty` 丢掉显式 0；JSON 改写后移除旧 `Content-Length`，SSE 保留注释、空行及 `[DONE]`。
- 客户端未请求 `include_usage` 不应丢弃已经收到的上游 usage；原始 Chat 强制向上游请求 stream usage，Anthropic→Chat 发现真实 usage 后保留下游发送路径。
- 入口：`backend/internal/service/openai_cache_conversion_output.go`、`openai_gateway_{response_handling,passthrough,cc_pipeline}.go` 及 PK-006 接入点。
- 验证：已有 `TestPKCacheConversionJSONKeepsPayloadAndOriginalUsage`、`TestPKCacheConversionSSEPreservesTextAndFraming`；增加协议或上游字段时逐个检查原始数据与改写结果。

2026-10-30 合入 v0.2.19 时扩展：上游用量解析新增 `prompt_cache_hit_tokens`，下游转换的根级读取别名同步追加在已有别名之后，保留 canonical/nested 显式0优先。开启转换时各已有别名同步，关闭时返回原字节；既有 `openai_gateway_usage_integrity_test.go` 增加对应断言但本轮未执行。

## PK-006

**跨协议和通道接入。** 引入：`6655dd8ca`。

必须保留以下已经接入的转换路径，不能只保留转换模块本身：

| 通道 | 主要入口（均在 `backend/internal/service/`） |
|---|---|
| Responses、Chat、Messages JSON/SSE | `openai_gateway_response_handling.go`、`openai_gateway_chat_completions*.go`、`openai_gateway_messages*.go` |
| Responses/Chat/Messages 兼容桥接和回退 | `openai_gateway_responses_anthropic_native.go`、`openai_gateway_responses_chat_fallback.go`、各 `*_anthropic_native.go`、`*_chat_fallback.go`、`openai_gateway_cc_pipeline.go` |
| 直通 | `openai_gateway_passthrough.go`、`openai_gateway_chat_completions_raw.go` |
| WebSocket 文本帧和协议适配 | `openai_ws_forwarder_ingress.go`、`openai_ws_forwarder_v2.go`、`openai_ws_v2_passthrough_adapter.go` |
| 已接入的图片出口 | `openai_images.go`、`openai_images_direct.go`、`openai_images_responses.go` |
| Embeddings | `openai_embeddings.go` |

验证要覆盖下游实际 usage 与内部账单两侧。当前 API Key 号池是历史真实验收范围；这不授权合并时移除已有其他路径。WS 二进制帧、部分 OAuth 图片合成路径的边界见文末，不能声称所有图片或 WS 都已改写。

2026-10-02 空格首字模式在既有 handler、service 与 Grok HTTP transport 的自动重试/协议回退入口增加同一请求门控，具体见 PK-011。仅空格正文已提交时拒绝重试，comment/ping 的转换与回退行为保留；真实 usage 仍走原缓存转换与账务路径。

- 2026-10-15 Kilo 接入既有 HTTP Responses 请求构造器，普通/透传及 Chat/Messages 兼容入口继续走现有 usage/计费/首字链；不新增网络客户端。该桥接不支持上游 WebSocket，启用时强制 HTTP，详见 PK-028。

## PK-007

**独立首字设置与配置链路。** 引入：`54b4372c2`；后续完善：`41958f4d4`、`a6123c20c`。

- 管理员系统设置保留独立“首字设置”页签、开关及随机时间上下限；默认关闭，范围默认 1–2 秒。界面按秒输入（精度 0.001），后端按毫秒保存。
- 键：`stream_early_heartbeat_enabled`、`stream_early_heartbeat_min_delay_ms`、`stream_early_heartbeat_max_delay_ms`；必须满足 `0 ≤ min ≤ max ≤ 30000`，非法范围不写入。
- 2026-10-02 增加 `stream_early_heartbeat_mode`：`comment` 为默认/旧配置行为，`downstream_ping` 为显式选择的下游兼容模式。缺字段保留，显式非法值/空字符串/null/错误类型拒绝；坏存储配置安全关闭并回退默认。GET/PUT、审计、设置缓存和前端局部保存同步包含此字段。关闭开关不清除已选模式。
- 2026-10-02 用户进一步要求增加第三种 `space`（空格首字/正文占位），并解释三种模式。它代表普通空格 U+0020，不是空字符串；旧默认及已保存的 comment/ping 选择不变。三种值必须在后端解析/校验、GET/PUT、审计、缓存及前端中英说明中同步维护，关闭开关仍保留所选模式。
- 局部更新缺失字段保留原值，显式 false/0 生效；审计包含新增字段。设置缓存默认60秒，当前进程成功保存后及时失效；读取失败/非法配置采用安全关闭回退。
- 保存失败不能提前替换有效缓存。其他实例按自身缓存刷新，已开始的请求使用启动时取得的设置，不能承诺所有实例和在途请求立即变更。
- 入口：`frontend/src/views/admin/SettingsView.vue`、`frontend/src/api/admin/settings.ts`、类型和中英设置文案；`backend/internal/service/{domain_constants,settings_view,setting_parse,setting_update,setting_gateway_runtime}.go`；`backend/internal/handler/admin/setting_handler*.go` 和 `dto/settings.go`。
- 验证：`TestStreamEarlyHeartbeat*`、`TestUpdateSettingsEarlyHeartbeat*`、`SettingsView.spec.ts` 中设置、秒/毫秒换算及局部更新场景。

## PK-008

**真实提前发出的 SSE 心跳与空格占位。** 引入：`54b4372c2`，完善：`41958f4d4`；2026-10-02 先增加显式结构化 ping 模式，再按用户要求增加空格模式。用户已明确选择保留真实 Responses ID，接受占位可能晚于设定时间。

- 对已接入的 OpenAI 兼容 Responses、Chat、Messages SSE 请求，在完成必要校验后、等待账号/上游时，从请求起始时间计算随机发送时机，真实写出并 flush 无正文的 `: keepalive` 注释。
- 上述注释为默认 `comment` 模式。选择 `downstream_ping` 时，首次定时写出 `data: {"type":"ping"}\n\n`，Messages 另带 `event: ping`；后续周期保活仍为注释。这两种旧模式不注入模型正文、空格、usage、伪造响应ID或修改真实输出。
- `space` 是用户明确要求的第三种正文占位模式，允许额外发送并保留一个 U+0020 空格，不声称可撤回或加快模型生成。Chat 使用合法 assistant 开始块、空格正文块和空 delta 块，后者推出 New API 原生 Chat 转发暂存的上一帧；后续 Chat 标识保持同一网关响应，真实正文、工具参数、usage 与未知字段保留。
- Responses 的空格使用独立 message 的完整 item/content/text 生命周期，保留在终态 output 中；保留上游真实 response.id、真实 item/tool ID、usage 和续聊语义，统一调整占位后真实 output_index 及出口 sequence_number。为避免尚未知真实 ID 时伪造响应，须等真实 response.created 到达且配置时间已到才注入；因此 Responses 空格可能晚于设置时间。模型输出先开始时直接保留真实输出，不为占位延迟业务。
- Messages 不实现空格占位，选择 space 时该请求使用注释保活。Responses 的 JSON Schema/JSON Object 结构化输出也使用注释保活，避免独立空白 message 被 JSON 解析器拒绝；Chat 接口携带 input 且没有 messages 的兼容请求可能返回 Responses SSE，也回退注释以避免混合协议。设置值仍保留 space。非流式、WebSocket、图片和 compact 等原排除范围不变。
- 空格是输出内容，会影响逐字比较、签名及下游缺失 usage 时的文本估算。本站仍使用原上游 usage 计费，不能声称任何下游的估算费用都不受影响；不能用空格伪造真实模型 token。
- New API `1a4166d8` / `v1.0.0-rc.41` 的 HTTP/SSE scanner 接受非空 data，原生 Responses 立即转发，原生 Chat 缓存一帧；Responses→Chat 保留非空空格。须保护其 terminal-only 正文兼容：真实正文只出现在完成快照时，出口补足缺失真实文本事件，避免已有空格让转换器跳过真实内容。此项代码依据与我方回归不同于真实部署链路验收。
- 兼容模式针对原版 Sub2API 的 Responses 默认 `semantic` 和原始 Chat 记时路径；Responses `visible` 仍等待非空内容，跨协议桥接/大请求保护也可能丢弃或缓冲 ping。该 ping 不是标准 Responses/Chat 模型事件，严格 SDK 可能拒绝；下游可能把它当已提交输出并停止失败换号。这些限制须在管理员设置中展示，不声称普遍兼容。
- 2026-10-02 用户接受“下游日志记首字、客户端仍可能等待”的行为并授权发布当前 ping 方案。New API `1a4166d8` / `v1.0.0-rc.41` 的 HTTP/SSE scanner 会对首个非空 data 帧记时；原生 Chat 缓存上一帧，Responses 转 Chat 丢弃 ping，故其日志时间不保证等于客户端收包时间。此项为源码核对，未做 New API 串联动态验收；不引入正文空格。
- 上条记录的是 `0.2.11-pk.1` 的 ping 发布范围。此后用户取消付费串联实测并单独授权新增 space 模式；空格变更按本条新增约束保护，不反向改写旧版或宣称已做线上实测。
- 真实业务正文开始后停止提前心跳；请求快速结束或取消时不得再写。随机数用于调度发送，不能直接作为统计值，也不伪造模型文本。
- 单纯修改 Header 或写出成功响应头不能取消等待心跳；首个心跳设置 SSE/no-cache/`X-Accel-Buffering: no` 并移除 `Content-Length`。真实输出更早到达时立即转发，不等待随机时间。
- 不注入非流式、WebSocket 入站、图片、legacy compact、native V2 compact 等排除路径；上游 WS 转下游 SSE 的既有重连语义保留。
- 适用范围由 `OpenAIGatewayHandler` 及其路由决定（包括复用该处理器的兼容平台），原生 Claude/Gemini handler 不因这项改动自动纳入；新增路由必须重新核对处理器。
- 2026-10-31 pk.25 用户明确：禁用提前响应时不安装二开 writer/定时器，完整沿用合入上游的首字判定，包括原 semantic / visible 配置；原生周期保活和协议转发不变。开启时才采集真实首包，服务器写出耗时不等于客户端网络到达耗时，部分 SDK 忽略注释。
- 2026-10-02 下游兼容调查：原版 Sub2API 的 Responses 会忽略注释及 `response.created/in_progress/keepalive`；语义模式与可见内容模式判定不同，空语义事件不能保证所有下游识别。Chat 原始转发会对非 usage-only 的数据块记时。账号测试页的 `_` 是 `AccountTestModal.vue` 的 UI 光标，不是可被协议撤回的模型字符；不得据此宣称注入 `_` 后能无损替换正文。
- 入口：`backend/internal/service/{openai_early_sse_heartbeat,openai_early_sse_space}.go`；`backend/internal/handler/{openai_gateway_handler,openai_chat_completions}.go`。
- 验证：`TestPKEarlyHeartbeat_*`、`TestPKFirstResponse_DisabledHeartbeatStillRecordsBodyAndCopiesContext`，以及获准的隔离远端通过实际反代读取 SSE。
- 空格扩展沿用 `openai_compact_sse_keepalive_test.go`：`TestPKEarlyHeartbeat_Space*` 覆盖 Chat 三帧、真实 Responses ID、terminal-only 正文/工具生命周期、快照字段、CRLF/分片、多行 data、短写、回退与取消。原 comment/ping 回归保留。新增用例由现有 CI/Release 的 `TestPKEarly` 选择器纳入；是否通过须核对本次提交的实际本地结果。

2026-10-26 合入 v0.2.18：PK-008 的 Responses 空格转换将上游新增 `response.metadata` 识别为占位前元数据，避免其提前关闭占位；仍只从 `response.created` 获取真实 ID，保留结构化输出/已含正文回退、序号、usage及工具生命周期，不为 metadata 伪造响应 ID。仅静态核对，未运行测试或构建。

同日pk.2按用户追加要求扩展既有usage完整性与handler回归：禁用/comment/ping/space四状态，真实reader解析后以同一原始usage和固定定价时间对账，并通过真实handler核对错误/断流/换号时最终账务提交次数。保留space已提交后禁止重放的协议安全边界；不把额外空格当模型token，不擅自修改上游原有partial错误收费策略。服务器完整回归的实际结果和tree按本轮报告记录，不能用测试代码存在代替通过。

## PK-009

**双计时与全部持久化路径。** 引入：`54b4372c2`，完善：`41958f4d4`。

- `usage_logs.first_response_ms` 独立记录首次成功非空下游正文写出的实测耗时（可为心跳）；原 `first_token_ms` 保存原上游计时，调度/计费不得改用随机值。
- 保留 nullable 字段和真实 `0` 的区分。历史记录保持原值/null，不回填或按当前开关重算。2026-10-31 pk.25 起，新记录 first_response_ms 表示按请求启动设置选定的展示首字：关闭或不适用提前响应的路径原样使用 result.FirstTokenMs；开启且适用时使用实际下游首包。字段名保持兼容。
- 异步 usage worker 使用独立原子计时状态，通过 `CopyFirstResponseTiming` 传递到 detached context；不能异步持有已回收的 Gin context。
- 开启提前响应时只记录第一次实际 body 写出，header-only flush 不计；换号不重置同一个下游请求的首包计时。关闭时直接使用原上游记录值，不因前导事件或保活提前。
- SQL select/scan、单条插入、批量、best-effort、查询和 DTO 均保留字段。插入追加在既有参数末尾，不打乱账务参数位置。
- 迁移 `backend/migrations/241_usage_log_first_response_ms.sql` 增加可空列，保留旧显式 SQL 兼容。当前由 SQL repository 管理该扩展字段；若以后转入 Ent，必须同步生成描述符并验证索引，不能直接往旧 Ent schema 中插字段。
- 入口：`backend/internal/service/{openai_early_sse_heartbeat,openai_early_sse_space,usage_log,openai_gateway_usage,gateway_usage_billing}.go`、handler usage context、`backend/internal/repository/usage_log_repo_{insert,query}.go`。
- 2026-10-02 上游新增 TypeSafe `/v1/systemone` 为非流式独立平台，不注入 SSE 或虚构首包值；其异步用量任务入队前必须快照渠道用量字段，闭包不得再访问原 Gin context。沿用 detached usage context、预留引用和原上游计时，扩展该入口已有回归并纳入候选及正式发布 race。
- 验证：`TestPKFirstResponseSurvivesDetachedUsageContext`、`TestPKUsageFirstResponseInsertPaths`、integration `TestPKUsageFirstResponsePersistence`，以及 scan/insert shape 现有用例。

2026-10-26 合入 v0.2.18：PK-009 的异步上下文边界覆盖原生 Anthropic 兼容 Chat/Responses 新共用 `submitForwardUsage`。在入队前同步保存纯值 `ChannelUsageFields`，成功及中断用量计费 worker 不再访问可回收 Gin context；原首包原子状态、预留引用、计费去重与 nullable 持久化保持。仅静态核对，未运行测试或构建。

## PK-010

2026-10-31 pk.25：普通用户和管理员的“首字”沿用该请求持久化的展示口径；关闭提前响应的新请求等于原上游首字，开启时仍为真实下游首包。管理员额外上游字段/双列保留，普通用户不新增管理员专属字段；历史不重算。

**首字显示、导出与上游指标权限。** 引入：`54b4372c2`，完善：`41958f4d4`。

- 普通客户文案继续为“首字”，普通 DTO 的 `first_token_ms` 映射实测 `first_response_ms`；没有测量值显示 `-`，不能 fallback 到真实上游指标。
- 管理员使用记录保留“首字 / 上游首字 / 耗时”，管理员 DTO 保留原 `first_token_ms` 并另给 `first_response_ms`；管理员导出分别保留首包和上游首字两列，字段顺序与标题一致。共享表格默认不展示上游，管理页面显式开启。
- 普通用户 API、详情、CSV 与渠道监控都不能绕过权限暴露原上游 TTFT。公开渠道监控去除原 TTFT 分位数并重算不含 TTFT 的健康分数，管理员数据保留。
- 监控的 Snapshot、Trend、Models、Matrix/buckets、Users 都要覆盖，连同 sample count、TTFT health/score 一起清理。隐私映射不随提前心跳开关关闭而恢复；未采集的非流式/WS 等用户首字可能为空。
- 上游计时沿用原 semantic/visible 定义；部分桥接取首上游事件，不承诺等于首段可见文字时间。
- 入口：`backend/internal/handler/dto/{types,mappers}.go`、`backend/internal/service/channel_monitor_v2.go`；`frontend/src/components/admin/usage/UsageTable.vue`、`frontend/src/views/admin/UsageView.vue`、类型和中英 dashboard 文案。
- 2026-10-01 PK-023 在币安菜单嵌入同一管理员使用记录页；嵌入模式沿用管理员 DTO、筛选和首字/上游首字展示权限，独立 `/admin/usage` 入口的原有默认行为保留。
- 验证：`TestPKUsageFirstResponsePrivacy`、`TestPKChannelMonitorPublicLatencyDoesNotExposeUpstreamTTFT`、`UsageTable.spec.ts`。前端该文件的 CI 纳入情况见文末。

2026-10-30 合入 v0.2.19 时扩展：上游新增的受限管理密钥 `usage.read` 响应保留管理面 `first_token_ms`，同时显式加入 `first_response_ms`，避免遗漏双计时。只扩展已授权用量响应字段，不扩大密钥路由或关联资源读取范围；嵌套用户、Key、账号、订阅继续剥离。既有 `scoped_admin_usage_response_test.go` 增加不同双计时值断言但本轮未执行。

## PK-011

**提前心跳与排队、周期保活及换号兼容。** 引入：`54b4372c2`，修正：`41958f4d4`。

- 队列心跳、compact/直通原有保活和提前心跳独立计数；从正文提交判断中分别扣除且不重复扣。只有心跳时仍可按原规则换号/回退。
- 提前控制器与原有心跳共用调度，完整注释/已知 ping 不终止后续周期保活；真实正文、部分或混合业务帧应停止提前心跳，避免插入正文中间。
- `downstream_ping` 模式下，更早的队列/周期注释不消耗待发送的首次数据 ping；外部完整 ping 可以满足首次发送。内部数据 ping 的字节和原注释一样从本层正文提交判断中扣除，不能重复扣外部队列心跳，也不能改变本层安全换号。
- `space` 的占位是正文，实际成功写出后纳入业务已提交判断，不能继续按“只有保活”重试或换号。等待占位前的注释仍按原保活规则处理；comment/ping 的既有字节扣除和安全换号行为不变。这是用户要求正文占位带来的明确边界，不得误用于旧模式。
- space 在占位前发生安全重试时，须在同一写锁内确认尚未写出占位并取消本请求剩余占位，再进行重试；保存设置不变。不能仅靠“转发前后字节数相等”或分离的布尔检查，因为排队/退避期间定时器也可能写出；不能在新尝试中沿用旧 response.id。
- 默认 `comment` 模式的外部排队心跳取消待发送的首次心跳；`downstream_ping` 按上条区分注释与数据 ping。共享控制器仍可接受之后的周期保活，关闭提前开关时也必须成立。只有心跳时 adjusted size 归一为 `-1`；外部注释字节由原调用方计数，writer 不重复累计。
- `Written()` 必须继续表示真实 HTTP 提交状态；只有“是否已输出业务正文、是否允许换号/重试/fallback”的判断使用一致的 adjusted size。Handler 到转发 service 的相关业务判断都要保留，不能遗漏桥接或重试分支，也不能把 HTTP 已提交状态改成未提交。
- 保留首输出超时的安全标记、最多一次安全切号和客户端取消阻止重放；已输出语义正文后，不进行会重复输出/收费的重放。
- 心跳不改变缓存转换、模型正文、usage 和账单，不以排队心跳冒充真实上游首字。
- 上述“心跳不改变正文”适用于 comment/ping。space 只允许登记的一次空格及协议索引/生命周期适配，不得改写真实模型内容、工具参数或原上游计费证据。
- 入口：`backend/internal/service/{openai_early_sse_heartbeat,openai_early_sse_space,openai_compact_sse_keepalive,openai_gateway_forward,openai_gateway_service,openai_gateway_request_body,openai_gateway_chat_completions,openai_gateway_passthrough,openai_gateway_grok}.go`；`backend/internal/handler/gateway_helper.go` 及两个 OpenAI handler；`backend/internal/repository/http_upstream.go` 与 `backend/internal/pkg/ctxkey/` 的请求级重试门控。
- 验证：`TestPKEarlyHeartbeat_AdjustmentCombinesStreamComments`、`ExternalHeartbeatAllowsLaterPeriodicKeepalive`、`OnlyCompleteTransportFramesPreserveController`、`QueueAccountingWithoutController`、`ReusesExistingPassthroughKeepalive` 等同前缀用例。
- 空格重试回归在原有 `openai_gateway_first_output_timeout_test.go` 的 `TestPKEarlySpaceFailover*`、`http_upstream_test.go` 的 `TestPKEarlySpaceGrokTransportFallbackHonorsRetryGate` 和上述 service 回归文件中。覆盖排队期间先写空格、SafeToFailoverAfterWrite 不能越过正文、原子取消未发占位、transport 不重发及 comment/ping 原行为；均纳入现有 race 选择器。

- 2026-10-15 上游新增“结构化 input 被拒绝时回退 Chat”入口：扣除 comment/ping 心跳后判断正文，回退前调用 `PrepareEarlySSESpaceRetry` 原子取消尚未发送的占位；已发送空格则禁止重试。回归在原 `openai_gateway_chat_completions_test.go`。

## PK-012

**提前 HTTP 提交后的错误与并发安全。** 引入：`54b4372c2`，修正：`41958f4d4`。

- 未发送正文的快速错误保留原 HTTP 状态和 JSON。心跳已提交 HTTP 200 后，晚到错误使用入站协议对应的 SSE 终止事件：Responses、Chat、Messages 不能混发，也不能在流后追加裸 JSON。
- Header 克隆隔离定时器与业务修改，写锁串行化输出；停止等待定时 goroutine 退出并还原 writer，之后才允许 handler 返回、Gin context 回收。
- timer 不回读变化中的 Gin context；取消、结束、错误及 Hijack 都要停止等待，Stop 可重复调用。错误转换只发送一次终止事件，保留原拟返回状态的 Ops 错误标记。
- 2026-10-02 增加短写保护：底层返回不足长度但未报错时转为 `io.ErrShortWrite` 并停止定时器，不把半帧当完整 ping 或成功错误转换；首包仍只按实际非空写出计时，心跳只累计实际写出字节。
- space 的 SSE 解析须覆盖分片、CRLF、多行 data 及工具调用；定时器不能插进半帧。占位后的序号、终态快照及晚到错误必须使用一致的真实 Responses ID，短写/取消仍停止；格式错误不能静默输出错乱生命周期。已有计时、锁与隐私边界继续保护。
- 保留 Ops 流错误/已提交标记；后到动态上游响应头已无法补发，这是提前响应的边界。
- `Server-Timing` 中间件只在真实 `/api/v1/` UI 路径启用；伪造 UI 请求头不能在模型网关启用并产生并发写入冲突。
- 入口：`backend/internal/service/{openai_early_sse_heartbeat,openai_early_sse_space}.go`、`backend/internal/handler/gateway_helper.go`、`backend/internal/server/middleware/server_timing.go`。
- 验证：`TestPKEarlyHeartbeat_FastErrorKeepsHTTPStatusAndJSON`、`LateJSONErrorUsesProtocolTerminal`、`QueueOnlyLateJSONErrorUsesSSE`、`HeaderMutationDoesNotRaceTimer`、`CancellationStopsPendingTimer`、`StopRestoresWriterAndPendingHeaders`；本地 race 检查与 `TestServerTimingScopesAndRoleGate`。

## PK-013

2026-11-02 v0.2.19-pk.42：生产版本检测固定来源改为用户指定的新私库 `yb1320057104/sub2api`；旧仓库缓存因repository不匹配而拒绝复用，保持release构建/正式标签过滤与认证下载边界。详见仓库迁移核对记录。

**PK版本标识与修订排序。** 引入：`85d690928`。

- 页面保留“版本号 + PK出版”。机器版本采用 `上游版本-pk.N`，Git 标签可带 `v`，镜像标签不带 `v`；中文不进入机器版本号。
- 更新判断和回退排序按修订数字比较，`pk.10` 大于 `pk.2`；不能仅比较上游版本或按字典序排序。
- 回退只接受私库正式 `vX.Y.Z-pk.N` 标签，读取最近30个 Release 后过滤草稿、预发布、重复项及不旧于当前的版本，降序最多3个；远程回退目标必须在允许列表中，支持可选 `v` 前缀。在线更新、远程回退和本地 backup 回退均要求 release 构建；source 构建即使直接调用 API 也会拒绝。这不是完整 SemVer 实现。
- 入口：`frontend/src/components/common/VersionBadge.vue`、中英版本文案、`backend/internal/service/update_service.go`。
- 验证：`TestUpdateServicePrivateRevisionUpdates`、`TestUpdateServicePrivateRevisionRollbackOrder`。其他模块同名版本比较测试不能代替这两个更新器场景。

2026-10-26 合入 v0.2.18：上游标签内 VERSION 仍为0.2.17，本次在 PK-013 范围内将源码版本对齐0.2.18；Jenkins 正式机器版本由 `v0.2.18-pk.1` 标签注入，页面PK标识、更新排序和回退边界保持。

## PK-014

2026-11-02 v0.2.19-pk.42：私有源码、Release和GHCR现行命名空间迁至`yb1320057104/sub2api`，旧来源仅作为不可改写的历史证据保留；新下载不回退旧库或上游。

**私有来源、凭据与缓存隔离。** 引入：`85d690928`、`e67a95f2e`。

- 源码、Release、更新、网页回退和恢复固定使用 `yb1320057104/sub2api`；镜像为私有 `ghcr.io/yb1320057104/sub2api`，不可访问时不回退到官方来源。
- 运行时 `UPDATE_GITHUB_TOKEN` 读取私库 Release 和资产；宿主机 GHCR 登录使用独立的包读取授权。开发者宽权限 token 不复制到生产，不进入 Git/日志。
- 更新缓存带仓库标识并校验正式 PK 版本；旧官方缓存、裸版本和其他后缀不得变成 PK 更新来源。
- 通用 GitHub 元数据客户端还服务于 Codex/Claude 版本同步，不能将所有元数据查询误锁为本私库；私库限制针对本应用升级/回退资产。
- 入口：`backend/internal/service/update_service.go`、`backend/internal/repository/github_release_service.go`、`VersionBadge.vue`、各正式 Compose、`.env` 示例、安装/恢复文档。
- 验证：`TestUpdateServicePrivateRepositoryCacheIsolation`、`PrivateCacheRoundTrip` 和 GitHub 客户端授权用例；操作前分别核对仓库与镜像私有状态。

## PK-015

2026-11-02 v0.2.19-pk.42：网页更新/在线回退的Release和asset白名单随固定新库切换，保留token脱敏、重定向/CDN授权边界、SHA256、归档校验及本地backup限制，不增加任意仓库配置。

**安全下载、SHA256 与重复更新路径。** 引入：`85d690928`；固定安装路径修复：`0121f7479`。

- 私有资产 API 正确携带凭据；离开受信 GitHub API 来源后去掉授权头。保留 URL 限制、重定向次数、下载大小等检查。
- 资产 API 优先于匿名下载 URL，使用 octet-stream；初始及每跳 URL 都校验准确 HTTPS 主机、本私库资产/下载路径、端口和 userinfo 等。拒绝伪造子域、路径型资产名；重定向最多10跳，归档上限500 MiB、checksum 上限1 MiB。
- Release 必须存在对应架构归档及 `checksums.txt`，资产 API 下载仍使用原资产文件名匹配 SHA256，不能省略校验或使用 API 数字 ID 当文件名。
- 更新/回退在原安装文件所在文件系统内暂存、备份、原子替换，保留恢复路径；重复更新和回退始终写原安装位置，不因运行中可执行文件带 `(deleted)` 变成另一路径。
- 无法可靠解析原路径时停止替换，不能猜测路径继续安装。
- 安装路径在服务创建时解析一次（含符号链接和绝对路径），更新/远程回退/本地 backup 回退共用；保留归档路径穿越、文件类型和解压大小限制。先备份旧程序再放入新程序，第二次 rename 失败尝试恢复。
- 入口：`backend/internal/service/update_service.go`、`backend/internal/repository/github_release_service.go`。
- 验证：`TestGitHubReleaseClientPrivateAsset*`、`TestGitHubReleaseClientRedirectAuthorization`、`TestGitHubReleaseClientPrivateChecksumSizeLimit`、`TestUpdateServicePrivateAssetAPIPreservesChecksumFilename`、`RejectsReleaseWithoutChecksum`、`RepeatedUpdatesAndRollbacksKeepInstallPath`、`ExecutableResolutionFailurePreventsReplacement`（后四项同 `TestUpdateService` 前缀）。

## PK-016

2026-11-02 v0.2.19-pk.42：镜像标签/来源label、Compose、安装器及发布归档生成器统一新私库；旧封存安装器和manifest原位保持，不重标为新源产物。浏览器外置/同容器程序更新和监督器语义保持。

2026-10-31 v0.2.19-pk.14：网页更新恢复为普通单文件主程序，不再附加或启动解压 Chromium。外置浏览器在应用运行环境独立安装，普通容器重启与网页更新不删除它；重建容器需重新安装。移除启动解包依赖，不改原数据卷、镜像种子、备份、校验和回退语义；保留历史资源与数据，不自动清理。下文 pk.11 自带资源规则属于历史版本。

2026-10-31 v0.2.19-pk.11：网页更新的sub2api单文件同时携带浏览器压缩资源与长度/SHA256尾部。兼容已部署旧更新器，无需先升级更新器或手动换镜像；正常重启在原数据目录browser/摘要下校验、暂存解压、修正预留加载器路径后原子启用。只读缓存复用，目录按资源版本隔离，不删除回退资源，不改原镜像种子、备份和重启窗口语义；源构建无资源时仍允许原系统浏览器路径。

**Docker内网页更新及持久化。** 引入：`85d690928`；镜像回退完善：`c54ac2264`。

- `SUB2API_DOCKER_SELF_UPDATE=true` 时，以普通应用用户运行可写数据卷中的 `/app/data/bin/sub2api`，镜像原始 `/app/sub2api` 保持只读；无需 Docker socket。
- `/app/data/bin/.image-seed.sha256` 记录镜像种子。同镜像重启/重建保留网页更新后的程序；种子不同则原子安装所选镜像二进制，包括旧镜像。
- 同一镜像的普通重建不等于回退。需强制重置时，只对准备恢复的独立副本，或已获准停机后的应用数据处理种子标记，不能删除数据卷。
- 同种子主程序缺失时，可恢复可执行普通 backup；拒绝 runtime 目录/程序/marker 符号链接，校验类型与可写性，同目录 staging 先发布程序再发布 marker。自定义启动命令保持原路径，不强制替换为应用启动。
- 网页下载与“立即重启”分开，`restart: unless-stopped` 负责恢复进程；端口和反代配置不被网页更新修改。单进程重启会断流，不是自动热切。
- 2026-10-23 pk.9修复：网页重启发送SIGTERM进入主程序既有HTTP停止及后台Cleanup流程，保留发送响应的延迟和Linux运行条件；不能直接`os.Exit`跳过defer。系统管理器仍按既有策略恢复进程；信号发送失败只记录，不回退为强退。首次安装阶段尚无业务后台，继续兼容其退出后重启流程。此修复不改变下载来源、SHA256、替换/回退、系统操作锁或管理权限。
- 入口：`deploy/docker-entrypoint.sh`、各 Dockerfile、正式 Compose、`VersionBadge.vue`、`backend/internal/pkg/sysutil/restart.go`及`backend/cmd/server/main.go`正常退出链；运行库/资源/entrypoint 变化必须部署对应镜像。
- 验证：本地 `artifact-runtime` 套件中容器启动、同种子保留、换种子恢复检查，以及隔离环境的重复更新/重启；必须确认运行程序版本，不能只看容器镜像标签。

## PK-017

2026-11-02 pk.42 非 Jenkins 发行：按用户当前无 Jenkins 的明确说明，新增 `deploy/local-release.py` 的 check/publish 入口，复用 manifest、SHA256、同制品十一项验收及私库正式 tag/main 校验；只接受标记真实本机构建和服务器隔离验收的产物，草稿上传程序与镜像归档后逐项核对大小和 GitHub SHA256，再复查来源并发布。凭据只从 `PK_GITHUB_TOKEN` 环境读取；失败保留草稿，重跑不覆盖或自动重发。不执行 GHCR push、Docker 或生产操作，既有 Jenkins 注册表门禁不变。必要构建的 follow-redirects 升至1.16.2，锁文件最小更新，保留既有审计例外边界。

2026-11-02 v0.2.19-pk.42：Jenkins可信SCM、prepare/seal/publish、私有GHCR包及正式tag/main核验切换新库。新增固定repository_policy仅在显式读取/验证旧封存时接受旧库，所有证据仍与原manifest绑定；新发行拒绝旧身份。首次GHCR包仍须先初始化并确认private，不因404绕过版本占用或同次十一项制品验收。原有断言同步但本轮未运行测试；详见docs/PK_REPOSITORY_MIGRATION_PK42_REVIEW.md。

2026-11-01 v0.2.19-pk.39：Jenkins Agent模板必须保留init:true，PID 1由docker-init/tini承担，Java在其下运行。已有环境应用配置须只重建空闲Agent，普通restart无效；保留现有镜像、全部挂载/secret及节点配置，禁止借此重建controller/专用daemon或重跑bootstrap。维护流程先记录原临时离线状态并暂停接收任务、复查普通/一次性执行器和队列，备份并仅更改init，使用无构建/无拉取/无依赖重建，再核对进程关系、其他容器和节点配置，按原状态恢复。应用发行不自动同步Jenkins基础设施。init回收被收养的孤儿，不替代存活父进程等待普通子进程；保留原十一项制品验收及私库发布门禁。

2026-10-31 v0.2.19-pk.14：三种应用镜像均移除 Chromium，Jenkins 不再导出浏览器根文件系统、修补加载器或附加 payload，也不再生成/发布重复包含镜像的完整部署 tgz。恢复原十一项同制品运行时验收、私库来源、SHA256、tag/main 归属与版本占用门禁。旧 pk.10～13 的 deployment profile 和 pk.11～13 的 browser profile 仍强制校验；已声明的 profile/证据不能剥离，旧封存包恢复/发布仍验证原检查。新版可信打包入口拒绝以缺 profile 重新构建这些历史版本，不能冒充旧浏览器版本。用户明确授权本轮执行一次 Jenkins 正式打包并比较同平台 linux_amd64.tar.gz；不运行本地源码构建/测试。

2026-10-31 v0.2.19-pk.11：在可信Jenkins打包入口从同次runtime-base的独立导出stage获取Chromium、加载器、字体及库；patchelf仅在构建stage预留解释器路径，最终镜像原生浏览器不被改写。资源附加到一次编译的主程序后，再统一生成镜像/网页归档/完整包及摘要。新增pk-browser-in-binary-v1和浏览器摘要绑定；旧更新归档仍仅sub2api，主程序及归档各不超过500MiB。新profile在原十二项基础上新增browser-web-update（合计十三项）：仅将更新归档主程序复制进无Chromium的裸Alpine3.21，非root、无网络、无系统安装，验证自动解压、本地页面渲染及第二次启动复用；失败不得封存发布。旧版本原profile及可选旧制品入口保持兼容，不剥离新证据降级。本轮只静态核对，实际构建验收待用户Jenkins执行。

2026-10-31 v0.2.19-pk.10：Jenkins在原同次二进制/镜像基础上生成完整部署包，包含原样镜像归档、受保护Compose模板和初始化入口；Chromium与全部运行库在镜像中，不另编译或下载浏览器。manifest增加pk-browser-deployment-v1、整包和成员摘要，inventory/seal/restore/ready/publish及可选本地制品入口统一校验。保留原十一项并新增隔离browser-runtime：非root、无网络、只读镜像，在临时目录渲染本地data页面，结束清理；摘要绑定同次证据后才发布完整包。旧包仍按原十一项/原profile核验，不能剥离新profile降级。完整包名称不含旧更新器选择串linux_amd64，避免误选；checksums增加完整包行，原网页更新包布局不变。仅静态核对，未执行本轮构建/验收。

**私有发行与可用部署入口。** 引入：`85d690928`、`e67a95f2e`；文档：`23e5fe663`。

- 2026-10-21 用户明确要求移除完整源码测试和 Git notes 门禁，以后直接交付无 `origin/` 前缀的正式标签。本扩展目标 `v0.2.17-pk.4`，继承 pk.3 首页及 pk.2 全部功能，不移动已推送标签。默认流程为保护项/差异核对 → 提交/合入私库 main → 推送未占用正式 `vX.Y.Z-pk.N` 标签并核验远端 commit → 用户点击 Jenkins `release` → build/inventory/十一项运行时验收/seal/publish；不自动部署生产。
- 新包使用 `validation_profile=pk-artifact-only-v1`，不 fetch/read 源码 notes、不生成虚假的源码通过报告，不要求 `source-validation.json`。制品报告仍为 `pk-artifact-v2`，严格绑定仓库、commit/tree、package_id、版本、二进制/归档/镜像身份及哈希；十一项检查和清理必须真实通过。旧 `pk-source-artifact-v2` 封存包继续按原规则核验源码报告哈希、notes 出处和运行时对源码哈希的绑定；旧 `pk-local-v1` 手动流程继续兼容，不能用新 profile 掩盖旧包证据缺失或篡改。

- 2026-10-08 Jenkins 缓存与打包清理修复：npm/npx、pnpm 内容存储、Go modules/build 使用专用 daemon 的持久命名卷，按 `release` / `manual-release` / `candidate`、工具链、平台及 UID/GID 分隔，不缓存项目 `node_modules`、发行结果或测试证明。卷身份与 local driver/无外部挂载选项必须核对；初始化只修改挂载根目录属主，构建仍非 root 且不挂凭据、socket 或生产数据。保留 frozen lockfile、pnpm 完整性与复制导入/禁用 side-effects cache、Go module verify 及 readonly 编译。首次冷缓存仍下载，后续仅复用有效内容。清理阶段仅恢复 HEAD 已跟踪的 `.keep`，不因源码本就没有该占位而失败，真实 Git 错误仍终止，其他源码变更继续由 seal 拒绝。既有 `test_release_matrix.py` 增加真实临时 Git 场景，覆盖占位不存在/被删、其他变更保留和恢复失败；缓存复用需另用两份干净源码及第二轮断网进行实际验证。

- Jenkins 从指定私库构建，手动选择已审阅的提交/分支或未复用的 PK 标签；同一构建二进制进入 Docker 镜像和 Release 归档。GitHub 保留私有源码、Release 和 GHCR 存储职责，不执行构建或测试。
- `SOURCE_REF` 保留 Git Parameter `PT_BRANCH_TAG` 可搜索分支/标签选择，分支值为 `origin/分支名`，正式标签为 `vX.Y.Z-pk.N`，统一规范化为完整 ref。2026-10-08 默认动作改为 `ACTION=release`：必须选择正式 `refs/tags/vX.Y.Z-pk.N`、属于私库 `main` 历史且版本未占用，拒绝 `COMMIT_SHA` 覆盖；列表中的默认分支不能直接发布。`ACTION=package` 保留分支及 40 位小写 SHA 候选打包、正式标签手动打包，`COMMIT_SHA` 只在该动作覆盖源码选择。候选不进入正式更新列表，被打包源码不得改变加载可信流水线的 SCM 分支。
- 按 2026-10-03 用户追加要求，Jenkins 仅监听服务器 `127.0.0.1:18081`，不开放公网端口；域名反向代理由用户配置。原网页一键更新、版本回退、私库授权下载和 SHA256 校验能力必须保留，迁移只改变构建与验证执行位置。
- 2026-10-03 用户要求 Jenkins 界面使用简体中文：安装 Locale 与简体中文语言包，保存默认语言 `zh_CN`，忽略浏览器语言偏好并关闭用户语言覆盖。首次安装脚本保留同样默认值；不改变应用自身语言、权限或打包发布流程。
- 标签必须指向私库 `main` 历史内的目标提交，已有 Release 不覆盖；发布串行执行，镜像写入来源/版本/revision 标签并核对包私有性，通过独立 Jenkins 发布凭据授权，读取源码使用只读 deploy key；不复制开发者宽权限 token。缺发布凭据时可打包，但不得称为已发布。
- Release 是正式发布而非 prerelease，带 `sub2api_版本_linux_amd64.tar.gz` 和 `checksums.txt`。当前仅发布 `linux/amd64`；Apple/arm64 必须先发布并验证相应私有镜像，不能替换成上游镜像。
- 历史规则（已由 2026-10-21 用户要求替代）：2026-10-08 曾将完整源码测试前移到提交前，并要求通过后才提交、绑定 notes 和发布；该前置现已取消。源码测试入口保留为可选诊断，不自动运行、不把未执行记为通过；仍不在本地编译发行程序。
- 同日用户指定服务器测试，默认以控制端 Python 3.12+/Paramiko 执行 `scripts/test-server.py`；仅上传 HEAD bundle 和暂存树 Git 对象包，远端解包后直接恢复暂存树，保留原始 blob、文件 mode、tree 与 base HEAD，不重新 `git add` 规范化或生成临时 commit。每轮使用独立 UUID、8 CPU/16 GiB 的 DinD 测试环境及目录，工具镜像来自 `deploy/jenkins/Dockerfile.source-tests`，不编译发行程序。SSH 主机指纹必须已核验，凭据文件须 Git 忽略且未跟踪，不传容器；测试源码、数据库/Redis 与夹具隔离，不挂宿主 socket/生产卷或影响生产服务。完成回传报告和原日志，清理本轮容器、匿名 daemon 卷及远端目录。报告注明 `execution_environment=server-isolated`，不得写成本机通过。本机 Linux/WSL 的 test-local 入口仍可用。
- 默认 `local-validation.json` 为 `schema_version=2`、`tests_profile=pk-source-v2`，含九个源码套件 `backend-unit`、`backend-integration`、`backend-race`、`backend-lint`、`frontend`、`shell`、`binance-proxy`、`release-helpers`、`security` 及独立 preflight；保留真实命令、日志 SHA256、前后相同的暂存 tree/工作区快照、base_commit 和执行环境。报告绑定可由 Windows Python 执行，不要求 WSL。
- 选择保存可选源码证据时，可用 `--bind-commit HEAD --report ... --publish-evidence` 将真实通过的原报告绑定到相同 tree、包含 base_commit 的干净最终 commit，并通过私库 `refs/notes/pk-source-validation` 发布。notes 不强制覆盖、不挪用其他提交证据；已绑定报告仅可重绑到同 tree 后代，保留原日志和执行时间。该证据不是新包提交、合入、打标签或正式发布的前置条件。
- 当前正式流程为正式标签/私库来源核对 → build → inventory → 专用 daemon 制品运行时验收 → seal → publish。`pk-artifact-v2` 十一项检查验证同一次归档/镜像、非 root 执行、可写卷、同种子保留、backup 恢复及种子切换等边界。运行时容器不接生产或发布凭据，使用隔离 daemon 和可清理测试卷。完整证据绑定 package_id、二进制/归档/镜像哈希；同次发布原制品，不重新构建、不自动部署生产。
- 旧手动 `package` → 下载同包 → 本地 `--artifact-dir` → 上传报告 → `publish` 仍兼容：`pk-local-v1` 十套件（含 `artifact-runtime`）必须全部通过且 commit/tree/package_id/制品 SHA256 一致。`publish` 可处理已封存但尚未对外发布的包；若已推送镜像、产生草稿或版本被占用，先核查阻挡和日志，不声称自动恢复或覆盖重发。候选不能由此转为正式版。
- 临时测试卷、种子变更夹具和新生成测试文件用完删除，不使用生产数据库/数据卷；仓库原有回归保留。Apple 部署脚本沿用 fake runtime 回归，在 Linux 上执行不等于真实 macOS/Apple runtime 验收。本机缺 WSL Linux 发行版和 Docker 后，用户已改为授权服务器隔离验证；新入口及文档不等于动态通过，实际结果以真实环境报告为准，凭据不进入清单/文档/skill/提交。
- 2026-10-01 币安充值接入：私库 `codex/` 分支和原 `feat/` 分支均生成候选制品；正式发布前增加币安协议、余额事务与并发入账检查。前端 Axios 升至 1.20.0，修复旧 1.18.1 的七项 high 安全告警，保持原有 API 客户端与刷新令牌行为，不为二开新增或扩大审计豁免。上游原有功能的审计策略按 2026-10-08 用户要求完整同步，范围见下条及项目约束第 3 节。
- PK-025 接入时，原 CI 和正式发布增加独立网关源码检查，验证认证、香港地域、目标白名单、每次探测与固定节点、错误及超时；2026-10-21 后该源码检查随可选套件保留，不再单独阻止正式发布。网关运行时行为未改动，脚本随私库源码管理，服务器凭据单独保存。
- 2026-10-02 上游同步分支 `sync/` 同样运行私库候选构建；候选版本从 `backend/cmd/server/VERSION` 读取并追加 `-pk.candidate`，仍不进入正式更新列表。前端关键回归增加上游充值优惠、金额组件、账号优先级、平台/配额及仪表盘测试，保留原有币安、首字与使用记录覆盖；TypeSafe 异步用量上下文回归加入候选和正式发布的 race 检查。
- 2026-10-03 耻辱排行榜接入：候选和正式发布执行关键前端回归，以及 `TestPKCyber*` 的 unit/race 和真实 PostgreSQL integration/race 检查，覆盖 PK-026/027；发布仍不操作生产。
- 原 `install.sh`、`docker-deploy.sh` 的不兼容旧安装入口保持停用。全新部署用私版完整 Compose；已有环境的 `docker-compose.pk.yml` 是 overlay，不能替换整套生产配置。
- 2026-10-08 合入上游 v0.2.14：保留私版停用脚本与部署来源，将新初始化规则同步到私版指南及三语 README。全新自动初始化的空邮箱/密码随机生成，显式邮箱须符合登录格式、密码为 8–72 字节；先检查已有用户并跳过创建，不能重置已有管理员。Compose 邮箱默认值改为空，原私有镜像、token、数据卷和网页更新选项保留。
- 同次依赖合并保留 Axios 1.20.0，接受上游 Vue 3.5.43、source-map-js override 及对应锁文件；前端运行行为待验证。2026-10-08 用户要求上游全部更新合入，并将“不扩大豁免”限定为二开功能，因此两条 xlsx 例外的理由、缓解措施及期限 `2027-01-06` 按上游原样同步；仅适用于上游既有只写导出，不新增或豁免二开特有解析/权限风险。安全套件仍须按实际结果处理，不把静态核对称为审计通过。
- 被保留的脚本 API helper 仅接受本私库 Release 元数据端点，凭据经 stdin 注入，不接受任意 URL/重定向参数；source 后直接调用旧 downloader 也不能绕过停用。Apple restart 必须在停服务前验证显式私有 ARM 镜像配置，不能将 `latest` 当作已支持 ARM。
- 管理员更新/回退/重启保留原认证、审计和系统操作锁；UI 在线升级仅面向正式 release build，候选不能冒充正式升级包。
- [中文部署指南](deploy/PK_DEPLOYMENT_CN.md)、README、[私有镜像说明](deploy/DOCKER.md) 和 [维护文档](PK_MAINTENANCE.md) 随源码保存；新部署使用自己的数据/账号/密钥，程序没有固定服务器 IP 或域名限制。
- 入口：`Jenkinsfile`、`deploy/jenkins/{control.py,package.sh,verify-runtime.py,Dockerfile.source-tests}`、`scripts/test-server.py`、`scripts/test-local.{ps1,py}`、Dockerfile、`deploy/` 下相关脚本、Compose 与文档。验证边界见下文，不能把“发布已完成”当成“线上已更新”。

- 2026-10-15 跟随上游 Go 1.27.2 / golangci-lint 2.14.x，服务器工具镜像更新为 `pk-source-tests:20261015`；上游 Actions 中的源码/部署/security 检查仍由现有九套件执行，关键前端列表合并保留双方条目。
- 2026-10-21 为通过提交前安全门禁，按 Go 官方 2026-10-19 补丁发布将当前 Go 工具链统一为 1.27.3，修复扫描命中的 `GO-2026-6809`（`crypto/x509`）。同步 `backend/go.mod`、三个应用 Dockerfile、源码测试工具镜像、严格 preflight 版本与维护命令；新工具镜像为 `pk-source-tests:go1.27.3-20261021`。不添加漏洞豁免、不关闭扫描，不在本地构建应用发行物，正式发行仍走 Jenkins。
- 同日分支清理后，`deploy/jenkins/bootstrap.groovy` 的初始化 SCM 同步为 `*/main`，与实际 Jenkins 任务一致，不再指向已停用的维护分支。`Jenkinsfile` 的 `origin/` 分支识别、正式标签识别和发布门禁保持不变。
- 同次验证将原有 `TestServerTimingConnectorRecordsDriverCallsWithoutRowLifetime` 放入标准库 `testing/synctest` 虚拟时钟，保留原睡眠时长、应用时间/数据库时间关系、查询计数和 SQL 不泄露断言，避免测试服务器调度延迟造成误失败；不修改生产 SQL 包装器、计时采集或响应头输出。
- 同次前端安全扫描命中 Axios 间接依赖 `follow-redirects 1.16.0` 的两项高危公告（`GHSA-q3jh-wrc6-jgq8`、`GHSA-8r9p-f939-6h3c`）；通过现有 pnpm override 机制固定修复版 `1.16.1` 并同步锁文件，不更改 Axios 版本，不增加或扩大漏洞豁免。

- 2026-10-19 合入 v0.2.17：将原有 `StripePaymentView.spec.ts` 纳入 `FRONTEND_CRITICAL_VITEST`，保证订单本地快照/client_secret 绑定的上游安全修复进入默认源码验收。工具链、九套件、notes 和 Jenkins 私库门禁不变；不扩大审计豁免。

- 2026-10-19 确立的正式标签交接继续保留，2026-10-21 取消其中“完整源码测试及匹配 notes”的前置：交给用户点击 Jenkins `release` 前，推送已审阅的 main 提交和未占用正式标签，核对远端标签剥离后的 commit。工作分支不能代替正式标签；交付 `ACTION=release`、`SOURCE_REF=正式标签`，其他两项留空，不默认替用户点击构建。规则同步 AGENTS、项目约束、维护文档及 Jenkins 说明。

2026-10-23 智商测试接入时，服务器安全检查命中 DOMPurify 3.4.14 的新安全公告，按官方修复版本将前端声明、override 和锁文件同步到 3.4.17；不新增安全豁免、不升级无关依赖。SVG 预览的净化、无脚本沙箱和 CSP 继续独立保留，动态结果记录在本次交接记录。

2026-10-26 合入 v0.2.18：PK-017 继续采用既有 Go 1.27.3/Jenkins 工具链，不恢复上游三个 Actions 构建/测试文件。前端保留 follow-redirects 1.16.1 精确覆盖及上游新增安全下限约束，锁文件同步，DOMPurify 3.4.17不回退；不增加审计豁免。仅静态核对，未执行构建、源码测试或制品验收。

同日pk.2按用户明确要求执行服务器隔离回归，保持本机不测试/构建。源码race选择器由PKKiloBridge扩为PKKilo，并增加PKDisabledExtensions；原范围完整保留，普通关闭计费与Kilo RecordUsage回归增加必需匹配要求。新增首字账务用例沿用PKEarly前缀进入unit/race；完整九套件、preflight、真实tree和日志/清理证据要求保持，既有运行时制品门禁不变。没有引入新的默认全套测试前置，当前执行基于本轮用户明确要求。

2026-10-30 `v0.2.19-pk.2`：按用户要求取消 `scripts/test-server.py` 的固定服务器地址和 SSH 用户名限制，连接目标只读取已有 Git 忽略的本机凭据文件，并检查 host/username/password 为非空字符串。保留凭据不跟踪、SSH 已知主机指纹校验、隔离 Docker、源 tree 绑定、报告和清理边界；不自动发起测试或连接。该改动不改变 Jenkins 制品门禁。

2026-10-30 `v0.2.19-pk.3`：用户要求服务器隔离回归，本轮将 `TestPKAccountCandy` 加入既有 race 选择器和必须匹配检查，保留所有原有选择范围。修复后的源码须用本轮完整报告记录真实结果，专项临时测试与完整九套件证据分别记录，不把含临时测试的 tree 冒充最终源码 tree；本机不运行构建或测试。

同轮完整回归在提交 `845c7160fa7fcb5ce05732b81839aa4699dfc44e` 得到 preflight 通过、九套件七通过两失败：糖果流解析的三处 errcheck，以及前端高危依赖审计。原始失败报告保留，不作为通过证据。安全修复定向参考上游 `main` 的固定提交 `d851a38477597157e78450d30af2d8504764d085`，同步 Vue I18n 11.4.16 与 decode-uri-component 0.5.1 的依赖修复，并同步直接使用的 Intlify 编译器；不整体合入未发行 main、不新增或扩大审计豁免，既有二开覆盖与 xlsx 写导出边界保持。依赖解析和后续验证只在用户指定服务器隔离环境执行，最终结果必须绑定修复后的新 tree，不能复用本轮失败或旧专项报告。

同日用户明确要求 Jenkins 增加 `REINSTALL_NODE_MODULES` 选择项（`否`、`是`，默认 `否`），沿用 PK-017 扩展：
- 默认 `否` 保留冻结锁文件安装及既有依赖缓存边界；显式 `是` 仅在本次 Jenkins 隔离构建目录删除前端 `node_modules` 和 `pnpm-lock.yaml`，随后使用非冻结、在线优先的依赖解析与完整性校验安装。不能用只删目录、保留旧锁或继续离线优先来冒充重新解析。
- 本选项只适用于 `release`/`package`；`publish` 选择 `是` 必须拒绝，不能重新安装或重建已封存包。Jenkinsfile 与初始化参数定义保持一致。
- 可信流水线保全原源码锁与哈希，构建实际使用新锁，记录实际锁、依赖审计、工具链及策略证据。仅恢复经核对的原锁，不恢复其他源码变化；严格源码 commit/tree 与 clean 检查继续保留，恢复或审计失败即失败。
- 默认包保持原 manifest schema；显式重装包用独立 schema 记录有效依赖与证据哈希，并由 runtime、seal、restore/publish 校验。旧封存包继续按原 profile 验证，不能删除声明/证据降级绕过；同一二进制用于镜像及更新归档的要求不变。
- 回归扩展仓库既有发布辅助测试，覆盖两种安装模式、删除边界、原锁恢复、非法 publish 重装及证据篡改；本机不运行构建/测试，真实 Jenkins 发行构建仍由用户亲自触发。

## PK-018

**旁路实例启动时保护现有并发。** 引入：`66671398d`。

- `SUB2API_SKIP_STARTUP_SLOT_CLEANUP=true` 让新实例跳过启动时的全局槽位/等待计数清理，不能删除旧实例真实在途请求的并发状态。
- 默认未设置时保留原启动行为；跳过全局清理后仍运行过期槽位清理，不能关闭正常过期回收。
- 入口：`backend/internal/service/wire.go` 的 concurrency provider。
- 验证：`TestProvideConcurrencyService_StartupCleanupDefault`、`StartupCleanupExplicitSkip`、`StartupCleanupSkipKeepsExpiredSlotCleanup`（同 provider 测试前缀），以及 stale-process cleanup 现有测试。

## PK-019

2026-10-31 v0.2.19-pk.27：单账号立即授权/失败重试提交后唤醒现有授权worker，启动门、停止取消、同机互斥和70360002会话锁不变；手动任务在下一批优先领取，不抢占正在运行的任务。每任务45分钟，消费轮次在45分钟后停止新开批、最迟90分钟上下文截止，给末批完整执行预算且保持数据库调用有界；巡检3分钟和统一重授权10分钟cron不变。

2026-10-31 v0.2.19-pk.26：AccountHealthService 由 AccountUsageService 的provider绑定，接入相同激活门和停止链，wire源文件、生成镜像及原清理回归参数同步。分钟cron加本机互斥及独立数据库会话锁，停止取消查询并等待队列worker；释放失败丢弃连接，避免带锁连接重回池。仅静态核对。

2026-10-31 PK-036 的 AccountAuthorizationService 接入同一启动门与清理链：未激活不消耗任务，停止取消浏览器和探测、等待 worker、释放数据库锁；cron协调与授权消费使用独立锁，长登录不阻塞下一轮巡检。原服务门控顺序不变。

**后台启动门和安全停止。** 引入：`66671398d`。

- `SUB2API_BACKGROUND_ACTIVATION_FILE` 未设置时保留同步启动。设置后，仅在该路径出现普通文件时启动等待中的受控后台；目录/符号链接不充当激活标记。
- 激活只执行一次，不靠重启应用交接；激活后删除文件不暂停已运行任务。
- 停止流程先调用 `StopPendingBackgroundStarts()`，取消尚未启动的任务并等待正在执行的启动回调，再停止各服务。`wire.go` 和 `wire_gen.go` 顺序必须一致。
- 2026-10-23 pk.9将网页重启接入同一退出链：先停止/等待业务后台，最后关闭Redis/Ent；不以`os.Exit`绕过清理。智商测试取消后独立释放自己仍持有的租约，不因结果写入失败而跳过；不清除其它实例或新worker的占位。
- `BatchImageCleanupService` 保留未启动即停止、立即停止、幂等生命周期保护，不能因启动门等待导致退出死锁。
- worker 关闭捕获的局部 `done` channel，不能改回读取可能被 Stop 清空的 `s.done` 字段。
- 当前21项受控后台：BatchImageCleanup、TokenRefresh、CNProviderBalanceCheck、DashboardAggregation、AccountExpiry、OpenAICodexVersionSync、ClaudeCodeVersionSync、ProxyExpiry、SubscriptionExpiry、OpsMetricsCollector、OpsAggregation、OpsAlertEvaluator、IdempotencyCleanup、ScheduledTestRunner、OpsScheduledReport、PaymentOrderExpiry、ChannelMonitorRunner、ChannelMonitorV2Aggregator、BinanceRechargeService、GeminiCachedContentService、IntelligenceTestService。币安轮询随同原后台交接，仍以数据库唯一约束和事务防止重复入账。2026-10-23 新增的智商测试同样经过启动门，退出时取消模型请求并等待工作结束；其他后台的门控顺序不改变。
- 门控不暂停全部任务，不是数据库只读模式；请求处理、必要记账、缓存失效及已有锁保护的必要工作仍应可用。
- 入口：`backend/internal/service/{background_start_gate,wire,batch_image_cleanup}.go`、`backend/cmd/server/{wire,wire_gen}.go`。
- 验证：六项 `TestBackgroundStartGate_*`，以及 `TestBatchImageCleanupLifecycle_StopWithoutStart`、`ImmediateStop`。

- 2026-10-15 新 Gemini 缓存恢复任务纳入启动门；停止取消数据库/上游调用并等待 worker 完成，未启动/重复/并发停止安全。普通客户端缓存修改仍保留上游脱离客户端取消的语义。回归 `TestPKBackgroundGeminiRecoveryStop` 及上游取消传播回归，加入源码 race 选择器。

2026-10-30 新增第22项受控后台 `AccountCandyTestService`：经过 `startBackgroundAfterActivation`，与 `wire.go` / `wire_gen.go` 的 Cleanup 同步接入；退出取消在途探测并等待结果收尾。账号测试不复用或改变分组智商测试生命周期。

## PK-020

2026-11-02 v0.2.19-pk.42：维护文档、版本提示和恢复指引改为新私库来源；旧运行记录保持原事实，生产源启用必须以新程序实际验证为准，不能把修改origin或token当作完成上线。

2026-10-31 v0.2.19-pk.14：新增 deploy/install-browser.sh，由服务器 Docker 管理员对指定 Alpine 应用容器执行。签名 apk 安装 Chromium 及系统运行库，原子写入独立 launcher，非 root 应用用户运行；拒绝覆盖自定义 launcher。安装不重启应用、不改数据或环境密钥。重启/网页更新保留容器安装层，重新创建容器时需再次安装，不能把宿主机安装或镜像更新误称为已完成容器依赖。

2026-10-31 v0.2.19-pk.11：已有官方Linux amd64部署可以按原网页更新/正常重启流程补齐浏览器，无需变更Compose或换应用镜像；保留端口、环境变量、数据库、Redis、数据卷和原JWT/TOTP密钥。额外运行库在应用数据目录，不覆盖系统库；完整部署包继续保留，发布不自动部署。

2026-10-31 v0.2.19-pk.10：完整部署包仅发布到现有私库Release，内含同一受校验私有应用镜像；不要求部署者重新登录GHCR才能导入随包镜像，不引入公共应用镜像或第三方下载源。数据库/Redis仍使用原Compose的标准依赖镜像。

**切换、更新与恢复的运维保护。** 引入：`66671398d`、`d3f5c8a53`，属于运维约束，不是自动零中断功能。

- 保留现有 Compose 参数、应用端口、反代、DB、Redis、JWT/TOTP 密钥和数据卷；旁路候选使用独立可写 `/app/data` 副本，不能与旧应用共写程序/激活标记。
- 保留在途请求时：先验证新实例，再由反代 graceful reload 接新连接，保留旧连接及旧后台。真实连接与异步计费排空、旧后台退出后，再激活新实例后台。
- 共享并发为0、固定等待时长、`/health` 成功不证明请求排空；WS 连接及进程内续链另查。没有完整实例排空接口时，不承诺自动迁移或零中断。
- 只有当前用户接受中断时才硬切或使用网页单进程重启。历史硬切授权不用于未来部署。
- 回退先检查迁移兼容与原密钥，使用可信私库版本和明确备份。恢复旧版本需说明功能差异，源码恢复不得扩大为清空业务数据。
- 入口与验收：`PK_MAINTENANCE.md` 的蓝绿、回退、恢复流程，PK-018/019 启动保护，以及实际部署前的授权范围和隔离验证。

2026-10-30 `v0.2.19-pk.2`：按用户要求从维护文档及维护 skill 移除历史实际部署的地址、域名、端口映射、容器/服务名和专属路径；维护命令改用经当前环境核对的参数。保留历史验收结论和非零中断边界，但不再把旧环境身份作为默认目标。此清理只修改当前源码文档，不连接或变更线上服务。

## PK-021

2026-11-01 v0.2.19-pk.39：用户授权的服务器运维仅覆盖空闲Jenkins Agent进程回收。已实际核验234个僵尸清零、init启用、节点恢复在线、镜像/挂载/节点配置及其他容器保持；首次安全回退与临时离线标记差异如实记录。配置/文档静态检查和现场运行状态不冒充构建或回归通过；没有运行测试、Jenkins作业或业务接口，身份/凭据/专属路径不入库。

2026-11-01 v0.2.19-pk.37：按用户授权只读查账号统计配置、队列、advisory锁及应用/PostgreSQL日志，确认已有cron每分钟触发并因SQL类型错误失败。修复只作格式、差异和调用链静态核对，不执行构建/测试、线上写入、主动统计、Jenkins或部署。

2026-11-01 v0.2.19-pk.36：工作台默认值调整仅做差异、格式和调用链静态核对，未运行构建、测试或真实接口；按既定规则交接版本分支/main/新正式标签，Jenkins由用户操作。

2026-11-01 v0.2.19-pk.35：本轮用户单独授权只读服务器应用日志，与静态代码核对一起定位TLS握手失败；未发真实模型/登录请求，未构建/测试/部署。新源码回归仍未运行；按既定规则提交版本分支/main/新正式标签，由用户自行Jenkins。

2026-10-31 v0.2.19-pk.33：基于用户最新pk.32完成重授权调度恢复、60秒等待及菜单图标修复；仅静态核对，未运行构建/测试或线上调用。保留浏览器源码、部署文档及旧正式标签，交接版本分支/main/新标签，Jenkins由用户执行。

2026-10-31 v0.2.19-pk.31：旧及格选择被移除时提供中英文可见提示及ID，保留账号输入和仍有效选择；请求序号防旧刷新覆盖较新结果，提交前刷新失败不发送任务。仅静态解析/gofmt/diff核对，未运行构建、类型检查或测试，不把源码修复写成线上实测通过。

2026-10-31 v0.2.19-pk.27：三个新变更接口仅完整管理员可用，带独立审计动作，重试/立即授权沿用Heavy限流；无需向浏览器返回密码、2FA或代理认证。列表按安全白名单投影任务来源及代理计数，中英文同步。批量删除固定确认时的选中ID，翻页/筛选清除选择；缺凭据的立即授权按钮禁用并保留补全入口，排队/执行中不提供重复按钮。仅静态解析与差异核对，不宣称构建或真实授权通过。

2026-10-31 v0.2.19-pk.26：健康度配置、雷达进度、列表健康度标签及授权入库出口选项保持既有组件/深色样式和中英文；雷达新接口仅完整管理员可访问，受限管理密钥明确拒绝。刷新/分页仅读存储，不触发统计或外部请求。健康度缺记录/查询失败明确显示，不伪造满分；首字和连续失败采用带背景标签。

2026-10-31 v0.2.19-pk.14：用户最新授权本轮外置浏览器安装及一次 Jenkins 打包，覆盖此前本任务范围内禁止服务器变更/仅用户触发的描述；不扩展为应用升级、服务器迁移或资源配额调整。沿用既有 ID，版本变更、部署限制和真实验收结果保持分别记录。

2026-10-31 v0.2.19-pk.11：用户要求继续补齐网页更新路径，使用新版本分支和新正式标签，不修改已推送pk.10标签。交接ACTION=release、SOURCE_REF=v0.2.19-pk.11，COMMIT_SHA/PACKAGE_ID留空，用户手动构建。未执行构建/测试或线上操作，不把静态核对当成新增十三项制品验收通过。

2026-10-31 v0.2.19-pk.10：完整部署包交接扩展到原Jenkins流程，用户选择正式标签手动release；不自动触发构建或部署。线上仅只读确认旧镜像缺浏览器，本轮不把计划中的新browser-runtime验收记为已通过。新部署入口初始化随机密钥，已有部署仅load-only并按原维护流程换应用镜像，保留项目、密钥和数据。

**持续登记、项目约束和维护 skill。** 原维护 skill：`e67a95f2e`；统一清单与约束：2026-10-01 本次新增。

- 2026-10-19 将 PK-017 的正式打包交接写入持久化入口。2026-10-21 用户进一步取消完整源码测试与 notes 前置：以后直接完成主分支/正式标签推送并核验，再交付 Jenkins 参数，由用户自行点击构建；不再只推工作分支或要求补 notes。AGENTS、项目约束、维护文档、PR 模板及仓库/本机维护 skill 同步新规则。属于原 ID 扩展，无新增二开，仍不允许改写已发布/已推送正式标签。

- `AGENTS.md` 纳入 Git，入口指向 `PROJECT_CONSTRAINTS.md` 和本清单；私库克隆/恢复后也必须保留。
- 2026-10-21 用户要求全面取消分支名前缀，替代 2026-10-03 的 `codex/` 命名规则：`main` 为正式分支，所有工作分支直接使用 `vX.Y.Z-pk.N`，不允许任何目录式/文本前缀或任务后缀。同一未发布版本复用一个分支与 PR，默认只有一个在途版本；功能、修复、文档、上游同步与并行助手不分别建分支。新建前核对本地/远端分支、在途 PR 和发行标签，已有版本分支优先复用。规则同步 `AGENTS.md`、项目约束、维护文档、PR 模板和仓库/安装版维护 skill；无新增 ID，原合入/发布验收门禁保持。
- 本次按用户授权删除本地与私库的 `codex/v0.2.17-pk.1`、`feat/early-stream-heartbeat`、`sync/upstream-0.2.12` 分支引用；三者均已包含在 `b99ca3ab9825cccfe771d3fbf3df620792c0d0e7` 历史中，无独有提交或开放 PR，Jenkins 实际 SCM 已为 `main`。当前版本保留 `v0.2.17-pk.2`，既有标签与提交历史不改写；作者仓库及 Git 的 `origin/`、`refs/heads/` 等命名空间不属于私库分支名前缀清理范围。GitHub 当前私库套餐不支持仓库 Rulesets，本规则通过项目约束和维护流程执行，不宣称已启用服务端强制拦截。
- 本文件为唯一功能登记表。维护文档、PR模板和 `skills/pk-sub2api-maintenance/` 通过链接/工作区路径读取它，不复制多份可能过期的功能清单。
- 2026-10-08 用户明确要求完整吸收上游新功能、安全修复、依赖和上游自身审计策略；“不扩大豁免”只约束二开新增或修改功能，不阻止上游原有功能的例外更新。共享代码须说明上游范围和二开扩展，不将上游豁免套到二开特有风险。已同步 AGENTS、项目约束、维护文档和维护 skill；不改变私库来源、本地禁令及正式合入/发布门禁。
- 新增二开在同一变更中添加 ID；扩展旧功能更新原 ID；合并上游逐个有效 ID 核对，未登记、存在功能丢失或未解决冲突的受影响项不能合入/发布；源码测试是否执行及结果如实记录，不把可选测试或 notes 重新设为前置门禁。
- 2026-10-02 按用户要求强化每次合并的文本冲突和行为冲突检查，覆盖上游新增/替换入口；Git 无冲突不能替代核对。PK-007～PK-012 的三模式、配置、协议 ID/生命周期、真实计时、usage/工具、错误与换号必须整体审查，PR模板记录逐项依据。
- 历史记录（默认流程及禁令已由下文 2026-10-08 授权替代）：2026-10-03 同步约束、维护流程、PR 模板与 PK-017，当时采用 Jenkins 打包、本地电脑测试，迁移期间禁止本地构建/测试。历史云端报告保持原执行环境，未执行项如实保留，不改写为后续验收通过。
- 已安装 skill 读取当前工作区约束和清单；更新仓库版 skill 时同步实际使用的安装副本。目录丢失从私库恢复，不靠旧记忆或功能标题重写项目。
- 2026-10-03 用户要求将实际发布操作持久化到项目 skill：固定记录 `package` → 同制品本地验收并上传报告 → `publish` → 按授权网页更新，明确正式标签与候选分支区别、`PACKAGE_ID` 复用、报告绑定和失败处理。只有试打包时停在 `package`；发布不重新编译、不自动升级生产。详见 skill 的“发布、更新或恢复”及 `references/private-release.md`；本次为流程文档补充，无新增二开，未执行打包、测试或正式发布。
- 2026-10-08 用户最新要求替代上两条的默认执行顺序与测试禁令：先在用户指定服务器隔离环境（或本机 Linux/WSL）完成九套件源码验证、提交相同 tree 并绑定最终 commit 的 notes，再一次 Jenkins `ACTION=release` 完成构建、十一项制品运行时验收及私库发布，之后按授权网页更新；旧手动流程仅作兼容。约束入口、维护文档、PR 模板、三语 README、Jenkins 说明及仓库/安装版 skill 同步；无新增 ID，扩展 PK-017/021，不改写历史报告。缺环境或失败时不提交、不合入、不发布，明确实际测试环境，不把服务器通过写成本机通过。
- 验证：静态核对 Git 跟踪、入口链接、登记条目与 diff、仓库/安装版 skill 一致性。此机制不等于 GitHub 分支保护或自动语义验证。

2026-10-30 `v0.2.19-pk.2`：记录用户要求不再把实际部署身份写入源码、文档或 skill；约束入口和仓库/已安装维护 skill 同步。使用 Git 忽略的本机受限配置及通用运维参数，保留固定私库来源与不可覆盖正式标签规则。无新增 ID，未运行构建或测试。

2026-10-30 `v0.2.19-pk.3`：同步用户新增的 Jenkins `REINSTALL_NODE_MODULES` 可选重装规则。默认 `否`，显式 `是` 才在本次构建目录删除前端依赖及锁文件后重新解析安装；原源码与实际构建依赖分别留存验证。仅此显式授权覆盖默认 frozen-lockfile 安装要求，不改变正式标签交接、旧封存包验证或用户手动触发 Jenkins 的边界；具体行为登记于 PK-017，无新增 ID。

2026-10-31 `v0.2.19-pk.12`：扩展既有认证与 PK-013～015 行为，无新增 ID。Turnstile 的 site/secret 仍由管理员设置并保存在数据库，部署配置新增仅服务端使用的 `turnstile.expected_hostname`；Cloudflare 返回的 hostname 必须精确匹配，登录、注册、发送验证码、找回密码及 pending OAuth 复用对应 action，非 2xx/nil 响应 fail-closed。前端只发送公开 site key 和 action，SN-team 的 `team.sayt.cloud` key/secret 不复用到其他域名。

## PK-022

**币安 UID USDT 小数点自助充值。** 状态：有效。
引入：2026-10-01 用户要求参考 `多商户版本-部署净版-20260914` 接入，并明确首版仅使用币安 UID 内部转账。
最近变更：2026-10-01 用户明确要求将固定 1:1 扩展为管理员可配置倍率，并显示兑换比例和系统到账金额；保留原 ID，默认倍率仍为 1。

- 独立“币安自助充值”菜单，管理员包含收款账户、充值订单、累计自助充值、每日消费、币安实时账单五个页签；后两项见 PK-023/024。管理员可配置收款账户并查看全部订单/统计；普通用户仅查看自己的订单和累计充值。统计只汇总已完成订单，保留 `total_amount` 的实收 USDT 口径，新增 `total_credited_amount` 统计实际增加的系统余额；总体与每个 Sub2 用户分别显示两种金额，并保留笔数、收款账户筛选和权限边界。
- 独立币安充值账户、订单和到账流水，用户关联现有 Sub2 用户，完整实收金额按订单保存的倍率加入原余额。币安账户未启用时不提供新建入口；管理员可配置多个收款账户，用户历史记录在账户禁用后仍可查。
- 管理员在收款账户配置 `credit_multiplier`，默认 `1`，允许 `0.000001～1000` 的普通正十进制、最多6位小数；拒绝零、负值、科学计数和非法格式。新建缺省为1，更新未提交字段时保留原值，不能因旧客户端省略该字段重置倍率。公开账户只暴露兑换倍率，不暴露币安凭据。
- 订单创建在原收款账户事务锁内保存 `credit_multiplier` 快照和 `system_amount`。计算为完整五位 USDT 应付金额乘快照倍率，以精确十进制按余额精度四舍五入至8位；例如 `10.12345 × 6.7 = 67.82711500`。不得先舍弃识别小数、使用浮点数或套用原支付的两位精度与容差。后续修改账户倍率只影响新订单；旧账户与迁移前订单按倍率1保留原账务，迁移不重算用户余额或已完成充值流水。
- 付款页显示 `1 USDT = 倍率 系统余额`，准确转账金额下方显示 `system_amount`，管理员和普通用户订单列表均增加系统到账金额列。详情与列表使用订单快照，不用账户当前倍率重新计算。`system_amount` 是确认付款后应加的余额，`credited_amount` 只表示实际入账金额，未完成时保持0，不能将预计金额显示成已到账。
- 币安 Key/Secret 使用现有 SecretEncryptor 加密，响应只显示是否已配置；留空保留已保存的密钥。收款 UID 有历史订单后不得修改，订单保存收款身份。禁用账户停止创建新单，已有订单继续回查。凭据只用于只读账单查询，不提供明文读取接口。
- 使用币安 `GET /sapi/v1/pay/transactions`，校准服务时间并签名；只识别正向 USDT 的 C2C/PAY 收入及匹配的接收 UID。只读查单不能相信客户端自报到账。查询达到100条时分时间段补查，限流和网络异常记录为可见查询错误，不显示虚假成功。
- 可选 `BINANCE_RECHARGE_PROXY_URL` 或持久化配置 `binance_recharge.proxy_url` 为此客户端指定 HTTP/HTTPS/SOCKS5 代理，未配置时直连；无效代理配置使启动失败，不静默绕过指定代理。`BINANCE_RECHARGE_PROXY_PREFLIGHT` / `binance_recharge.proxy_preflight` 默认 `false`；开启后必须使用带认证的 HTTP/HTTPS 专用网关，每次查收、探测和实时账单先探测香港节点，规则见 PK-025。其他 HTTP 客户端不继承这两项配置。
- 输入整数 `1～1000000` USDT，系统追加 `0.00001～0.99999` 尾数。同一真实收款 UID 的完整应付金额不跨订单复用，防止旧单迟付认领到新用户；报价由数据库事务及唯一约束保护，耗尽明确拒绝。用户同时最多3个待付单，新建间隔30秒，每日最多20单。
- 币安流水 ID 按真实收款 UID 去重；订单、已使用 balance 充值记录、`users.balance`、`users.total_recharged` 与到账流水同一数据库事务提交，使用同一个订单快照系统金额。实收 USDT 仍按原五位整数单位匹配、留存，币安原始账单不乘倍率。并发、重试和进程重启不能重复加款；提交后失效用户鉴权及余额缓存。新充值须在原用户余额流水中可查。
- 自动轮询覆盖最近24小时的到账流水，包括到期订单；币安交易时间必须在原订单有效期内，接口延迟发现的有效付款仍可入账。超时付款及无法准确匹配的情况保留供管理员核对，不盲目认领。重新查单按订单创建时间至到期后24小时的窗口读取真实历史流水，受币安可查询历史范围限制。
- 新增轮询纳入 PK-019 启动门、停止等待及多实例查询互斥；普通支付、缓存转换、模型计费与首字行为不改变。
- 2026-10-02 上游普通充值赠送/折扣仅使用 `payment_orders` 和普通支付报价，币安继续使用自己的倍率快照与独立事务，不叠加该赠送或折扣。新增上游两份 `241_*` 迁移与首包迁移文件名不同；迁移器按完整文件名和校验和跟踪，保留已发行迁移名称及内容。
- 2026-10-08 上游 EasyPay 回调参数白名单及普通支付 return URL 去用户 query 修复只影响普通支付路径；币安的签名账单查询、订单倍率、金额匹配和入账事务保持独立，不复用 EasyPay 回调。此次没有数据库迁移变更；静态核对不代表真实充值验收。
- 入口：`backend/internal/pkg/binancepay/`、`backend/internal/service/binance_recharge*.go`、`backend/internal/handler/binance_recharge*.go`、`backend/internal/server/routes/binance_recharge.go`、`backend/migrations/242_binance_recharge.sql` / `243_binance_recharge_multiplier.sql`，以及前端币安充值 API/类型、用户与管理页面和导航。
- 验证：在已有支付协议测试、用户 repository 集成测试和支付前端测试中扩展 `TestPKBinance*` 场景；CI 与正式发布覆盖五位精度、倍率与8位舍入、配置缺省/更新保留、订单快照、旧数据迁移、签名/账单过滤、并发重复到账、多倍率统计、历史金额不复用、过期回查与权限。前端原有 `PaymentView.spec.ts` / `payment.spec.ts` 检查配置保存、兑换比例、订单金额与统计口径。按 PK-017 的提交前源码验证入口执行并记录真实环境，发行构建仅在 Jenkins 进行。模拟账单与真实 PostgreSQL 验证不等同于真实币安转账验收。
- 倍率回归入口：原有 `backend/internal/service/wire_test.go` 的 `TestPKBinanceCreditMultiplierValidation`、`backend/internal/repository/user_repo_integration_test.go` 的 `TestPKBinanceMultiplierCreditsFullUSDTWithEightDecimalRounding` / `SnapshotSurvivesAccountChangesAndConcurrentChecks` / `RejectsOldSettlementAtomically`（后三项同 `TestPKBinanceMultiplier` 前缀），以及原有 `migrations_schema_integration_test.go` 的 `TestPKBinanceMultiplierMigrationPreservesLegacyDataAndDefaults`。保留既有重复入账、状态/权限和实时账单只读断言。
- 边界：首版仅 UID 内部转账，不支持链上地址、订阅购买、原支付倍率/手续费、自动退款或邀请返利。有关联订单的异常展示为待核对；无归属到账流水保存在数据库，首版没有独立流水浏览或人工认领入账接口。与其他独立系统共用相同收款 UID 时，小数订单无法识别付款意图，应使用专用收款账户。历史金额占用记录必须保留，不得通过删除订单回收已使用报价。
- 倍率迁移的回退边界：订单倍率在旧数据回填为1后移除数据库默认值，新程序必须显式保存快照；`system_amount` 由数据库按原金额和快照生成，已完成状态要求实际 `credited_amount` 与其一致。pk.9 及更早程序缺少新字段，新建会失败，旧结算 SQL 会被约束拒绝、整笔账务事务回滚；不能静默按1:1少加款。旧版本也不识别新的显示/统计口径，不能当作倍率功能可用的回退版本。迁移不会因程序回退撤销，恢复自助充值须使用支持本迁移的私库版本。

## PK-023

**管理员每日消费页签。** 状态：有效。
引入：2026-10-01 用户要求在币安自助充值菜单增加每日消费，复用现有全部使用记录页面。

- 仅管理员可见并通过原管理员使用记录 API 取数，普通用户不能访问全局消费记录。默认选择今天、所有用户，保留原页面的筛选、汇总、详情和导出能力。
- 展示的是使用记录中的实际消费，不按资金充值来源归属或过滤。页面提示明确包含所有充值渠道余额产生的消费，包括自助充值和手动充值；不得将这些数值标成“仅币安充值的消费”。
- 默认每5秒自动刷新，可关闭或输入刷新秒数；切换页签/卸载后停止定时任务，刷新不得造成重叠请求堆积。保留现有手动刷新和筛选行为。
- 复用 `frontend/src/views/admin/UsageView.vue` 的嵌入模式和 `frontend/src/components/admin/usage/AdminDailyUsage.vue`；原 `/admin/usage` 页面默认行为与 PK-010 管理员首字权限保持。
- 验证：本地前端类型检查、既有使用记录测试及新增在已有测试文件中的嵌入/日期/刷新生命周期断言；发行构建仅在 Jenkins 进行。

## PK-024

**币安实时账单。** 状态：有效。
引入：2026-10-01 用户要求迁入参考项目实时账单，并限定管理员访问。

- 管理员在币安自助充值菜单选择收款账户和日期区间，默认今天，直接查询币安 Pay 账户流水。展示收支方向、交易类型、币种、币安原精度金额、交易时间、交易号和详情；不将账单金额强行截成自助订单的五位精度。
- 实时账单包括币安返回的收支与各类交易，不能用仅包含 USDT C2C/PAY 收入的自动入账过滤结果充当完整账单。首版范围是 UID/Pay 账单，不显示链上充值接口。
- 查询只读，不创建本地充值订单、不认领交易、不修改余额。只有 PK-022 的校验与事务可以自动入账；后台权限在服务端验证，不仅隐藏页签。
- 保留查询时间范围校验、100条上限及可能还有更多结果的明确提示、API 限流退避与请求错误；不以截断结果声称账单完整。管理员只能查询已经配置并加密保存凭据的收款账户，响应不回传凭据或签名URL。
- 账单与充值查收共享数据库 `api_locked_until` 租约及 `poll_token` 校验，跨实例互斥；账单另有 `bills_next_query_at` 冷却，到期订单优先查收。成功读账单不修改充值查收计划，币安限流则同步延后两类查询。
- 开启 PK-025 后，每次实时账单操作先重新探测香港出口，再用同一出口校时并读取账单；探测失败不查询币安、不改余额，也不回退直连。
- 入口：`backend/internal/pkg/binancepay/` 的账单查询、币安充值 service/handler/admin routes、前端 `BinanceLiveBills.vue` 与币安 API/类型和管理员菜单。
- 验证：在原有支付协议和前端测试文件扩展账单收支/小数、日期校验、上限提示、只读权限用例；选择运行 PK-017 的可选源码验证入口时记录真实环境与结果。真实账户账单可用性须在部署配置后核对。

## PK-025

**币安专用香港出口探测。** 状态：有效。
引入：2026-10-01 用户明确要求只让币安相关逻辑使用香港代理，并在每次查询前探测可用节点。

- 仅影响 PK-022/024 的币安客户端。保持其他模型请求、支付渠道、GitHub 更新和系统网络的既有出口；不配置全局代理、TUN 或系统路由。
- 正式配置文件支持 `binance_recharge.proxy_url` 和 `binance_recharge.proxy_preflight`，默认空地址/`false`，兼容既有静态代理；对应的非空环境变量 `BINANCE_RECHARGE_PROXY_URL` / `BINANCE_RECHARGE_PROXY_PREFLIGHT` 优先于文件。开启预探测时必须使用带用户名和密码的 HTTP/HTTPS 网关。空地址、SOCKS 预探测、非法布尔值或代理配置使启动失败，不能静默降级。
- 配置由既有 Viper 加载链在启动时读取。服务器可先在持久化 `/app/data/config.yaml` 写好独立币安配置，再由用户网页升级触发新程序重启后生效；不要求重建容器注入新环境，也不在升级时丢失代理配置。文件配置同样只注入币安客户端，不成为全局代理。
- 每次顶层 `History`、`Bills`、`Probe` 操作调用认证网关 `POST /_binance/preflight`，上限15秒，拒绝重定向、非200、畸形响应和非法令牌。预探测包含在原操作超时及数据库查询租约内；失败作为可见错误交给既有退避机制。
- 网关并行检查最多8个固定节点，允许4轮并发探测，覆盖3个后台账户 worker 并为手动查询保留容量，超限明确返回429。每个候选先以严格 TLS 访问 Cloudflare trace 并确认 `loc=HK`，再确认币安时间接口返回200及有效 `serverTime`；两个检查共享该节点6秒预算。每次重新探测，不用上次成功代替本次证据。全部失败时拒绝操作，不回退到直连或其他地区。
- 成功的随机令牌固定指向一个节点，有效期240秒。应用为本次操作创建独立 transport，校时和全部分页共享该出口，令牌仅放在 CONNECT 头；并发操作不得通过切换全局代理组互相改路由。固定出口中途失败则本次失败，下次操作重新探测。
- 网关只接受经过认证且带有效令牌的 `CONNECT api.binance.com:443`。币安签名和 API 凭据在应用到币安的 TLS 内传输；拒绝其他目标、普通转发、无令牌或过期令牌。节点监听仅在本机，网关只绑定应用可达的私网地址，凭据和订阅保存在服务器受限文件中，不写入源码或日志。
- 入口：`backend/internal/config/config.go`、`backend/internal/pkg/binancepay/{client,proxy_preflight,bills}.go`、`backend/internal/service/{wire,binance_recharge}.go`、`backend/cmd/server/wire_gen.go`、`deploy/binance-proxy/gateway.py`。固定节点及 systemd 运维见该目录说明和 `PK_MAINTENANCE.md`。
- 验证：既有 `backend/internal/payment/crypto_test.go` 的 `TestPKBinanceProxyPreflight*` 覆盖真实模拟 CONNECT/TLS、分页固定出口、并发隔离、失败关闭和取消；既有配置测试覆盖文件读取、环境优先及非法配置。网关及配置验证按 PK-017 的提交前源码验证入口执行并记录真实环境，报告作为 Jenkins 正式发布门禁；生产只做公开币安时间接口及真实出口探测，不用模拟到账冒充真实转账。
- 边界：地域依据 Cloudflare 当前看到的出口，节点名称不作为地域证明；公开时间接口成功不能代替收款账户 API 权限或真实转账验收。订阅节点凭据失效或出口变化时停止该节点使用，需更新服务器节点配置；没有直连兜底。

## PK-026

**耻辱排行榜与管理员手动扣费。** 状态：有效。
引入：2026-10-03 用户要求按“安全策略”使用记录统计破甲排行、日期筛选、实时展示与默认6.7倍率扣款；追加要求独立菜单、普通用户只读、管理员操作，并明确命名“耻辱排行榜”；普通用户标识仅显示前3位，其余脱敏。

- 独立“耻辱排行榜”菜单。已登录用户可查看排名、展示名及次数；普通用户的展示名由后端仅保留前3个 Unicode 字符、其余打码，无用户名时不回退暴露完整用户ID。管理员可查看完整标识、管理字段、调整设置和逐人扣费。普通用户接口不得暴露其他用户的邮箱、余额或扣费控制字段，不能只靠前端隐藏按钮保护写入。
- 唯一统计来源是 `usage_logs.request_type = 4`（`cyber`），每条已入库记录计一次，按次数降序并稳定排序。默认当地今天，日期区间和时区由后端校验；记录异步写入后可见，刷新不触发扣款。默认每5秒刷新，可关闭/调整；卸载停止、请求不重叠，过期日期响应不得覆盖当前筛选。
- 全局扣费倍率持久化，默认 `6.7`，允许 `0.000001～1000`、最多6位小数；服务端校验普通正十进制、精度和范围，金额使用精确十进制并按余额8位精度记录。管理员确认后扣款金额为所选时间内尚未扣费的触发次数乘倍率；已扣过的记录不因切换日期、修改倍率或再次点击而重复扣费。
- 扣费预览冻结用户、时间区间、具体使用记录、次数和倍率，10分钟有效；扣费请求使用预览ID重试。同一使用记录只能产生一次扣费，重复或并发请求不得重复扣余额。预览后新增的触发记录留待下次操作；相互重叠的已失效预览须重新预览，不能悄悄改金额。
- 扣减 `users.balance`、负值 `admin_balance` 原余额流水、扣费批次及逐条使用记录认领在同一数据库事务完成。余额不足整笔拒绝，不部分扣款；不改变累计充值、原模型费用、订阅/API Key/账号额度或上游 usage。成功提交后失效用户鉴权和余额缓存。
- 扣费记录与去重证据独立保存，清理使用记录不能删除扣费证据。排行榜只统计仍保留的使用记录；数据库迁移不回扣历史余额，也不自动扣款。程序回退不撤销新增表或已完成扣款。
- 入口：`backend/migrations/244_security_policy_ranking.sql`、`backend/internal/service/cyber_policy*.go`、管理员与普通用户使用记录 handler/routes、前端独立排行 API/页面和导航。自动禁用计数见 PK-027，不能混同为筛选日期内次数。
- 验证：在已有后端 service/repository/handler 与前端回归文件扩展 `TestPKCyber*` 及排行场景，本地验证金额、权限、日期边界、并发扣款、重试、重叠区间、余额不足回滚与刷新生命周期。当前变更的实际结果见[发布核对记录](docs/PK_SHAME_LEADERBOARD_REVIEW.md)；不在本地编译发行程序，不新增遗留临时测试文件。

## PK-027

**安全策略触发次数自动禁用用户。** 状态：有效。
引入：2026-10-03 用户追加要求可配置触发次数自动禁用账户，并可设置手动解除禁用后重新计数。

- 仅管理员可配置。`auto_disable_enabled` 默认 `false`、`auto_disable_threshold` 默认 `10`（允许1～1000000000的整数）、`reset_on_manual_enable` 默认 `true`；升级不会自动启用禁用规则，不回算历史记录。这里的账户是排行对应的 Sub2 用户，管理员用户始终豁免。
- 自动禁用以启用期间新写入的安全策略使用记录为准，在独立持久化计数上累加，达到阈值将普通用户状态改为 `disabled`。它独立于排行榜日期筛选、是否已扣款和原模型计费；关闭规则期间不累加，关闭/再开启不隐式清空已有计数。修改阈值在后续安全策略记录写入时判定。
- 数据库插入触发器覆盖现有应用对父表的单条、批量和 best-effort 使用记录写入（含父表自动路由到分区），用户行锁串行化并发累计；重复而没有实际插入的记录不累计。使用记录清理不重置该计数，也不恢复用户状态。外部导入必须写父表 `usage_logs`，直接写叶子分区不经过父表的语句级触发器。
- 管理员从已有用户管理手动把 `disabled` 改为 `active` 时，按 `reset_on_manual_enable` 决定清零或保留累计次数。清零只重置自动禁用计数，不删除原使用记录、不改变排行榜历史或扣费去重。保留计数时，下一条安全策略记录可能再次触发禁用。
- 复用已有用户状态变更的鉴权失效 outbox，事务提交后由既有 worker 广播失效并重试；不新增后台服务。禁用阻止随后鉴权的新请求，不主动中止已经开始的请求；usage 异步入库及缓存广播存在短暂延迟，不承诺恰好只允许阈值数量的并发请求。
- 入口：`backend/migrations/245_cyber_policy_auto_disable.sql` 的使用记录/用户状态触发器、PK-026 管理员设置和原用户管理解禁流程；既有鉴权失效 outbox/worker 继续受原启动和停止约束保护。
- 验证：在既有 repository integration 文件扩展 `TestPKCyber*`，覆盖阈值、默认关闭、管理员豁免、并发插入、重复写入、手动解禁两种重计模式和鉴权 outbox；选择运行 PK-017 的可选源码验证入口时记录真实环境与结果。迁移不撤销已禁用状态，旧版回退仍受数据库触发器约束，须先核对已保存开关及管理员可操作入口。


## PK-028

**OpenAI 账号 Kilo 桥接。** 状态：有效；依据：2026-10-15 用户要求接入 `E:\Ai\kilo-test` 的协议转换，并明确模型/代理交由 Sub2API 配置。

- 新增/编辑账号仅 OpenAI 平台显示独立开关，所有 OpenAI 账号类型可保存。`accounts.extra.kilo_bridge_enabled` 为 boolean，旧账号/新建默认 false；省略更新保留旧值，显式 false 关闭；其他平台提交该键或非法类型拒绝。普通更新、局部 extra 和批量 API 均校验并复用原调度刷新。
- 与缓存转换完全独立。开启后强制 HTTP Responses，忽略旧的“不支持 Responses”探测，不自动回退 Chat 上游；下游 Chat/Messages 继续通过原协议适配器。关闭后恢复既有路由设置，不覆盖所存的协议/WS 设置。
- 分组强度策略继续有效：选中 Kilo 账号后，仅对入口未识别的 ultra/persistent 按对应上游等级检查映射、上限和拒绝，保留原请求模型作用域，不对已经处理的普通等级重复映射；超限不绕过原权限规则。
- 模型交由用户账号映射，不新增参考桥接中的 `openai/` 前缀。Kilo 与透传同时开启时同样应用账号映射，调度/上游请求保持一致；不套用 Codex 内置模型别名。既有渠道映射与 compact 专用映射继续有效。
- `reasoning.effort` / `reasoning_effort` 中 ultra、persistent 转 max；其他强度不由桥接修改。按用户最新要求，使用记录通过 requested_reasoning_effort 保留客户端原始强度（ultra 仍显示 Ultra），客户费用和余额/订阅/API Key 额度按此原始强度的配置倍率计算；ultra、persistent 可分别配置，未配置按1倍，不套用 max 倍率。上游请求与管理员实际转发字段 reasoning_effort 保留 max；账号自定义成本规则仍反映实际转发强度和原始 usage。普通账号保留上游既有计费口径。
- function_call 转 custom_tool_call，arguments 原样成为 input；function_call_output 转 custom_tool_call_output，字符串包装为 input_text 数组；配对 call_id/namespace/工具参数及其中的加密子任务保留。对应 fc_/fco_ 项 ID 转 ctc_/ctco_。
- 普通用户的使用记录/API 仅显示请求强度，不提供上游强度或桥接映射说明。Responses JSON/SSE 回显的强度元数据恢复为原始 ultra/persistent；不更改生成正文、工具参数或真实 usage。原请求为 max 时照常显示 max。桥接请求的错误响应和用户错误记录中，如错误文案暴露到 max 的强度/路由映射，改为通用上游失败提示；普通参数错误保持，管理员实际错误诊断保留。
- agent_message 仅保留可读 input_text 并转用户消息；reasoning 移除 encrypted_content 并保留 summary；include 删除 encrypted_content 请求。既有 custom_tool 项保持，不进行字符串递归擦除。
- 复用账号认证、URL 安全校验、代理、超时及重试。API Key 账号 Base URL 可填 `https://api.kilo.ai/api/gateway`，拼接 `/responses`；所选代理为空即现有服务器出口。开关不替换账号凭据类型或 OAuth 的目标服务，不提供独立代理配置。
- 同步Chat/Messages与Messages的custom_tool_call_input.done保留Kilo工具调用输入。响应继续走原 HTTP JSON/SSE、首字计时、usage 改写和费用流程；内部保留原 usage，缓存转换照常。没有新增表或全局设置。
- 入口：`account_kilo_bridge.go`、`openai_kilo_bridge.go`、`admin_account.go`、`upstream_protocol_routing.go`、OpenAI forward/passthrough/chat/messages/request_body/scheduling、`account_test_service.go`，创建/编辑组件与中英说明。
- 验证：扩展原 `openai_gateway_service_test.go`、`openai_gateway_usage_integrity_test.go`、`gateway_reasoning_pricing_test.go`、强度策略/定价及 DTO 回归和两份账号表单 spec，覆盖平台限制、默认值/保存、普通/透传、映射/未映射、代理/直连、工具子任务、ultra/max、同步/流式、分组策略及缓存转换共存；独立倍率下核对客户金额、余额/订阅/API Key 额度和上游成本，用户出口不显示映射。实际执行结果见最终源码报告。未用真实 Kilo 付费凭据作在线生成验收，不声称所有模型/客户端均已实测。

2026-10-19 合入 v0.2.17：普通/透传错误终态同时保留 Kilo 客户端错误脱敏和上游新增的 `created_at`、`sequence_number`；不改变管理员原始诊断、强度计费或 usage。原 `openai_response_model_rewrite_test.go` 新增 `TestPKKiloBridgeSynthesizedFailurePreservesUpstreamMetadata`，覆盖两条实际流式合成终态路径的时间、序号、真实响应 ID、正文、客户端脱敏和内部原错误保留，纳入现有 unit/race 选择器。

同次审计发现的既有边界：强度倍率函数与校验由多类账号共享。第三方兼容 API Key 即使关闭 Kilo，若其 Chat→Responses 上游接受 ultra/persistent 且配置了同名倍率，仍可能使用该 PK 扩展键；真实 OpenAI 常规合法强度不受影响。详见[计费审计](docs/PK_OPENAI_KEY_BILLING_AUDIT_0.2.17.md)。本次登记并保留已有配置，不将这一缺口描述为所有普通账号都严格隔离，也不未经明确范围调整收费。

2026-10-26 合入 v0.2.18：PK-028 的精简调度候选投影补入既有 `kilo_bridge_enabled`，避免 Kilo+透传账号在渠道上游模型限制筛选时误用普通透传逻辑，并保持初筛/最终完整账号的模型映射与强制HTTP策略一致。仅传递既有布尔配置，默认关闭、账号保存、强度倍率及工具转换不变；共享候选母本不作原地修改。静态核对发现并补齐该既有调度遗漏，无新增ID，未运行测试或构建。

同次给 OpenAI 精简 meta 添加 `PKProjectionVersion=1`，旧无标记投影按既有未命中走回源/重建，避免升级后长期使用缺 Kilo 开关的 Redis 记录；非 OpenAI 旧投影、完整账号 JSON、上游缓存/桶版本/epoch及失效机制保持兼容。默认数据库回源开启；显式关闭时旧 OpenAI 投影需等待后台重建成功，不绕过关闭设置，不主动操作生产缓存。既有调度缓存回归文件扩展格式与旧投影边界，未运行、不新增测试文件。

同轮用户明确要求“除了这两个开关，如果都在关闭的情况下，要跟上游一模一样”。据此修复上方 v0.2.17 审计登记的共享强度倍率隔离缺口：`reasoningEffortBillingMultiplier` 恢复固定上游的标准归一化；仅 Kilo 开启时，`RecordUsage` 为客户定价创建私有子 context，允许原始 ultra/persistent 倍率，并传给基线、响应模型重定价和免费Fast重算。账号成本仍使用原 context/实际转发强度；Kilo关闭时即使历史配置保留 ultra/persistent 倍率也按上游规则处理，不误用扩展键。缓存转换关闭时原始tokens及上下文选档不变；两开关独立，开启时保留对应规则。无需配置/数据库迁移，不修改历史账单；更新既有强度和网关计费回归，未运行测试或构建。历史审计第5项自本次代码修复后不再作为当前已知缺口。

## PK-029

**账号隐藏计费倍率及下游用量缩放。** 状态：已授权移除。引入：`v0.2.16-pk.2` / `4427e25f8`；下游 Token 缩放扩展：`v0.2.16-pk.5` / `d1b1dbf65`。

- 2026-10-16 按用户要求将全部源码恢复到 `v0.2.16-pk.1`，撤销隐藏倍率设置、扣费桥、账务展示、预留适配、批量图片倍率快照及 HTTP/SSE/WebSocket 用量缩放；恢复 pk.1 的缓存转换、分组/用户倍率和账号成本口径，Kilo 桥接保留。
- 原入口为 `account_hidden_billing.go`、`hidden_usage_output.go`、`internal/pkg/usageoutput`、相关 handler/repository/DTO 及前端设置/展示；实现和新增迁移 `246_hidden_billing_multiplier.sql` 随源码回退，历史内容仍可从原标签读取。
- 本次仅回退源码，不删除数据库列、迁移记录或账号配置，不返还或重算已结算费用。旧代码忽略隐藏倍率配置和追加列；若日后部署旧程序，存量批量图片任务仍按已冻结单价结算，不能把源码回退称为重置全部历史倍率。
- 验证：除本清单外全部跟踪文件须与 pk.1 一致，并执行目标版本完整九套件和 preflight；结果绑定本次最终提交。迁移兼容性为源码核对，不是本次生产降级验收。

## PK-030

**原始 Token 趋势统计。** 状态：已授权移除。引入：`v0.2.16-pk.3` / `13880e18a`；普通用户缺失提示调整：`v0.2.16-pk.4` / `ec35f1e77`。

- 同次授权恢复 pk.1 的图表查询、聚合、DTO 和前端展示，撤销原始 Token 快照及覆盖标记逻辑，也撤销 pk.4 的缺失提示控制；不修改历史使用记录。
- 原入口为 `usage_original_tokens.go`、`usage_log_original_tokens.go`、usage repository/聚合及 `TokenUsageTrend.vue`；新增迁移 `247_usage_original_tokens.sql` 随源码回退。已存在的数据库追加列与迁移记录保留，pk.1 的迁移 runner 不要求删除数据库中多出的 246/247 记录。
- 验证方式与 PK-029 相同；本次不连接生产数据库，不将历史费用或图表口径恢复解释为数据回滚。PK-029、PK-030 的原 ID 永久保留，后续重新引入须明确登记。

## PK-031

2026-11-02 v0.2.19-pk.40：按用户要求，智商测试（含管理员别名路由）、雷达、账号授权统一仅用顶部页面标题；这四个路由在移动端保留顶栏标题，桌面说明同步顶栏。其他页面标题、品牌、主题与布局规则不变。

2026-10-31 v0.2.19-pk.33：账号授权菜单使用既有Icon组件的badge认证徽章，与雷达signal图标区分；路由、权限、菜单顺序和响应式布局保持。

2026-10-31 v0.2.19-pk.9：账号授权菜单紧接雷达，授权页和巡检页沿用柔蓝导航、表单、Select、对话框、深色与响应式约定；只对管理员开放。保留 pk.8 雷达标题、四列布局和紧凑分页。

**DP API 品牌与用户中心、管理后台柔蓝主题。** 状态：有效。引入依据：2026-10-21 用户提供界面截图，要求浅灰蓝配色、白色胶囊导航，并确认用户中心与管理后台都调整；随后指定 DP API 品牌、渐变标志、全部内置显示文案更名和移除 GitHub 友链。目标版本 `v0.2.17-pk.2`。

- 用户中心与管理后台共享浅灰蓝页面背景、柔蓝强调色、白色胶囊选中导航；按钮、筛选控件和卡片采用一致的圆角、边框与轻阴影。品牌显示规则见下文，不改变业务接口结构、计费算法、权限边界或既有配置存储结构，不新增依赖。
- 默认标志为 `frontend/public/dp-api-logo.png`，采用透明背景的 DP 组合与蓝紫渐变；侧栏、首页、登录注册布局、Key 使用页、模型广场、法律文档页和 favicon 使用该资源，已配置的自定义 Logo 仍优先。图像生成提示保留在 `docs/DP_API_LOGO_PROMPT.md`。不在图片内重复放置站点名称，文字继续由站点设置渲染。
- 同日按用户追加要求，浏览器 favicon 使用从现有 DP 标志等比生成的 `frontend/public/dp-api-favicon.png`（64px）与 `favicon.ico`（16/32/48/64px），不重新绘制品牌。静态入口、无自定义 Logo 以及清空 Logo 后均回退到专用 PNG；`main.ts` 监听公开站点 Logo 的异步加载/变化，`utils/branding.ts` 保留合法自定义图片优先并按真实扩展或 data MIME 声明图标格式，未知格式交给浏览器识别，非法 URL 不替换当前图标。原有 `utils/__tests__/branding.spec.ts` 扩展格式、清空回退和动态替换回归，仍由现有前端关键套件执行。
- GitHub 友链移除入口包括首页/Key 使用页页脚、顶栏用户菜单、版本弹层的发布页外跳和支付设置的 GitHub 文档链接；合规原文入口改为站内页面。保留 GitHub OAuth 登录及必要配置入口、PK-014/015 私库更新与回退请求、手动恢复命令中的真实私库地址和校验链。多语言内置文案及前端使用指引同步品牌；协议、环境变量、持久化键和服务名不因显示更名而改变。
- 原 `views/admin/__tests__/SettingsView.spec.ts` 的支付说明用例同步上述已授权的 GitHub 友链移除行为：确认外跳链接不存在，同时保留支付说明和商品名称配置的正向断言，不恢复已移除的友链来迁就旧断言。
- 2026-10-21 用户继续指定站点名称为 `DP API`。后端公开/管理员设置读取及 `GetSiteName`、余额/配额通知和统一邮件通知、前端注入配置/缓存、登录注册/验证、首页、模型广场、法律文档页、Key 使用页与浏览器标题统一使用该默认名；空白值或旧内置名 `Sub2API` 在读取时显示为 `DP API`，新实例默认值同样为 `DP API`。非默认自定义站点名优先并保留既有显示规则，不迁移或重写现有数据库记录，不替换 Go 模块/仓库路径、机器协议标识、User-Agent 或用户自定义配置；后续全局显示及合规文档调整见下文。名称规则集中在 `backend/internal/service/setting_features.go` 和 `frontend/src/utils/branding.ts`，`balance_notify_service.go` 的既有共享默认常量、`notification_email_service.go` 的名称读取、设置解析/公开接口及各显示入口复用；统一邮件通知与页面标题保留原有去除首尾空白的规则。
- 同日用户进一步要求全部内置 `Sub2API` 显示文字改为 `DP API`，并移除界面 GitHub 友链/外跳入口。后端包含安装/启动提示、插件兼容提示与 schema 显示标题、模型说明、默认支付商品名、账号测试文案、设备证明错误提示，以及新建 WebAuthn/TOTP 的显示名称；不改插件协议字段、模块名、User-Agent、可执行文件名、既有密钥、认证算法、第三方版权/许可声明或已配置的自定义商品名/前后缀。
- 合规文档 `docs/legal/admin-compliance.{zh,en}.md` 仅作品牌文字修订，责任、义务和条款保持完整；为遵守上游“内容修改需递增版本”的审计规则，版本更新为 `v2026.10.21`。`admin_compliance.go` 与 `frontend/src/stores/adminCompliance.ts` 同步新确认短语和版本；文档链接改为站内 `/legal/admin-compliance`，按站点当前语言展示。原有确认记录保留，旧版本按既有门禁要求管理员重新确认，不自动接受、不删除历史记录、不降低确认或访问要求。既有 `admin_compliance_test.go` 与 `adminCompliance.reset.spec.ts` 覆盖新品牌/站内链接、旧版本需确认与记录保留。
- 合规全文阅读仅在精确的只读 `LegalDocument` 路由 `/legal/admin-compliance` 暂不显示全局确认弹窗，避免全文再次被遮挡；其他法律路径不在此范围，返回控制台立即恢复原确认要求。既有确认状态、历史记录、接受接口及后端访问门禁不变，原 `adminCompliance.reset.spec.ts` 补充全文阅读、路由往返和状态不变回归。
- 主题使用现有布局、公共样式与 Tailwind 配置。保留原有浅色/深色模式切换、主题偏好保存与深色下文字/控件可读性，不增加新的主题配置或持久化字段；状态、错误、禁用及键盘焦点仍须可辨识。
- `AppLayout` 统一提供响应式顶栏高度和主区内边距变量，`TablePageLayout` 与 `CustomPageView` 的全屏高度按同一变量计算，兼容桌面 80px 顶栏及小屏布局；原有表格横向滚动、分页与自定义内容交互不变，对应既有回归纳入关键前端套件。
- 2026-10-21 用户审核通过 Home 设计图，并要求另交新版本且保留 `v0.2.17-pk.2` 全部功能。首页扩展目标为 `v0.2.17-pk.3`，从包含已发布 pk.2 提交 `b99ca3ab9825cccfe771d3fbf3df620792c0d0e7` 的私库 main `fb25bfc0a18c607311f6f7873b0689ad708e8774` 继续开发；不回退或替换 pk.2 的其他业务实现。默认首页改为柔蓝双栏首屏：左侧本地化价值说明与登录/控制台入口，右侧单密钥连接多模型的示意，下方为一键接入、灵活调度和用量说明。沿用站点名称/Logo/副标题/文档地址配置与 URL 清理；自定义非默认副标题完整显示，内置默认副标题使用中英文首页说明。保留 `home_content` 的 URL iframe/HTML 最高优先级和 `compact_home_enabled` 简洁模式，不新增状态接口、虚构指标或免费额度承诺。
- 首页头部与首屏的模型广场入口共同遵守 `FeatureFlags.modelPlaza` 和 `model_plaza_require_auth`；匿名主操作仍指向 `/login`，登录用户与管理员分别进入 `/dashboard`、`/admin/dashboard`。文档未配置或不安全时不显示外链，语言与主题开关、深色和窄屏布局继续保留。既有 `HomeView.compact.spec.ts` 更新旧终端断言并补充默认页跳转、双入口门控、自定义品牌、文档链接和主题持久化回归，纳入 `FRONTEND_CRITICAL_VITEST`；实际验证结果以本次测试服务器报告和浏览器截图为准。本次扩展沿用 PK-031，无新增 ID。
- 2026-10-21 用户指定登录页采用参考截图的左侧品牌文案/旋转刻度盘、右侧登录卡片排布，并明确沿用当前柔蓝主题色。登录页独立使用 `frontend/src/components/layout/LoginLayout.vue`，SVG 刻度盘封装在 `components/auth/LoginDial.vue`；颜色从现有 Tailwind `primary`、`dark` 和 `canvas` 派生，保留 `theme` 偏好键及浅色/深色切换，不使用参考站橙色、名称或业务文案。站点名、Logo、自定义副标题沿用公开设置与 URL 清理，缺省使用 DP API 品牌；不修改共享 `AuthLayout.vue` 的其他认证页面布局。桌面显示品牌区、主题色滚动条与鼠标跟随刻度盘，窄屏优先完整登录表单；减少动态效果偏好、不可见页面和组件卸载时停止对应动画/监听，不生成虚构用量或在线状态。
- 登录改版保留 `views/auth/LoginView.vue` 的邮箱/密码、三种验证码、Passkey、六种 OAuth、登录条款同意/修订门禁、TOTP 二步认证、注册/重置密码开关、邀请信息清理与登录后跳转逻辑；GitHub OAuth 仍作为认证方式保留。仅调整模板、局部样式及密码显隐按钮的可访问名称。原 `LoginView.spec.ts` 扩展公开设置未加载、后台模式、条款、密码显隐和实际登录请求回归，原 `TencentCaptchaActionGate.spec.ts` 同步布局替身；两者与原 `OAuthLoginSections.spec.ts`、`TotpLoginModal.spec.ts` 纳入 `FRONTEND_CRITICAL_VITEST`。通过与否以本次测试服务器报告为准，桌面/移动端及浅色/深色实际截图另行核对；本扩展沿用 PK-031，无新增 ID。
- 用户与管理员的业务菜单、路由、功能开关、简易模式和自定义菜单继续遵循原有可见性与访问权限；仅移除上述已授权的友链入口。保留菜单组展开、滚动位置、72px 侧栏折叠，以及移动端遮罩、菜单展开/关闭和内容布局，不扩大权限。
- 保留 PK-013 的“版本号 + PK出版”标识、管理员版本更新/回退入口及弹层可用性；保留 PK-022 币安自助充值与 PK-026 耻辱排行榜在对应角色下的独立入口、原有操作与权限。PK-010 使用记录指标权限及其余既有二开不因全局样式调整而变化。
- `-version` 输出品牌更新为 DP API；PK-017 的 `deploy/jenkins/verify-runtime.py` 同时识别新品牌与历史 Sub2API 输出，仍严格匹配版本、完整 commit 与构建字段，不放宽制品/来源校验。既有 `test_release_matrix.py` 增加两种品牌与错误版本/提交/名称的回归，随 `release-helpers` 源码套件执行。
- 主要代码入口：`frontend/tailwind.config.js`、`frontend/src/style.css`、`frontend/src/components/layout/AppLayout.vue`、`AppHeader.vue`、`AppSidebar.vue`。公共选择器 `frontend/src/components/common/Select.vue`、`DateRangePicker.vue` 以及复用公共样式的用户/管理员页面属于联动检查范围；筛选值、事件、键盘操作与弹层定位保持原有语义。展开侧栏为16rem（默认256px），主区与 `features/prompt-audit/PromptAuditView.vue` 固定保存栏共用布局宽度变量，折叠时同步至72px，避免遮挡配置开关；原 `PromptAuditView.spec.ts` 纳入关键回归列表。
- 2026-10-24 用户反馈左侧菜单过宽并要求恢复原宽度、无需测试直接提交，目标 `v0.2.17-pk.13`。沿用 PK-031，无新增 ID；依据品牌改版提交 `b99ca3ab9` 的父版本，将 `AppSidebar.vue` 与 `style.css` 从280px恢复为原 `w-64`（16rem），`AppLayout.vue` 的共享宽度变量同步为16rem。静态核对展开/72px折叠、主内容区及提示词审计固定保存栏的间距联动；菜单权限、功能开关、品牌样式及移动端开关保持原实现。按本次要求未运行测试或构建、未生成测试文件，不将历史回归结果作为本轮通过证据。
- 同日pk.14扩展首页导航及联系方式/智商测试展示，详见PK-032和PK-033；pk.15按用户澄清，智商测试展示在首页，联系方式展示在首页/登录/用户仪表盘顶部。原首页三种模式、登录认证流程、仪表盘统计和256px侧栏保持，导航在中屏提前换行以容纳两个新锚点。仅静态核对，不冒称浏览器视觉验收通过。
- 现有回归入口：`frontend/src/components/layout/__tests__/AppSidebar.spec.ts` 检查排行榜菜单、订阅开关、折叠组、滚动位置、自定义图标和版本弹层不被裁剪；`frontend/src/components/user/dashboard/__tests__/UserDashboardStats.spec.ts` 检查平台统计卡片及简易模式。二者继续保留在 `Makefile` 的 `FRONTEND_CRITICAL_VITEST` 中；本次将管理员 `frontend/src/views/admin/__tests__/DashboardView.spec.ts`、公共选择器原有 `Select*.spec.ts`、`DateRangePicker*.spec.ts` 和 `disabledSelectors.spec.ts` 一并纳入，保护筛选、键盘、输入法、禁用态与日期边界。实际结果仍须核对当次源码套件原始日志，不能仅因列入清单即声称通过。
- 验证计划与边界：按 PK-017 在用户指定测试服务器隔离环境运行完整九套件及 preflight，提交相同 tree 后绑定最终 commit 的原报告及 notes；本条登记时尚未以本次报告确认通过，不沿用历史测试结果。补充核对用户/管理员、浅色/深色、桌面展开/72px 折叠/移动端，以及筛选、卡片、菜单和版本弹层；现有源码断言和 jsdom 交互用例不能代替真实浏览器视觉验收，实际结果与未覆盖范围另随交付说明记录。发行构建仍只在 Jenkins，新增临时测试文件在验证后清理。
- 邮件品牌入口还包括 `content_moderation.go` 的内容审计通知、`user_service.go` 的通知邮箱验证码，以及 `auth_email_binding.go`、`auth_oauth_email_flow.go`、`auth_service.go` 的绑定/注册/OAuth验证码与密码重置：默认值统一复用 `defaultSiteName`，直接读库的名称走同一兼容规则；保留通知流程、权限、收件人、验证码、SMTP发件名与原有去空白规则。
- 名称兼容回归扩展于原有 `setting_service_public_test.go`、`setting_service_update_test.go`、`backend/internal/server/api_contract_test.go`、`frontend/src/utils/__tests__/branding.spec.ts`、`stores/__tests__/app.spec.ts` 与 `router/__tests__/title.spec.ts`：核对默认/旧内置名、非默认自定义名、公开/管理员/邮件及内容审计通知读取一致、读取不写库，以及前端注入/请求/缓存与标题一致；API契约保留旧内置名输入并验证新默认名输出。名称需求追加后，先前源码结果不代表最终树通过，须重新执行完整验证。

## PK-032

2026-11-02 v0.2.19-pk.40：SVG缩略图新增点击放大/原生键盘按钮，使用已加载的artifact打开响应式大图，不追加读取或模型请求；控制台与公开首页共享ResultArtifact。大图仍经过同一DOMPurify净化，空sandbox、严格CSP、无脚本/同源/外部资源权限保持；支持SVG/CSS时间动画。复用BaseDialog关闭按钮、Esc、遮罩和焦点恢复，局部Tab圈定，关闭/卸载清理事件。仅展示尺寸变化，结果读取、缓存、轮询和所有任务规则保持。

2026-10-31 v0.2.19-pk.9追加空号池例外：用户授权只读现场，确认0账号分组的SVG/糖果题已running约20分钟且租约续期。runIntelligenceTestAttempts在每次请求前查询当前account_groups/accounts；没有未删除账号时不发请求、不继续重试，以completed及既有error_code=empty_account_pool记为本轮结束，passed保持NULL，不伪造答案。普通与公开列表增加安全empty_pool标记，前端显示中性「号池空」和等待下轮说明，不显示作品/详情/合格标记。释放本任务租约时从完成时间等待配置间隔，避免原到期时间已过导致立即重跑；新账号在下轮重新纳入。仅真实空池跳过，限流/并发暂满不冒充空池，非空组原重试逻辑保持。不改表结构或历史记录，不执行构建/测试/真实模型请求、不部署。入口为intelligence_test_retry/service/finalize/public及两个结果页面、中英文案/API类型。

2026-10-24 `v0.2.17-pk.14` 按用户要求将已有结果展示到公开首页 `/home`；沿用本 ID，不改变生成任务。首页顶部“智商测试”锚点随实际展示内容出现，点击滚动并聚焦结果区；无配置、关闭、无已选有效分组或无已完成结果时，整个区块和锚点隐藏。SVG/糖果类型与分组切换展示最近最多30条真实完成结果、当次模型/强度、时间与耗时，糖果答错仍显示不合格，不展示失败卡片或虚构数据。

- 同日 `v0.2.17-pk.15` 用户明确澄清“仪表盘顶部显示联系方式，不是智商测试”，因此撤销pk.14在用户仪表盘追加的智商测试结果区及导入，保留顶部联系方式。首页结果与原智商测试独立页面继续保留；不涉及配置、历史结果、后台任务或数据库变更。本轮仅静态核对，未运行测试或构建。

- 新增独立只读 `/api/v1/public/intelligence-test/{groups,results,artifact,details}`，用于用户明确授权的公开展示；原登录/管理员接口权限保持原样。每次请求直读数据库总开关和类型开关，并在结果查询中匹配同一配置原值、当前分组选择映射及有效分组，避免保存/关闭并发时沿用旧配置。只返回 `completed`，作品和详情同时核对原分组/结果ID和类型，未开放或不存在返回空列表/404；不返回凭证、Key状态、管理员诊断、原始模型流或后台配置，不提供执行模型的公开入口。
- 公开组列表最多2000组，仅包含有已完成结果的启用类型及分组；单组单类型最多30条。响应 `no-store`，读取预算5秒，保留公开面板限流并增加独立每IP每分钟240次上限，Redis限流不可用时拒绝读取；单作品、提示词和回复继续受原有数据库长度边界保护。首页使用 `HomeIntelligenceResults.vue`，独立公共API不携带Bearer凭证、不进行登录刷新或重定向；60秒刷新当前类型/分组，页面隐藏暂停，切换、清空和卸载取消旧请求。SVG继续使用原净化、严格CSP、无脚本/无同源sandbox，糖果详情继续转义展示。
- 公开展示入口：`backend/internal/service/intelligence_test_public.go`、`handler/intelligence_test_public_handler.go`、`server/routes/intelligence_test_public.go`及router；`frontend/src/api/publicIntelligenceTest.ts`、`components/home/HomeIntelligenceResults.vue`与`HomeView.vue`。原ResultArtifact/ResultDetails仅增加可选读取函数，默认仍使用登录接口；调度、无限重试、租约、测试Key、计费和迁移未变。本轮仅静态核对，未运行测试/构建，历史pk.12动态结果仍只证明原范围。

**服务端定时智商测试。** 状态：有效。引入依据：2026-10-23 用户要求参考本机“鹈鹕测试”，在本站新增 SVG/糖果题菜单；追加总开关、管理员账户生成测试 Key，以及每次一键更新全部撤销旧测试 Key 后重建。目标版本 `v0.2.17-pk.5`。

2026-10-23 追加分组勾选，目标 `v0.2.17-pk.6`：用户只希望为自己选中的分组配置测试 Key，并明确本轮不运行测试。沿用本 ID；本轮仅静态核对，不将 pk.5 的历史测试结果描述为本轮通过。

2026-10-23 继续修订，目标 `v0.2.17-pk.7`：仅展示已选分组；保存配置后清空所有智商测试历史、取消旧任务并按新配置立即重新排队；糖果题展示当次发送文案与最终助手回复；鹈鹕和糖果桌面每行6张；新增安全失败诊断。沿用本 ID，本轮不运行测试或构建。

2026-10-23 用户明确要求“其它分组不需要排队，所有测试并发执行”，并补充按“分组数×2”动态并发，目标 `v0.2.17-pk.8`：取消此前全局2任务与整批等待限制，同时移除智商测试HTTP客户端的2连接上限；所有已选有效分组的已启用测试到期即独立启动。此授权仅改变本功能调度，不改变正常网关的账户/通道并发、权限、计费和审计。沿用本 ID，本轮仅静态核对、不运行测试或构建。

2026-10-23 用户反馈pk.8某个分组两项始终空白、重新保存也无效，授权直接查看服务器并要求修复后服务器回归通过再提交，目标 `v0.2.17-pk.9`。只读现场确认：网页重启跳过Cleanup留下该组旧租约，保存只清结果，租约到期后两项自动恢复。修复网页正常退出链及结果收尾与租约释放的独立性；不通过清除所有有效租约规避防重保护，不新增owner/迁移。

- 菜单采用“SVG / 糖果题 → 已选且有效分组”两层页签。设置区仍列出全部有效分组供勾选，结果页签、默认组、空态、键盘导航和结果请求统一按已生效的 `selected` 过滤。普通用户只读最终作品、糖果合格/不合格及当次题目/最终回复；正在请求及等待重试的状态为“Agent工作中”，失败结果不进入卡片列表。卡片均显示已保存的当次请求模型、推理强度及真实测试时间，终态显示本机单调时钟测量的整轮耗时（包含重试等待）；不公开推理事件、原始响应流、请求头或上游错误正文。两类结果桌面每行6张，小屏响应式1/2/3列，糖果长文在卡内滚动。
- 2026-10-23 pk.11按用户要求新增当次模型/强度显示：在领取任务、创建running记录的同一事务中，将应用全局配置及分组覆盖后的`settings.Model/Effort`写入`request_model/request_effort`。这与随后调用本站Responses的请求配置一致，普通网关后续模型/强度映射保持原行为，不将快照当作最终供应商型号。列表接口新增可空`model/effort`，卡片直接读取本记录，后续配置变化不改写历史。迁移250仅新增可空列，历史NULL显示“未记录”；新记录空effort表示未发送强度字段，显示“模型默认”，显式`none`等值原样显示。不回填旧记录、不改变旧迁移；文本使用Vue转义并允许长模型名换行。
- 2026-10-23 pk.12按用户明确要求自动重试所有未成功请求，取代此前一次失败即显示失败卡的行为。HTTP错误、超时、网络中断、响应不完整/无效及无效SVG均在同一running记录内重试，不限制总次数；退避1/2/4/8/16秒后封顶30秒，可被生命周期取消。每次尝试先建立5分钟context，再通过共享事务锁校验配置代次、Key及仍有效的原结果租约并续租6分钟，失去所有权或续租结果不确定时立即停止，不在无租约时发送。请求完成且糖果答案错误或无法匹配答案时仍记completed/不合格，不为得到正确答案继续请求。
- pk.12取消和持久化失败通过同条SQL原子清理本ID的running记录，并仅在原active_result_id仍匹配时释放/立即到期；已确认的终态及正常周期不受影响。planner也恢复过期activeID对应未完成或缺失结果的情况，避免遗留到未来周期。服务器隔离专项验证通过：真实PostgreSQL的8个子场景及后台生命周期9项均启用race、无失败/跳过，backend lint为0 issues；范围、原始报告与哈希见交接记录。本机未运行测试或构建，新增临时测试及控制脚本已删除。
- 设置保存在 `settings.pk_intelligence_test_config`。总开关默认关闭，关闭时隐藏普通用户菜单，直接访问结果/作品接口也被拒绝；管理员始终保留配置入口。SVG、糖果各有独立开关（默认启用，受总开关控制）、完整可编辑提示词、模型、强度、1～1440分钟间隔及分组模型/强度覆盖。默认模型 `gpt-6-astra`、强度 `medium`、间隔5分钟；糖果答案默认21，可随改题配置0～1000000整数。提示词原文发送，不暗中附加额外要求。默认SVG词要求SVG/CSS动画，默认糖果词显式包含最终答案格式。
- 每次成功保存（包括相同配置）在同一事务中更新配置、生成仅后端使用的代次、删除全部智商测试结果并将任务时间置为当前时间；随后取消旧任务、唤醒调度器。关闭总开关或全部类型时只清空并停止。保存与任务领取共用事务锁，旧代次不能再插入历史，旧任务只更新原ID且不能补回已删除结果。保留有效租约直到原worker退出，正常本实例无需等5分钟，健康跨实例按5秒检查取消；崩溃worker仍受最长6分钟旧租约保护，只影响同一分组同一类型的接替，不阻塞其它测试。前端保存成功立即清空列表、取消旧读取并重新加载，迟到的旧响应不能恢复历史。
- 管理员“一键生成 / 更新”每次在数据库事务和互斥锁内，撤销映射表记录的全部旧测试 Key、移除旧凭据，再仅为明确勾选的有效分组重新生成随机 Key，归当前有效管理员账户。名称以 `智商测试-分组名` 开头，凭据为后端专用 `sk-iq-` 前缀；`pk_intelligence_test_keys` 保存分组与真实 Key ID 的对应关系。不按名字接管或删除普通 Key，不从普通用户创建测试 Key。旧 Key 使用软删除和独立加密随机删除标记清除原凭据并保留必要计费历史，避免固定占位值与普通自定义 Key 唯一约束冲突；数据库写入或随机生成失败回滚整批轮换。pk.8轮换在提交前记录旧任务取消句柄，提交后仅取消该快照并唤醒调度，避免并发调度刚启动的新Key请求被迟到取消误停。
- Key区域提供分组复选框、全选、清空和已选数量，点击“一键生成 / 更新”生效，底部普通配置保存不执行 Key 轮换。分组接口的 `selected` 按现有映射回显，独立于 `key_configured` 的当前可用状态；没有映射的新分组默认不勾选，轮询不覆盖未提交的选择。首次分组读取成功前禁用更新，区分读取失败与合法空分组；同步失败或部分分组失败保留选择供重试。
- 更新接口必须显式提交 `group_ids`；缺字段/null拒绝，空数组明确表示撤销全部旧测试 Key且不重建。最多2000个输入ID，正整数去重；同事务核对全部选择仍为未删除的有效分组，非法选择在任何撤销操作前拒绝。更新成功后，未选分组没有Key映射，既有调度JOIN映射和发送前校验会停止其新任务，原有在途取消/跨实例检查保留；Key轮换本身不改总开关、不清空历史，也不新增分组选择配置字段或迁移。pk.7的保存清空及详情迁移独立于Key轮换。
- 分组授权、订阅、Key创建频率/数量、用户余额、模型白名单、内容审计、计费与并发限制继续走既有规则。权限不足的分组明确报告原因，不自动授权订阅或绕过收费；成功轮换后，此类组旧 Key 也撤销。普通分组授权/订阅需由管理员先配置，不能把“已生成”描述为真实模型调用已通过。新增组需手动勾选后更新才加入，已删除或停用组不重建，改名采用当前名称。
- 专用 Key 的通用列表、详情和嵌套使用记录 DTO 同样脱敏，不向浏览器返回原值，不提供输入 Key、地址或手动触发模型的接口。专用 Key 不能通过普通编辑换组；普通 Key 的创建、编辑、删除、收费及输出保持原行为。
- 调度器固定使用 `http://127.0.0.1:{server.port}/v1/responses`，端口取服务端配置，不接受浏览器参数，不使用环境代理、不跟随重定向。专用 Key 在所有网关鉴权入口额外要求真实回环对端地址和进程内生成的一次性随机凭证；不信任转发IP，只有预定的 POST Responses 入口可用。一次性凭证只在内存保存哈希，1分钟失效、使用即删除并移除请求头，日志脱敏包含该头。pk.8将待消费凭证容量扩为8192以覆盖2000组双类型瞬时并发，请求结束/失败/取消后清除未消费凭证；不按Key覆盖凭证，避免同组两种测试互相作废。
- SQL任务状态与租约防多实例重复执行，同组同类型不重叠；所有到期的已选分组/已启用类型并发启动，并发任务数按实际分组数动态计算：N组双类型为2N项，关闭一项时为N项，不写死为8个。智商测试调度器或HTTP客户端不再两项一批。调度扫描独立于模型完成，不因其它慢任务阻塞已到期任务；每项正常成功仍按自身间隔运行，每次请求5分钟超时。停止/关闭/重配取消旧任务和退避等待，跨实例每5秒检查配置和Key轮换。取消或失去租约时清理未完成记录并只按自身ID释放/重新到期；进程异常仍保留原6分钟安全租约，过期后清理未完成记录，以新ID重新领取，不能复用失联worker的旧ID而破坏fence。旧failed历史仅从列表隐藏，不改写为成功。网关已有账户/通道并发与限额继续按正常请求生效。
- pk.9任务HTTP结束后，结果写入与租约释放分开处理：总收尾预算仍为5秒，结果写入最多2秒，释放使用独立最多3秒上下文并有界重试；只按分组、类型及原`active_result_id`释放，不能误清新任务。结果已被保存清空或结果写入失败也执行释放；数据库持续不可用时保留租约自然到期，不伪造释放成功。正常网页重启应先取消/等待这些收尾再退出；强制杀进程、断电或持续数据库故障仍受原租约到期保护。
- 每个功能每个分组最多30条完整结果，插入时同事务删除更旧记录；删组后清理对应结果与调度状态。单作品/最终助手回复最多256KiB，单响应最多4MiB，不保存推理事件或原始流。糖果题在领取任务时保存当次完整提示词快照（最多100000字节），仅在完成时保存最终助手回复；原有合格判定保留。迁移249仅新增有默认值的详情列及长度/终态约束，不修改旧迁移；历史记录缺失的文案、回复与原因明确显示不可还原，不能用当前配置冒充当次题目。原有网关计费与审计记录遵守原保留策略，不因测试结果轮换或配置保存删除账务证据。
- 原控制台列表只返回结果元数据，作品和详情继续通过带登录、功能门控及既有管理面限流的只读JSON接口按需读取；pk.14新增的公开展示使用本节单独登记的只读接口，不取消原接口鉴权。前端仅缓存当前最近30条，切组/类型或保存清空时释放并取消在途读取。错误保存固定分类与最多2048字节的脱敏说明；HTTP仅提取最多32KiB JSON中的指定message字段，不保存原始响应体/请求头，详细说明仅管理员可读。SVG/HTML先净化再放入无脚本、无同源权限的iframe和严格CSP，支持SVG/CSS动画，不执行JavaScript或外部资源。糖果文字采用转义插值，不作为HTML执行。管理员提示词应使用可在该预览方式中展示的作品格式。
- 入口：`backend/internal/service/intelligence_test_*.go`、`backend/internal/handler/intelligence_test_handler.go`、用户/管理员routes、两类API Key鉴权中间件、Key DTO和编辑保护、`backend/migrations/{248_intelligence_tests,249_intelligence_test_details,250_intelligence_test_request_metadata}.sql`、`backend/internal/service/wire.go`及`backend/cmd/server/{wire,wire_gen}.go`；前端 `api/intelligenceTest.ts`、`stores/intelligenceTest.ts`、`views/user/IntelligenceTestView.vue`、`components/intelligenceTest/`、菜单/路由和中英文文案。
- pk.5 验证与边界：用户要求测试后，最后明确改为服务器隔离测试（此前本机前端检查结果单独记录），执行后清理新建临时测试文件；实际范围/结果见交接记录，不把局部源码检查称为完整或生产运行时通过。未连接生产、未实际消耗模型Token；Jenkins发行构建与同制品十一项验收仍由用户触发。分组须具有可用模型和本站Responses兼容能力；原有仅SystemOne平台限制不被绕过。完整范围与静态依据见 [智商测试交接记录](docs/PK_INTELLIGENCE_TEST_REVIEW.md)。
- pk.9验证：服务器独立DinD内预检和完整九套件全部通过，额外unit/integration竞态回归通过，覆盖SIGTERM清理、4组双类型并发、Stop取消、保存代次及真实PostgreSQL租约隔离/写入失败；测试树与原报告SHA256见同一交接记录。本次3个临时测试文件测完删除，原有测试保留；删除后生产实现与测试树完全一致，不将含临时测试的报告绑定为不同最终tree的notes。本机未运行测试/构建，生产仅只读排查，Jenkins制品验收另行执行。
- pk.10交接：2026-10-23按用户明确要求递增版本并重推分支，完整继承pk.9业务实现及所有有效二开；只同步版本交接文档，不新增功能、配置、迁移或测试。既有服务器证据保持原tree归属，本轮不重新执行测试/构建；正式标签为`v0.2.17-pk.10`，旧标签保持，用户自行启动Jenkins正式发行后再网页更新。

## PK-033

**联系方式配置与页面展示。** 状态：有效。引入：2026-10-24 `v0.2.17-pk.14`，用户要求系统设置增加联系文字input与二维码上传，在首页、登录页显示；追加首页顶部联系方式锚点、醒目展示和登录后用户仪表盘展示。

- 系统设置站点区域增加“联系方式配置”，文字复用已有 `settings.contact_info`，保留兑换页、个人资料及顶栏原入口和历史值；二维码新增 `settings.contact_qr_code`，默认空，无需数据库迁移。两项独立可选，trim后无文字/无合法二维码则整个展示区和首页联系方式锚点隐藏；只填一项只显示该项。二维码可上传、预览、移除，随原系统设置保存生效。
- 二维码仅接受PNG/JPEG/WebP Base64 data URL，原图不超过2MiB、每边1～4096px，后端先检查编码大小/真实图像头与尺寸，再完整解码并规范化Base64；拒绝SVG、远程地址、类型不匹配、损坏或超限图片。前端上传限制相同类型/大小，公共展示只接受对应栅格data URL。文字采用Vue转义，长内容换行；二维码保持完整比例和白底，不裁剪。
- 管理员读取/保存、审计、公开settings、HTML注入和前端缓存完整贯通；缺字段保留原值，显式空字符串清除，二维码null拒绝。沿用既有管理员鉴权、step-up、设置审计与缓存刷新；仅审计字段变化，不记录图片正文。不改变站点Logo上传策略及其他设置的局部保存语义。
- 默认/简洁/自定义首页顶部有条件显示“智商测试”“联系方式”两个锚点，点击滚动到对应区域，尊重减少动态效果偏好并移动键盘焦点。联系方式使用项目统一白底圆角卡片、柔蓝图标、细边框与轻阴影，文字紧凑分层，二维码保持白底并置于右侧；登录表单下方采用更紧凑联系卡片；用户仪表盘顶部展示联系卡片。所有入口同步设置，未配置不留占位。现有首页自定义内容、登录认证流程、仪表盘统计查询和菜单权限保持原实现；沿用PK-031品牌与256px侧栏。
- 2026-10-24 `v0.2.17-pk.16` 用户提供截图，明确要求调整pk.14深蓝大块联系方式样式以符合项目。沿用本ID与PK-031色板，直接复用全局card；调整为左侧40px柔蓝图标、右侧标题/18px联系文字，收敛为20px上下内边距和128px二维码，登录变体16px正文及112px二维码；按实际容器宽度换行、长文字可换行不溢出，保留深色模式与锚点焦点。仅改共享组件样式和结构，仪表盘仍只有顶部联系方式，首页智商测试及保存/隐藏规则不变；未运行测试或构建、未新建测试文件。
- 2026-10-26 `v0.2.18-pk.2` 服务器安全扫描发现该WebP解码调用涉及GO-2026-6222/5061/4961；将 `golang.org/x/image` 从v0.41.0最小升级到v0.45.0，保留图片格式、大小/尺寸检查、完整解码和管理员权限，不扩大审计豁免。沿用本ID，实际验收以修复后tree的服务器完整报告为准。
- 入口：后端`setting_contact.go`与setting keys/parse/update/public/DTO/admin handler/audit链；前端`components/common/SiteContact.vue`、`composables/useSiteContact.ts`、`SettingsView.vue`、`HomeView.vue`、`auth/LoginView.vue`、`user/DashboardView.vue`、settings API/types/app store及中英文文案。`ImageUpload.vue`仅增加可选图片类型限制，其余上传默认行为不变。
- 验证与边界：静态核对完整保存/公开/注入链、局部保存和隐藏条件、锚点及响应式布局；扩展仓库原有设置校验、部分更新、公开注入与API契约回归文件，未新建测试文件，未运行任何测试或构建。二维码合法图片校验不保证其内容一定为可扫描二维码；未执行浏览器视觉验收，不冒称通过。本轮未发布制品或操作生产。

## PK-034

2026-11-02 v0.2.19-pk.42：账号糖果测试仅针对明确配置的来源分组，首条匹配生效；全部启用模型完整合格后移除原全部分组并加入所选多个合格组，不合格沿用首个完整错答立即转单个目标，技术失败/取消不新增转组。内置文本模型及当前账号映射候选通过管理员只读接口提供，自定义模型ID保留，原1～8模型及并发/请求路径不变。无规则手动入口明确拒绝，自动调度跳过。详见PK-035及本轮核对记录。

2026-11-01 v0.2.19-pk.35：共享账号糖果请求加入同账号/同出口/同TLS配置最多3次握手尝试；自动授权测智、雷达逐模型与账号手动糖果共用。只对发送阶段明确TLS握手超时且未发送HTTP请求/未取得响应的失败短退避重试，每次Clone并重建body，不重复HTTP401/403/429、正文读取超时/解析错误或完整错答案；取消/上层超时立即停止，不放宽TLS校验、不换代理、不直连兜底、不清共享连接池。普通账号连接测试和正常网关转发保留原行为。最终握手失败显示固定脱敏说明，不再仅显示泛化“账号测试请求失败”；完整21/非21及组准入判定不变。

重试在account_candy_transport.go集中实现，Responses/Chat糖果发送入口共用，间隔1秒/2秒。仅匹配带net.Error证据的标准net/http TLS握手超时，httptrace保护已发请求/已收响应；不覆盖自定义uTLS另一包装或不透明插件错误。既有Agent Identity恢复独立保留，3次按每次HTTP派发计；普通OAuth现场错误在本轮范围内。服务器保留原脱敏错误诊断，新重试日志仅含次数/退避，未发模型请求验证。

2026-10-31 v0.2.19-pk.33：授权内测智在凭据提交后等待60秒再执行，模型仍全部并行且完整结果判定不变。已有账号重授权保留原组及恢复后的调度，只有完整结论才按当前规则转组；请求失败不倒退授权。首次新账号继续隔离直到测智/准入完成；普通手动糖果测试与雷达定时测试不增加此等待。

2026-10-31 v0.2.19-pk.31：授权新重试任务的不合格目标重新读取pk_radar_account_config，实际转组仍按原规则读取并锁定当时的当前目标；糖果配置保存链本身保持。模型与推理强度快照、完整答案判定、并行和分组准入不改。

2026-10-31 v0.2.19-pk.20：账号管理糖果列与历史详情合并读取已完成或正在测智的工作台授权结果。独立origin=authorization避免任务ID与雷达ID冲突；账号归属及完整管理员校验保持。新旧记录按实际开始时间择新，原生雷达运行中优先，generation有记录时按当前版本判定，旧授权无generation保留真实历史结论并标记来源，不伪造当前轮次。

2026-10-31 v0.2.19-pk.19：授权准入使用糖果设置中全部启用模型，同账号最多8个并发；沿用糖果原生请求、完整答案及技术错误区分，授权任务独立保存逐模型结果。原雷达测试、提前不及格转组和账号调度不变；授权关卡等待本轮结果全部结束后分组。详见PK-036及授权测智核对记录。

2026-10-31 v0.2.19-pk.18：授权测智关卡改为读取本功能现行雷达配置，不再读取迁移前settings中的旧糖果配置；目标分组和首个启用模型与糖果设置页面一致。详细边界与静态核对见PK-036及docs/PK_AUTHORIZATION_GATE_REVIEW.md。

**账号级糖果测试、定时检查与不合格转组。** 状态：有效。引入：2026-10-30 `v0.2.19-pk.1`。用户要求按截图在账号列表直接测试账号，并增加独立定时配置；后续明确这不是分组测试、首版覆盖 OpenAI 全部账号类型，非21时移除全部原组仅加入指定组，请求失败不转组。

- 仅完整权限管理员管理。账号列表的 OpenAI 行提供“糖果测试 / 详情”和当次模型、结果、时间；账号页独立“糖果测试设置”入口，保留原连通性测试及 PK-032 的分组测试。测试固定使用所选账号，不由分组调度选择其他账号；其他平台及合成 UI 测试账号不执行本测试。OAuth、API Key 和影子账号沿用各自真实凭据、模型映射、代理及协议路径。
- 配置独立存于 `settings.pk_account_candy_test_config`：`enabled=false`、`interval_minutes=60`、`model=gpt-6-astra`、`effort=medium`、`target_group_id=null`。间隔允许1～1440分钟，模型/强度独立配置；题目固定为既有糖果题原文加最终答案格式，合格答案固定21，不能从请求注入其他题目、地址或凭据。自动开关不限制手动入口；未配置目标时手动只记录结果，启用自动测试需选择有效的 OpenAI 目标分组。
- 定时任务按账号独立到期，不依赖已选测试分组或专用分组 Key；只处理未删除、启用且可调度的 OpenAI 账号，跳过已在目标组及按设置自动暂停的过期账号。每实例最多8个并行任务，每次最多5分钟，数据库6分钟租约防止同一账号多实例/手动/自动重叠。目标组内可手动复测，但合格后不会自动恢复原组。
- 仅收到完整协议成功终态和非空最终助手回复后判定答案；合格21保持当前分组。完整回答不合格（包括无法识别出合格答案）且有目标时，删除该账号全部原分组绑定并只加入目标组。HTTP/网络/超时、取消、协议失败或不完整响应记为失败/取消，不转组。只保存最终回复，不保存推理流或原始上游错误、请求头与凭据。
- 转组、终态结果和调度缓存 outbox 在同一数据库事务完成。转组前重新检查配置代次、原任务租约、账号、目标组状态与准入限制；比较启动时原分组快照，管理员已经改组则保留当前组并记录原因。目标组被删除/停用或不满足准入条件时保留原组，明确区分“测试不合格”与“转组未完成”，不假报已转组。旧任务不能覆盖新任务、配置或管理员变更。
- 请求使用事务前取得并在领取锁内核对的账号/母号快照，转组时再次比对身份摘要；凭据、模型映射、代理连接字段及已选协议/Kilo/TLS配置变化时保守跳过。摘要和凭据仅存内存，不进入结果/日志。配置 PUT 必须提交五个字段，仅目标组可为 null。结果模型/强度是配置请求快照，后续账号映射和 Kilo 强度转换仍沿用原规则。Responses 的 incomplete_details、未完成项或工具输出与 Chat 的非 stop/缺少 DONE 都不能冒充完整答案。
- 过期未完成任务的清理独立于自动开关：异常退出后的手动任务也会在租约到期后恢复可重试状态，只清理仍匹配原 active_result_id 的记录。正常停止等待本轮请求/结果收尾，重复或并发停止不清除后来启动的新生命周期。调度缓存继续通过既有 outbox 异步更新，事务提交不等于所有实例缓存瞬时同步。
- 新增迁移 `251_account_candy_tests.sql`，独立 `pk_account_candy_results` / `pk_account_candy_state`；每账号保留最近30条记录，保存触发来源、当次模型/强度/题目、最终回复、识别答案、原分组和实际目标、错误/转组原因、开始/结束和真实耗时。最终回复最多256KiB。配置保存切换代次、取消旧任务并重新计算到期；历史结果保留，不清空分组测试或改写历史账务。
- 接口：`GET/PUT /api/v1/admin/account-candy-test/config`、批量最新结果 `GET /api/v1/admin/account-candy-test/results?account_ids=...`（最多200个）、`POST /api/v1/admin/accounts/:id/candy-test`、`GET /api/v1/admin/accounts/:id/candy-test/results/:result_id`。沿用管理员认证、合规门控、管理面限流及写操作审计；受限管理密钥不获得新路由权限，handler 另作拒绝。只读接口不启动模型，返回 `no-store`，详情绑定账号和结果ID。
- 前端仅刷新当前页账号（每5秒，页面隐藏暂停），切页、关闭和卸载取消过时读取；新测试不能被旧响应覆盖，实际转组后刷新账号列表。详情内容按文本转义，失败不会显示为不合格移组。新功能执行语义沿用管理员账号直测，不创建用户模型账单，不改变正常网关用量、缓存转换、Kilo计费、额度或 PK-032 分组测试账务。
- 入口：`backend/internal/service/account_candy_*.go`、账号测试的私有糖果模式适配、`handler/account_candy_test_handler.go`、管理路由及 service/handler/server Wire；前端 `api/admin/accountCandy.ts`、`composables/useAccountCandyTests.ts`、`components/admin/account/AccountCandyTest*.vue`、`views/admin/AccountsView.vue` 与中英文文案。
- 验证：本轮仅静态核对请求固定账号、完整回复判定、事务/租约、权限/缓存、前后端契约与生命周期；既有最小依赖 Cleanup 回归同步新增服务参数。没有运行本地构建、测试、服务器回归或真实账号调用；没有新建临时测试文件。不将代码存在或历史报告写为本轮动态通过；Jenkins 制品验收仍由用户触发。

2026-10-30 `v0.2.19-pk.3`：服务器专项在 pk.2 复现 upstream 类型报“无 API Key”。仅在糖果测试私有分支为该类型读取当前账号已保存的 `api_key`；普通连通测试的类型限制和其他平台/类型的取密钥方法保持不变，地址校验、模型映射、代理和完整回复判定继续复用原链。既有 `account_test_service_openai_test.go` 增加 `TestPKAccountCandyUpstreamAccountUsesConfiguredKey`，同时检查正常请求与普通测试仍拒绝该类型。其他专项覆盖仍使用测后删除的临时测试文件；修复后实际是否通过以新一轮服务器报告为准。

同轮完整回归发现 `account_candy_probe.go` 的三处 `strings.Builder` 写入未显式处理返回值。按仓库既有写法补齐 `WriteString`/`WriteByte` 的返回值赋值，不改变 SSE 拼接、完成态、长度上限、答案判定或转组规则，也不放宽 errcheck。最终完整回归和账号专项须对新 tree 重跑，临时测试文件测后删除。

2026-10-30 `v0.2.19-pk.4`：线上 OAuth 手动糖果测试记录显示响应解析失败，答案未判定、分组未改变；原始上游正文未保存，不能从泛化错误唯一确认某个事件或响应头。代码核对确认糖果已经复用原生 OAuth 身份、固定 Codex 地址、模型映射、代理和传输，并非把 OAuth 当作 API Key。本轮修复二者的解码兼容差异：

- 依据受限正文识别 JSON 或 SSE，不再仅因缺失、非规范或错误声明的 Content-Type 拒绝合法流；仍须通过完整协议和最终助手文字校验，HTML、无效 JSON、错误与不完整响应不能成为有效答案。
- Responses 支持原生连接测试兼容的 `response.completed` 与 `response.done`；继续要求 `response.status=completed`、无错误/未完成原因、无工具输出及非空最终助手正文。仅收到 delta、`[DONE]` 或 EOF 不代表成功，禁止用部分答案触发转组。
- 兼容原生连接测试可接受的 SSE 行前导空白、BOM 及空白分隔行；data 载荷尾随空白仍参与事件长度统计，保留 4MiB 总响应、1MiB 单行/事件和 256KiB 最终答案限制。读取、大小、JSON/协议及缺失完成标记使用固定安全说明，不存原始上游错误、请求头、令牌或推理文本。
- 普通账号连接测试、独立糖果模型/强度/定时配置、固定账号选择、答案21判定、租约/快照及事务转组保持。扩展仓库既有 `account_test_service_openai_test.go` 的解析边界断言，不新建临时测试文件。
- 用户明确本轮不跑回归，故仅做代码和差异静态核对，没有执行本机/服务器构建、测试或真实模型复测；不得引用 pk.3 的源码/制品报告宣称 pk.4 通过。历史失败记录保留，生产配置与账号数据不修改，Jenkins 构建和上线仍由用户另行操作。沿用 PK-034/021，无新增 ID。

2026-10-30 v0.2.19-pk.6：账号列表将糖果模型、结果、手测与详情移至独立「糖果测试」列，不再堆叠于平台/类型列。多模型已有失败答案分别列出，汇总不再把空聚合答案误报成「未识别出有效答案」。糖果 HTTP/JSON/Responses SSE/Chat 错误提取 message/detail、code、type、param，保存前移除已知账号/母号密钥、代理认证、授权头、令牌和 Cookie，并限长2048字符；不保存完整错误正文或推理。传输错误仍采用安全分类，技术错误不触发转组。旧泛化错误没有原文，历史记录不回填。既有解析边界断言同步并补充脱敏覆盖，但本轮未运行；详细静态依据见 [pk.6 核对](docs/PK_RADAR_0.2.19_PK6_REVIEW.md)。

## PK-035

2026-11-02 v0.2.19-pk.42：`RadarConfig.group_rules`为有序数组，每项包含`source_group_id`、非空去重`pass_group_ids`、`fail_group_id`。规则复用settings JSON，与generation同事务更新；来源和所有目标均校验有效OpenAI组。无配置时不测试，旧全局目标仅保留历史授权任务兼容；已配置多个来源的账号按第一匹配只运行一轮。调度、claim、逐模型请求和最终事务复核来源/账号快照，人工改组不得覆盖；合格多目标逐项准入后整体提交，失败保留早转组证据。雷达结果扩展完整实际目标列表，旧单目标字段及历史记录保持兼容。前端支持规则增删、排序和合格多选，未配置显示原因；模型下拉汇总内置目录/账号映射/已保存模型，并保留自定义输入。代码入口新增radar_model_candidates.go及对应管理员handler；静态核对见docs/PK_CANDY_GROUP_RULES_PK42_REVIEW.md。

2026-11-02 v0.2.19-pk.40：移除雷达内容区重复大标题/装饰图标，统一顶部标题和说明；账号糖果设置按钮移至页签工具栏，查看智商测试结果、刷新提示、五个页签与管理员权限保持。

2026-11-01 v0.2.19-pk.38：增加独立“邮件配置”页签，配置和发送结果由PK-038提供；原账号/分组观测、授权巡检及健康统计入口与行为保持。

2026-11-01 v0.2.19-pk.35：授权任务区分首次直接准备测智与重授权等待60秒；糖果传输重试成功继续原结果归档、逐模型判定与雷达同步，耗尽尝试仍为请求失败、不是不及格。已有任务/测智历史不回写。

2026-10-31 v0.2.19-pk.33：授权任务显示等待60秒倒计时和缓存未就绪阶段，已有账号任务记录当前保留分组，成功分组后更新结果；测智请求失败/进程中断说明区分已有账号恢复与首次入库隔离，现有雷达观测和逐模型结果映射保持。

2026-10-31 v0.2.19-pk.31：立即/定时重授权优先保留原生当前非删除组关联；账号无组时读取settings.pk_authorization_workbench.pass_group_ids，与工作台展示来源一致，避免旧pk_authorization_config.group_ids把已删除组带回。仅未保存过workbench选择时兼容旧fallback；显式空选择不复活旧配置。

2026-10-31 v0.2.19-pk.27：授权巡检操作列为expired/credentials_completed/missing_credentials提供“立即授权”（缺完整凭据禁用）；按行账号ID提交、使用最新历史快照，并在执行前再次检查原账号及当前错误码。已恢复、删除、封禁或同邮箱已有任务时拒绝/跳过；自动重授权开关关闭不禁止管理员显式操作。继续显示排队/授权中及任务消息，不改账号统计和糖果巡检调度。

2026-10-31 v0.2.19-pk.26：新增“账号统计观测”独立tab，展示当前统计账号、每批数量与进度、各账号成功/失败/跳过状态及健康度、最快/最慢首字。完整管理员可设置5至1440分钟周期、1至168小时窗口和启停；默认30分钟/24小时。路由query和键盘切换支持新tab，定时刷新仅取持久化进度，不能触发日志聚合；分组、账号糖果和授权巡检路径保留。

2026-10-31 v0.2.19-pk.20：账号观测分页/总数/合格筛选统一采用原生雷达与授权关卡的有效最新判定；导入不及格按verdict=fail自然触发既有浅红整卡，不用分组成员关系冒充测试。读取原授权JSON中的逐模型结果、时间、分组及出口；历史只有单模型结论时仅展示已记录模型，缺失IP/完整正文不补造，旧测智时间缺失明确回退到授权任务时间。新授权每模型记录实际开始/结束、耗时、映射模型、同账号代理出口和正文；列表去除正文，详情才返回。目标组内停止定时的规则不变，升级不触发补测或改写历史测试数据。

2026-10-31 v0.2.19-pk.19：授权准入使用糖果设置中全部启用模型，同账号最多8个并发；沿用糖果原生请求、完整答案及技术错误区分，授权任务独立保存逐模型结果。原雷达测试、提前不及格转组和账号调度不变；授权关卡等待本轮结果全部结束后分组。详见PK-036及授权测智核对记录。

2026-10-31 v0.2.19-pk.18：授权测智关卡改为读取本功能现行雷达配置，不再读取迁移前settings中的旧糖果配置；目标分组和首个启用模型与糖果设置页面一致。详细边界与静态核对见PK-036及docs/PK_AUTHORIZATION_GATE_REVIEW.md。

2026-10-31 v0.2.19-pk.9：增加独立「授权巡检」页签，设置和状态由 PK-036 提供。账号测智 dispatchRadar 每次调度仍直接查询最新 accounts 与当前资格条件，手动新增账号在下一次符合条件的调度进入；未缓存名单，原多模型糖果、转组和分组测智逻辑保持。

**雷达观测站与多模型账号观测。** 引入版本：v0.2.19-pk.5。

- 管理员侧栏位于仪表盘下方，前端路由与后端 API 均要求管理员；账号观测不公开，不继承原智商测试的用户结果权限。
- 分组页签复用 PK-032 配置和任务，原智商测试页仅保留结果。账号列表手动糖果测试和设置入口保留，与雷达共用新配置、状态及结果。
- 两个观测开关独立，首次开启并保存后分别立即唤醒执行首轮，后续按各自间隔；关闭其一不影响另一方。分组调度启动等待可被配置保存唤醒，账号保留现有立即通知；符合条件的对象按既有并发预算执行，打开页面不自动启用。
- 支持 1～8 个不同模型，至少启用一个。每模型沿用 PK-034 原生账号凭据、地址、模型映射、TLS与代理链路，不使用分组Key。任一完整最终答案非21即不合格；仅请求错误、取消、不完整响应不转组，逐项显示全部不合格模型和答案。
- 全部合格模型均完整完成才记合格；部分技术失败且无答错记未知。自动转组仍移除原全部分组，仅加入目标，事务内复核账号身份、分组快照与目标准入，并写既有调度outbox。
- 单模型错误说明仅表示该失败不作为转组依据，不再借用单模型版“未转组”描述整个批次，避免与另一模型答错触发的合法转组矛盾。
- 新建 pk_radar_account_config/models/runs/model_results/state 五张表；旧配置只读初始化一次，旧历史原位保留。配置代次CAS、账号租约token、续租和终结事务防止旧任务覆盖新配置；账号连接变更取消剩余调用。
- 当前页卡片复用平台/用量/分组组件，展示最近/下次时间、逐模型结果、糖果降智判定和出口IP。合格筛选由数据库先筛选计数后分页。用量读现有批量接口。
- 每模型调用前通过同一代理配置请求固定 HTTPS IP 探测服务，不发送账号密钥。无代理才直连；绑定代理失效即失败关闭。轮换代理的观测IP与模型连接IP可能不同，界面明确说明。IP探测失败不改路由、不凭此转组。
- 新结果最多保留每账号最近30轮，详情同时读取旧表最近30条，以 origin 和 id 区分。旧客户端写接口返回409要求刷新，避免旧单模型设置覆盖新模型列表。
- 糖果设置、分组测智和币安页面的14处原生选择框改用项目 Select；数值ID、TTL、空继承值、null目标组、币安金额与结算逻辑不变。沿用 PK-022～024/031。
- 后台复用 PK-019 现有受控服务生命周期，不额外启动第二份调度器。升级前排空旧进程；回退保留新表，旧版读取旧设置，已发生合法转组不会自动撤销。
- 入口：backend/migrations/252_radar_observatory.sql、backend/internal/service/radar_*.go、backend/internal/handler/radar_account_handler.go、frontend/src/views/admin/RadarObservationView.vue、现有 AccountCandyTest* 组件。详见 docs/PK_RADAR_OBSERVATORY_PLAN.md。


### v0.2.19-pk.6 反馈优化

- 同账号全部启用模型并行请求，每模型持有独立、经身份摘要核对的账号/母号对象，避免共享可变模型映射缓存。每实例最多8个账号、每账号最多8个模型，即最多64个模型调用；单模型预算5分钟，整轮6分钟，租约6分钟并每30秒续约。分组调度预算不变。
- 首个完整非21答案立即在可串行化事务中提交模型结果、不合格判定、全部原组替换和outbox；短事务按本账号串行提交，网络调用保持并行。后续不合格模型只记录自己的结果，不重复成功转组；其他模型继续完成。配置代次、租约、身份、原组快照和目标准入检查保留。
- 转组前取消或配置/身份变化仍禁止转组；合法提前提交后的取消、技术错误、租约过期和最终收尾保留已完成的不合格与转组证据。新代次筛选不混用旧代次判定，后续管理员改组不被最终收尾重新覆盖。
- 卡片平台/类型与用量并排，去掉用量操作按钮，组名紧跟邮箱；字体按用户要求减半并缩小间距。AccountUsageCell 的 showActions 默认 true，仅雷达传 false；账号管理原用量操作保留。每个模型直接显示具体脱敏错误，详情同样可见。
- 沿用现有五张雷达表与旧历史兼容，不新增迁移、不回填旧记录；管理员菜单、路由及后端鉴权保持，分组观测及正常模型网关计费路径不变。
- 本轮仅执行格式化与静态差异/权限/生命周期核对；没有运行本地或服务器构建、回归、真实模型测试，没有新建临时测试文件。用户自行点击 Jenkins 打包并更新，pk.5 历史报告不作为 pk.6 验收。

### v0.2.19-pk.7 字体与四列布局

- 根据用户新截图撤销 pk.6 过度缩小字体的展示规则：账号名14px、正文/按钮/错误12px、邮箱/时间/用量辅助信息至少11px，并提高浅色辅助文字对比度。继续使用项目系统字体、柔蓝色板和深色模式。
- 账号卡片从1280px视口起每行4张，1024～1279px为3列，768～1023px为2列，小于768px为1列；现有每页12条对应桌面3行，不虚构缺少的账号。
- 平台/类型与用量仍处于同一行，两侧允许徽章或用量明细在窄卡内换行，避免放大字体后溢出。字体覆盖只作用于雷达卡片，账号管理共享组件样式保持原样。
- 去掉卡片顶部状态色条；仅 verdict=fail 的账号使用整卡浅红背景和柔和红色边框，出口区与页脚同步适配，深色模式用低饱和暗红底。保留状态文字和逐模型错误，未知、运行中或仅请求失败不会因错误被染为不合格。
- 仅修改 RadarObservationView.vue 的模板类名和 scoped CSS，不改脚本、后端、迁移、凭据、模型并行、即时转组、独立开关、筛选或权限。静态差异核对与 git diff --check 已执行；没有本地/服务器构建、测试或浏览器视觉验收，不将 pk.6 证据当作本轮通过。

### v0.2.19-pk.8 标题、分页与柔和失败态

- 账号标题使用可收缩容器与单行省略号，保留完整名称 title 提示；状态徽章不被挤出卡片，不改原账号名与邮箱数据。
- 雷达账号页独立渲染紧凑圆角分页：去掉表格式白色横条，保留结果范围、首尾页/当前页及省略号、前后翻页、当前页高亮、越界保护与键盘焦点。移动端显示当前页/总页数；继续复用既有分页翻页监听、服务端过滤/计数和每页12条，不修改通用 Pagination。
- 不合格整卡底色进一步弱化为 #fffafa，红色边框、出口区、状态标签、模型失败底色及页脚同步减淡，仍以可读文字表达失败；深色模式降低红色覆盖强度。仅由既有 verdict=fail 触发整卡失败态，技术错误不会新增不合格判定。
- 保持 pk.7 的字号与桌面四列布局，以及模型并行、即时转组、独立开关、代理出口与管理员权限。仅 RadarObservationView.vue 的模板、分页显示计算/翻页入口和 scoped CSS 变化；后端及迁移无变化。
- 本轮仅静态差异、现有调用链及 git diff --check 核对，未运行本地/服务器构建、测试、浏览器视觉验收或真实模型调用；没有生成测试文件，也没有线上变更。交由用户点击 Jenkins 正式打包与更新。

## PK-036

2026-11-02 v0.2.19-pk.42：授权工作台`intelligence_gate`可独立保存，新环境默认false，历史保存值保留。关闭或来源无规则时不测智：新账号按所选入库组原生准入，已有账号保留原组/代理/人工停调，仅沿用系统错误恢复。开启时已有账号按原组、新账号或无组账号按入库组匹配雷达第一条规则，合格加入全部pass_group_ids，不合格单目标；新账号待测智时仍无组停调。立即/定时授权读取已保存开关，失败重试保留原有效开关/模型并核对来源规则；旧任务兼容但新任务不再隐式套全局目标。独立workbench接口以md5 revision CAS防覆盖，保存仅白名单非敏感字段且审计省略正文；巡检revision不混用。规则移除/停用后等待、发送前及运行期可取消；最终规则/账号/目标在事务内锁定复核，防部分转组。保留PK-019启动门、持久worker、并发、cron、凭据归档和账号准入。详见docs/PK_CANDY_GROUP_RULES_PK42_REVIEW.md；仅静态核对，未运行真实授权或模型请求。

2026-11-02 v0.2.19-pk.41：按用户指定，将授权工作台现有测智关卡说明卡从右侧配置列移到左侧账号输入区下方；卡片内容、样式、动态模型列表及全部提交/授权/测智规则保持。桌面左右分栏，小屏按账号输入、关卡说明、参数配置顺序展示。

2026-11-02 v0.2.19-pk.40：移除账号授权内容区重复大标题，顶部标题和说明保留；查看授权巡检入口移至页签工具栏，工作台/代理/历史与所有授权、调度、凭据和分组逻辑保持。

2026-11-01 v0.2.19-pk.36：账号授权工作台未保存参数时，账号并发默认20、优先级默认1、每个原生IP最多绑定账号默认3。GetConfig新增独立workbench_account_concurrency/workbench_account_priority只读返回字段，先按新默认初始化再读取settings.pk_authorization_workbench，保留已保存值及显式优先级0；前端初始化和加载缺省值同步。IP上限仅缺失/0时补3，非零保存值不改。原通用account_concurrency/account_priority继续来自巡检配置表，SaveConfig、定时/立即重授权及旧迁移不改；新账号负载因子仍等于实际并发，授权并发和代理模式默认保持，已有账号与历史任务不批改。

2026-11-01 v0.2.19-pk.35：首次新增账号保存后立即读取最新凭据/刷新缓存并测智，仅applyAuthorization实际更新已有账号（before非空）的工作台同邮箱、立即/定时重授权及其重试等待60秒；不按任务source误判新旧账号。替代pk.33对所有导入都等待的规则，其余快照、缓存、取消、已有账号分组/调度恢复保持。日志与源码表明首次新账号schedulable=false不阻止按ID直接测智，因此继续无组停调直到判定及准入完成，不按未经证实的原因提前开放调度。糖果握手重试沿用PK-034共享实现，不改登录代理/浏览器重试边界。

2026-10-31 v0.2.19-pk.33（用户最新恢复要求）：凭据交换依旧同步完成，事务落库后在当前worker内等待至少60秒、可取消/受总超时与租约约束；复用已有cron，不新增定时任务或独立调度器。等待后重读账号、失效token缓存，再发全部模型测智请求；缓存失败明确暂停本次测智并由原cron继续刷新，不回退新凭据。

已有账号（工作台同邮箱更新、立即/定时重授权及重试）保留原分组；原status=error由原生ClearAccountError与SetAccountSchedulable(true)恢复，提交后执行运行时阻断清理、outbox与调度快照同步，不再为测智重新停调。人工inactive、active但schedulable=false继续保持。完整测智按及格多组/当前糖果不合格组及原生准入规则变更；请求失败、不完整、取消或中断不回退凭据/已有组/恢复后的调度。首次新建账号仍无组停调，只有完整测智和分组事务成功才开启。此条取代历史pk.24所有重授权也清组停调的描述。

缓存实现新增仅授权流程使用的InvalidateTokenStrict，真实删除错误能进入cache_pending，禁止将仅记录日志的旧尽力清理当作成功确认；已有InvalidateToken调用的返回行为不变，不改变其它平台业务。

授权保存同时在账号行锁内单调推进原生_token_version，避免仍持有旧账号对象的请求沿用相同代次重新填充旧token缓存；复用CheckTokenVersion，不修改计费/普通网关协议，也不把代码竞态描述为截图失败已被实测证明。

在凭据事务中捕获identity（凭据/代理/连接配置）、status、schedulable与组集合；等待后、逐模型及最终分组事务比较同一快照，拒绝覆盖期间人工变更。已有账号入库事务也检查授权期间组变化。jobs.settings记录已有账号保留策略和现有分组以供任务/中断展示，不保存明文凭据快照或增加表结构。旧流程留下的active+false隔离无法与人工停调可靠区分，不批量解锁或回填历史。菜单/中英文阶段同步，仅静态核对，未运行构建、测试或真实请求。

2026-10-31 v0.2.19-pk.31：失败重试新建任务时刷新分组目的地，不再校验旧任务fail_group_ids；手动导入重试使用当前保存的workbench及格组，重授权重试保持当前账号组优先策略，reauthorization_retry元数据保留多次重试来源，旧版仅含retry_of_job_id的记录递归追溯原来源（父ID严格递减）以兼容，历史记录不重写。旧任务和凭据快照规则、并发/优先级/出口及模型快照不变。前端每次加载/刷新过滤隐藏的已删除或停用及格ID；提交前重新加载配置/组列表，移除旧ID则停止本次POST并提示用户确认。新增authorization_groups.go集中读取分组，原校验仍锁组、检查类型/隐私准入并返回具体失效ID；不通过忽略无效目标放宽准入。完整保留用户pk.28～30对account_authorization_browser.go的更新，按pk.30字节核对不改浏览器。

2026-10-31 v0.2.19-pk.27：新增account_authorization_retry.go，把proxy_network_failed等前置连接/导航错误接入外层换代理；保留同代理最多3次独立连接，最多尝试5条实际预检通过的不同代理，未通过者不占名额，每次使用独立OAuth会话并在退出删除会话。禁用/删除的候选在连接前复核，池耗尽/总时限有明确阶段和真实计数；保留noReplay及密码/2FA提交后不自动换出口，明确凭据错误/封禁/限流/安全验证不靠轮换重放，授权码交换保留一次原传输重试后终止。新增account_authorization_actions.go：失败或needs_input且尚未保存授权的任务可重试，创建新manual任务，保留原参数和失败记录，使用最新未删除历史而不复活旧任务密文；已保存授权的测智失败不重新登录。巡检立即授权固定账号ID、继承现行测智与原账号分组策略，原代理/并发/优先级不改。历史锁、active-email唯一索引和owner_token继续防重与防迟到写入。授权代理批量删除限制1至200个正整数ID并去重，使用原代理导入锁删除独立pk_authorization_proxies记录；前端支持当前页勾选/全选/确认，不删除原生IP管理代理，进行中连接可自然结束。无新表/迁移。

2026-10-31 v0.2.19-pk.26：工作台增加 account_proxy_mode（direct/managed）和 max_accounts_per_proxy（1至1000）；跟随工作台设置及任务JSON快照保存，缺字段的旧任务仍默认直连。仅新建账号在applyAuthorization事务内按原生proxies.id升序选有效未满代理，以独立70360005事务锁串行分配，持有代理行锁后重新统计所有非删除绑定账号，和CreateAccount.ProxyID一起提交。无可用代理则以明确容量原因终止入库，不改用直连；授权登录代理池、OAuth/浏览器和已有账号出口不受此设置影响。后续测智使用已绑定的原生账号代理，原并发=负载因子、优先级、空组停调与多模型准入保留。

2026-10-31 v0.2.19-pk.20：用户明确替代每次提交追加历史的旧规则。每邮箱复用一条当前凭据，提交只把加密凭据快照写入任务；新邮箱用无秘密pending占位满足原外键，排队/执行中不进入历史。任务终结后归档（失败/需处理和明确封禁保留，授权前取消不归档），同邮箱覆盖当前记录；已有多条历史只读去重展示。编辑和巡检补全立即更新、原邮箱不变，不触发授权；新排队任务固定独立加密快照，旧排队任务在管理员修改前补齐快照。短事务锁、历史版本比较及先结算前次任务防止迟到归档覆盖后续编辑/删除或污染下一任务。归档清除任务中的密码/2FA密文副本，进程中断由既有cron补归档。删除清空该邮箱所有历史密码/2FA及任务快照、取消未完成任务，用无秘密删除标记保留外键，禁止旧版本凭据重新出现；不删除账号OAuth token、任务结果或测智证据。历史操作列保留查看并新增更新/删除，编辑弹窗修改密码和2FA，删除须确认；完整管理员、step-up、no-store及敏感正文不入审计保持，新PUT接口补审计正文排除。历史缺失仍支持补全并等下轮。复用现有表和JSON，无迁移，无真实登录/模型请求或运行验证。

2026-10-31 v0.2.19-pk.19：展示和提交改为单条SQL快照全部启用的雷达模型，模型/推理强度按排序保存在jobs.settings.intelligence_models；旧单模型字段保留兼容，缺少数组的旧排队任务继续使用原单模型快照。每账号最多8个模型并发，各自读取并核对独立账号对象，避免共享模型映射缓存；同轮5分钟请求预算、外层10分钟授权预算和既有账号任务并发保持。单一协调流程检查任务所有权、取消并保存逐模型状态、答案、判定、脱敏错误和耗时；任一完整答错立即保存不及格结论但不停止其他模型，本轮全部终结后才进入既有事务性分组。全部完整回答21才及格；只有技术失败且未发现答错时保持无结论、未分组；一个答错和其他技术失败仍是不及格。取消保留已完成结果，迟到结果只能在同owner_token下补充元数据，不能重开任务或修改账号分组。任务接口仅投影这些安全结果，中英文工作台展示完整名单及逐模型进度。复用已有JSON列，不改表或历史数据，定时重授权原组、准入与账号状态保护不变。新增正式源码account_authorization_intelligence.go；仅gofmt和静态核对，未运行构建、测试或真实请求。

2026-10-31 v0.2.19-pk.18：修复工作台、提交校验和测智失败后转组仍读取旧pk_account_candy_test_config的问题。展示/提交通过同一条SQL读取pk_radar_account_config.target_group_id和pk_radar_account_models中sort_order最靠前的启用模型，模型及推理强度保存在本批任务快照中；关闭定时开关不影响手动关卡。实际不及格转组重新读取当前雷达目标并FOR SHARE锁定至账号分组提交，清空目标时不回退到旧配置；缺失或失效仍保持未分组。沿用一次探测、及格多组、已完成判定留存、准入/账号快照及重授权原组保护，不改数据库结构或配置数据。中英文说明明确首个启用模型；保存设置后的既有saved事件刷新工作台。仅gofmt与静态调用链/SQL/差异核对，未构建或执行测试，不代表线上已验证。

2026-10-31 启动补装专项证据：服务器断网临时容器的11项入口逻辑检查通过（APK/身份切换为测试替身，无真实下载或账号登录）；不是正式制品验收或完整回归。未本机构建/测试，未部署，详见 docs/PK_AUTHORIZATION_GATE_REVIEW.md。

2026-10-31 未发布补充（沿用 PK-036/016/021）：容器启动时默认自动补装缺失的 Chromium/Xvfb（PK_AUTH_BROWSER_BOOTSTRAP=true），浏览器仍不进入主程序、网页更新归档或发行镜像层。网页更新替换二进制并由监督器重启同一容器，不会重建容器；只有更换镜像/删除重建才丢失系统依赖，由新入口自动补装。已有依赖不重复安装，版本/帮助/自定义命令不触发下载；安装最多等待240秒，失败保留网关启动并记录错误。三套镜像及Compose配置同步，应用健康检查宽限300秒，既有十一项制品验收命令保持离线。首次/重建补装需访问Alpine软件源；旧环境需换一次包含入口的新镜像，单独网页更新不能更新启动脚本。此前仅留工作区；现按文首最新要求交接版本分支、main及正式标签，由用户自行打包，不自动部署。

2026-10-31 未发布扩展：工作台提交强制“授权 → 无分组/停调 → 糖果测智 → 分组”。及格分组由工作台多选，不及格目标只取既有糖果配置的 target_group_id，转组时重新读取并锁定；客户端不能覆盖。模型和推理强度在提交时从糖果配置快照。完整答案21及格，其他完整答案不及格；请求失败、不完整回复、取消不判答错。提交及转组都校验分组有效性；绑定前重新核对账号凭据/代理/状态/分组快照、平台、OAuth和隐私准入，并保留原生简单模式及混合渠道检查。先持久化已完成的判定，再事务性替换组、恢复原允许的调度及完成任务；失败保持无组停调，手动禁用不自动启用。任务展示阶段、授权已保存、判定和最终分组；取消/中断不抹除已保存的授权与判定。沿用 jobs.settings 与既有 settings，无表结构变更。定时重授权和旧排队任务保留原分组与账号参数，不自动套用新工作台关卡。

巡检每轮读取最新账号管理并在执行前复核，只同步状态，不调用hi探测或改写accounts。只有当前error且命中配置状态码才进入重授权；旧expired不能让已恢复active的账号继续重授权。403按官方语义区分密码/凭据、2FA、安全验证、限流与明确停用；单独403不是封禁证据。登录接口错误改为被动读取Network响应，已进入错误页面及时结束，保留代理和OAuth校验。CF自动代点/求解不在本轮实现范围。验证事实及后续服务器回归范围见 docs/PK_AUTHORIZATION_GATE_REVIEW.md；未提交发布。

2026-10-31 v0.2.19-pk.16：对照用户提供 wb-2fa，预检和实际授权统一按已安装 Chromium 版本设置 UA、Client Hints、语言和平台，保留浏览器本身的 TLS 实现，不伪报参考客户端的固定版本；不新增 Python/curl_cffi 运行依赖。捕获主页导航错误、HTTP码及 cf-mitigated 验证页，单次已确认失败立即结束；仅对提交账号凭据之前的预检最多重试3个独立浏览器会话，沿用同一代理和总超时，终态保留具体错误，不重放登录或回调。细分连接/隧道、认证、DNS、TLS、HTTP拒绝、人工验证和超时，原始代理凭据不回显。导入后状态明确为尚未预检，保留手动 GET 与授权前预检；不新增调度、数据库列或自动批量请求。不放宽验证码、人工验证、代理强制出口及禁止直连回退规则。仅在用户授权的服务器做匿名首页专项验证，本机不构建或测试；实际结果与限制见 docs/PK_AUTH_PROXY_PREFLIGHT_REVIEW.md。

2026-10-31 v0.2.19-pk.14：账号授权通过 PK_AUTH_BROWSER_PATH 或 PATH 查找外置 Chromium，不再从更新主程序解包。安装入口为 deploy/install-browser.sh，中英文缺失提示同步外置安装与容器重建限制。所有授权并发、代理预检、封禁判定、凭据历史/补全、巡检/重授权 cron 和账号列表读取逻辑保持；不改业务表、历史数据或固定加密密钥。

2026-10-31 v0.2.19-pk.11：正式主程序自带授权浏览器资源，启动时自动准备；配置中的历史镜像默认/usr/bin/chromium不覆盖网页更新带来的新浏览器，其他用户自定义路径继续优先。随包加载器路径在独立暂存目录修正后原子启用，不要求root或另行配置；支持现有NO_SANDBOX显式设置，容器默认沿用开启。准备失败保留网关启动并在日志/工作台说明权限或磁盘排查方向。保留所有凭据历史、补全、封禁、代理预检、授权并发和cron业务语义，不改表、密钥或历史数据。

2026-10-31 v0.2.19-pk.10：授权浏览器随完整部署包的应用镜像提供，镜像默认路径及no-sandbox参数由项目设置，无需部署者另装Chromium。初始化入口校验镜像、生成0600的.env及固定TOTP_ENCRYPTION_KEY/JWT/数据库/Redis/管理员密码，随机Compose项目隔离，默认仅回环监听；拒绝覆盖非空部署目录。已有部署load-only不更改容器、数据或密钥；首次旧镜像升级使用完整应用镜像，网页二进制更新不能安装系统依赖。中英文缺失提示明确完整部署包路径。继承pk.9授权、封禁、巡检、凭据补全及空号池行为，不改业务表和历史数据。

**账号授权、授权巡检与凭据历史。** 状态：有效。引入版本：v0.2.19-pk.9。

- 2026-10-31 追加：参考账号API的run-account页面判定与credits-protocol的account_deactivated错误码，首次/后续授权识别官方明确的停用、删除和封禁。任务进度使用独立banned终态并停止入库；凭据历史按邮箱显示「账号已封禁」及说明，支持筛选。已确认封禁不进入自动重试；管理员处理后可手动提交，后续真正授权成功才解除该邮箱的封禁显示。普通401/403、代理错误和人工挑战不单独作为封禁证据。

- 账号授权菜单紧接雷达，工作台/授权代理/凭据历史三页签；雷达增加授权巡检。前端和API只允许完整管理员，受限管理Key不可进入；明文历史查看使用既有step-up验证。
- GenerateAuthURL生成独立随机state、PKCE verifier和session ID，SessionStore读写锁隔离，可并发生成，不需要CPA。每批授权并发1～50，新账号并发1～1000、优先级0～1000、及格入库分组可多选，不及格目标沿用糖果测试设置，负载因子与账号并发一致。
- 自动巡检限可密码登录的真实OpenAI OAuth账号；影子、合成测试、Agent Identity/PAT、API Key等在列表显示不适用，不转换其账号类型。工作台新授权先无组停调并通过测智分流，复用原生准入/分组校验；定时重授权更新原ID，保留分组、并发、负载、优先级、模型代理及二开设置。成功后恢复系统错误停调；手动inactive状态保持，期间状态变化拒绝覆盖。
- 单个服务端cron分钟调度器按持久化计划执行每3分钟巡检、每10分钟选择重授权，两开关默认关闭，并发可配。多实例会话锁防重复协调，独立消费锁让长登录不阻塞巡检；同类任务不叠加，逾期补跑。先完成本轮状态同步再选择重授权；配置保存不立即执行，页面刷新只读取状态。
- 巡检每轮从最新accounts取ID，执行前重读账号；重授权每轮关联当前账号与最新历史，删除账号不复活。巡检仅同步最新账号状态与错误码，不发送额外请求；工作台测智复用原生糖果测试的凭据/模型映射/TLS/账号代理，不创建用户模型账单。普通连接测试及原有糖果调度保持。
- 重授权HTTP码可配置，默认[401]，允许不重复的400～599。仅账号管理当前error且错误信息命中配置状态码才进入重授权，不沿用历史expired兜底。巡检不再主动探测；配置、身份与凭据通过事务复核，旧结果不覆盖新账号状态。
- 最新历史缺少邮箱/密码/2FA显示「账号凭据不够」并跳过，提供「补全凭证」。已有邮箱固定，缺失邮箱可补填。补全按邮箱更新当前加密凭据且不排队、不唤醒授权，显示「已补齐，等待下次重授权任务」。cron任务固定独立加密凭据快照，后续补全不能替换已排队任务。
- 凭据历史按邮箱保存当前内容与更新来源，提交任务结束后归档、补全和编辑即时保存；密码/2FA与代理URL加密，固定TOTP_ENCRYPTION_KEY是保存前置，防止临时密钥重启丢失。列表仅返回安全元数据；敏感请求体从审计正文省略，但操作仍审计。实际凭据、令牌、回调和原始上游错误不写任务日志。
- 代理池最多200条，支持认证SOCKS5/SOCKS5H/HTTP/HTTPS及行末反斜杠。每次独立Chromium会话先通过选中出口GET chatgpt.com并检查页面，失败尝试池内其他启用代理，全失败不直连。令牌交换沿用该出口；工作台测智用账号原代理。官方人工/邮箱/短信验证转人工，不绕过。
- 校验回调state/地址、邮箱和原ChatGPT工作区，提交前复核任务所有权、取消、配置与账号快照。新凭据保存后失效token缓存并更新调度快照/outbox，缓存失败显示待刷新并由cron重试。
- 迁移253只新增config/history/proxies/jobs/state五张独立表；pk.20按用户要求改为按邮箱更新凭据，旧表结构不改。Chromium按pk.14规则外置，网页二进制更新不安装浏览器；备份恢复须保留数据库及原加密密钥。
- 入口：service/account_authorization_*.go、handler/account_authorization_handler.go、迁移253、admin路由与Wire；repository/account_repo.go的ListAllWithFilters沿用已有事务作用域，入库前同邮箱查询不另占连接；前端AccountAuthorizationView、AuthorizationSchedule/Records、accountAuthorization API、中英文案和雷达入口。
- 本轮仅源码、格式化、差异、权限/事务/生命周期和文案静态核对。未运行本地/服务器构建或测试、真实模型/登录请求、浏览器视觉验收，未新建测试文件；既有最小Cleanup回归仅同步参数。Jenkins实际构建/制品验收由用户触发，不把静态检查说成登录已实测。详见 docs/PK_ACCOUNT_AUTHORIZATION_PLAN.md。

## PK-037

2026-11-01 v0.2.19-pk.37：ensureCycle创建轮次时，started_at/window_start/window_end及空号池CASE结束时间显式使用timestamptz，账号数参数显式integer，消除复用$3被推断为不同类型导致整个事务回滚的错误。失败日志仅附SQLSTATE与固定错误类别，不输出SQL/参数/账号数据。线上只读证据：配置启用、周期30分钟/窗口24小时、7账号、轮次/任务/快照均0，next_cycle_at为2026-10-31 14:46:16.772597+08；2026-11-01 12:47～12:51应用每分钟告警，PostgreSQL同分钟报inconsistent types deduced for parameter $3，非cron丢失或锁占用。更新后沿用下一个分钟cron处理已到期配置，从当前时刻建立新轮次，不补建停滞期间所有历史轮次。每轮最新账号、均分最多5批、错峰串行及每次100项上限、仅上游失败口径保持；无迁移或历史数据改写，未执行动态验证。

2026-10-31 v0.2.19-pk.26：新增被动健康度统计。每轮以同一数据库快照读取最新账号管理总数及账号ID，ntile(min(5,N))平均分批；每批计划时点按周期/实际批数错开，只消费最早未完成批，批内逐号查询，不并发扫库。100号为5×20、300号为5×60、400号为5×80、301号为61/60/60/60/60、1号为1批。每分钟cron最多读取100个队列项、45秒预算、每号10秒超时；大批续跑仍属于原5批，慢批不与下一批/下一轮重叠。配置修订变更取消旧未完成队列，迟到结果不覆盖新设置；重启从未完成项续跑，删除账号跳过，零账号结束等待下轮。

健康度=成功请求/(成功请求+终态上游失败)×100%，默认近24小时；失败归因、免费成功、部分计费失败排除和APIKey/request_id去重沿用pk.25。无有效请求为null，不当100分。窗口末端统一为本轮开始时间，底部只显示成功记录first_response_ms的MIN/MAX，不使用P50/P95。失败保持旧快照并记录失败原因。列表include_health_stats只读取持久化快照，缓存键隔离；仅健康度可skip_today_stats，不触发原始日志聚合。原最近完成首字/连续失败仍按pk.25的独立实时批量快照刷新，首字三位秒数与失败次数加入绿/红/灰背景。

迁移254仅新建pk_account_health_config/cycles/jobs/snapshots四张独立表及索引；原usage_logs、ops_error_logs和accounts仅作读取，不修改已有表结构或历史数据。快照按账号保存，进度记录保留约7天并以每次cron最多1000条的方式渐进清理。新入口为account_health_service.go、account_health_stats.go、account_health_handler.go、AccountHealthCell.vue、AccountHealthObservation.vue及accountHealth API/类型/翻译。仅gofmt、差异和调用链静态核对，未运行构建/测试；边界核对见本轮文档。

**账号列表请求统计与分批健康度快照。** 引入：`v0.2.19-pk.25`；健康度扩展：`v0.2.19-pk.26`。

- 账号列表新增默认显示、可隐藏的两列，含完成/最近失败时间，失败次数大于零标红。随分页、筛选、手动与自动刷新更新，即使账号列表 ETag 未变化也更新统计。
- 最新完成请求按 usage_logs.created_at DESC、id DESC 取一条成功记录，读取其 first_response_ms，不取平均、不跳过该条缺失的时间去取旧值。失败占位、安全策略拦截与有终态错误的部分计费流不当作成功；免费但有真实用量或首字的请求可以清零。
- 连续失败只统计最近成功以后 ops_error_logs 中 account_id 对应、error_owner=provider、phase 为 upstream/network/account_auth、HTTP>=400（排除499）、非业务限制/非count_tokens的请求。按账号、API Key和请求ID去重，回退client_request_id或独立错误行ID；支持 usage 的 local:/client: 计费关联。平台余额不足/本地配额/路由错误既不加数也不清零，恢复成功的2xx过程记录不计作最终失败。
- 沿用现有管理员 today-stats/batch 接口，显式 include_request_stats 才附带独立 request_stats；每页一次批量SQL、复用账号/完成时间索引与30秒快照缓存，缓存键区分新旧请求。失败返回不可用标记，不伪造0，不泄露数据库错误。
- pk.25两列不添加表或迁移；pk.26健康度新增独立统计表。不改原历史数据、失败归因写入、业务调度/配额/计费或账号状态。统计仅代表仍保留且实际写入的日志；关闭错误日志或历史清理后不能据此还原未保存事件。
- 入口：`backend/internal/repository/account_request_stats.go`、`service/account_usage_service.go`、`handler/admin/account_handler.go`、`pkg/usagestats/usage_log_types.go`；前端 `AccountsView.vue`、`AccountRequestStatsCell.vue`、账号API/类型/中英文案。
- 验证：扩展既有 `usage_log_repo_stats_integration_test.go` 的跨日、重复、免费成功、缺失/零耗时、平台错误排除及部分计费失败关联用例；本轮仅静态审查，未运行构建、测试或线上请求。

## PK-038

**雷达账号数量邮件提醒。** 状态：有效。引入：`v0.2.19-pk.38`。依据：用户要求可配置执行周期、最低账号数和收件人，按多选条件统计并复用系统发件设置。

- 雷达“邮件配置”默认关闭，30分钟周期、阈值1，三个条件全选。周期1～10080分钟、阈值1～1000000、最多20个合法去重邮箱；至少一个条件，启用时必须具备收件人及系统SMTP配置。不另存SMTP密码。
- 勾选条件按AND：额度**使用率<100%**、最新有效测智合格、当前不是401。未勾选条件不限制。数量严格小于阈值才发信，等于阈值不发；持续不足按配置周期继续提醒。
- 每轮重读当前未删除账号和原生已保存状态，单次快照避免跨页混合，不固定号池名单或修改业务账号。额度使用账号自己的Codex快照及原生窗口重置规则，所有已知有效窗口均低于100%才满足；无可判断额度列为Unknown且不满足该条件，不用本地消费金额推算、不额外发起上游额度或模型请求。Spark影子按自己的额度快照，不套父账号全局额度。
- 合格直接复用雷达最新实际判定及当前配置代次，包含符合既有条件的授权测智结果；不以分组或历史曾经合格替代。当前error里的401及未到期临时停调401被排除，恢复后的历史401不排除。不改变授权巡检的可配错误码策略。
- 服务端每分钟cron读取持久next_check_at，启用/保存后下一分钟开始；单进程互斥和跨实例数据库锁防同时执行，持久化提前推进本周期时间，失败或重启不每分钟重发/追赶全部历史周期。配置revision防并发覆盖、迟到写；发送前复核启用与版本，停服不开始新的发送。SMTP成功返回仅代表提交给邮件服务器，不保证收件箱最终投递，崩溃临界点不承诺严格exactly-once。
- 新迁移255仅新增独立邮件配置/最近状态表，不改既有账号、测智、用量、账单或统计表及历史。查询失败显示失败，不把未知数量作为0发送；部分收件人失败明确展示，错误信息脱敏。邮件只含聚合数据、条件及检查时间，动态HTML转义，不带账号凭据。
- 页面显示匹配数、最新账号总数、未知额度数、最近检查、下一次、最近发信及结果；刷新展示状态不覆盖未保存表单，客户端不承担任务调度。接口仅完整权限管理员，拒绝scoped账号管理key，配置保存审计，响应no-store。
- 入口：`backend/internal/service/radar_email_alert*.go`、`backend/internal/handler/radar_email_alert_handler.go`、`backend/migrations/255_radar_email_alert.sql`、雷达页面及邮件组件/API/中英文案；生命周期沿用PK-019启动门和原清理链，SMTP沿用`EmailService`读取系统设置。
- 验证：本轮只做格式化、SQL/权限/接口/生命周期和差异静态审查；未运行构建、测试、typecheck、真实邮件或线上操作。没有生成测试文件。详见`docs/PK_RADAR_EMAIL_PK38_REVIEW.md`，源码交接不代表功能已部署或邮箱实发通过。

## 验证入口与现有边界

### 已有回归入口

| 范围 | 已有测试/流程 | 使用边界 |
|---|---|---|
| PK-001～006 | `backend/internal/service/openai_gateway_usage_integrity_test.go` 中 `TestPKCacheConversion*` | 覆盖设置、公式及 JSON/SSE 代表场景；不是所有协议端到端验证 |
| PK-007 | `setting_service_update_test.go`、`setting_handler_partial_payload_test.go`、`SettingsView.spec.ts`、`api_contract_test.go` | 包含配置链、局部保存、错误与前端换算 |
| PK-008～012 | `openai_compact_sse_keepalive_test.go`、handler context、DTO、usage repo、channel monitor、Server-Timing 测试 | unit/race 与数据库 integration 分开；候选须检查对应 commit/tree 及真实执行环境的源码报告 |
| PK-010 UI | `frontend/src/components/admin/usage/__tests__/UsageTable.spec.ts` | 币安/每日消费接入时纳入 `FRONTEND_CRITICAL_VITEST`，与 `UsageView.spec.ts` 一同验证嵌入模式和原上游首字权限 |
| PK-013～015 | `update_service_test.go`、`github_release_service_test.go` | 使用更新器自身 PK 修订、授权、路径及校验测试 |
| PK-016～017 | `Jenkinsfile`、`scripts/test-server.py`、`scripts/test-local.py`、现有 `deploy/tests/` 和制品验收流程 | 发布构建不等于生产已部署；测试清单/矩阵改变时同步核查 |
| PK-018～019 | `concurrency_service_test.go`、`wire_test.go`、`batch_image_cleanup_test.go` | 需要门控/停止并发行为证据，不能仅搜索到 env 键即认为有效 |
| PK-020～021 | 维护流程、固定 commit 的隔离验收和静态文档核对 | 提交前源码测试与 Jenkins 发行构建分开；不把历史线上/探测报告当作本次新代码验证 |
| PK-031 | `AppSidebar.spec.ts`、`UserDashboardStats.spec.ts`、管理员 `DashboardView.spec.ts` 与公共选择器既有回归 | 核对实际 `frontend` 执行列表；菜单与交互回归不代替浅色/深色、桌面/移动端的浏览器视觉验收 |
| PK-033 | 原有设置服务/局部更新/公开注入和API契约回归 | pk.14仅扩展既有用例与静态核对，未执行；不等于图片或页面动态验收通过 |

可选完整源码验证的 `backend-integration` 执行数据库 integration 的 `TestPKUsageFirstResponsePersistence`，只有实际通过才能生成通过报告；`backend-race` 保留旧候选/Release 选择器及 PostgreSQL 15 专项。Jenkins 前端发行构建不等于执行了 `frontend` 源码套件。选择运行验证时，应检查真实执行列表并记录服务器隔离环境或本机 Linux/WSL；未执行项明确写未执行，不将服务器结果写成本机结果。2026-10-21 起这些源码检查不再是提交、合入或发布前置，功能保护核对和制品运行时验收仍按 PK-017 执行。

### 已知行为边界

1. Codex Session 重新导入会显式发送默认 `false/1` 覆盖已有缓存转换设置；普通编辑的缺字段保留规则挡不住显式值。批量编辑页面尚无转换 UI，批量 API 已支持。转换按实际选中账号读取配置，没有专门继承影子账号父转换配置的机制。
2. 超大倍率可导致 int32 溢出；部分非流式 Responses/embeddings 路径的 `nil,error` 返回可能丢失已采集的用量证据，不能标为已全面修复。
3. WebSocket 二进制帧保持既有直通范围。部分 OAuth 非 direct 图片的合成出口未接入账号转换，可能原样透传 `response.tool_usage.image_gen` 内的 usage；API Key 图片路径已接入。JSON 扫描固定 usage envelope，未知 provider 嵌套不会自动转换。OAuth 不在本次历史号池真实探测范围内。
4. 提前 HTTP 200 后，错误以 SSE 终止事件表达，晚到动态响应头不能补发；SDK/代理可能忽略或缓冲心跳。真实下游收包须在最终网络链路测量。
5. 原上游“首字”保留已有计时语义，部分协议是第一条事件，并非第一段文字；旧/未测量记录的首包为 null。
6. 网页手动 Docker 回退示例仍使用示例账号/`sub2api` 服务名。实际 Compose 项目、服务名、停机授权与等待方式须按维护文档核实，不能照抄示例宣称无中断回退。
7. 更新器的两次 rename 不是一笔文件系统事务；只保留一份 backup，本地回退会消耗它，也没有新程序启动失败后自动健康回滚。必须验证实际恢复结果。
8. 早发 writer 的自动 JSON 错误转换覆盖单次完整写入；新增分块 JSON 错误输出需要单独审查。
9. 尚无缓存转换专用前端测试、所有协议出口、decimal `100 × 0.29`、溢出结算和转换后按次档位的完整专项覆盖；VersionBadge 也未有 PK 专项测试。已有代表用例不得描述为全面覆盖。

以上边界应在相关变更中逐项处理，不借本次文档登记擅自改变业务。获得对应修复授权并完成验证后，更新原条目与边界记录。

## 新增条目模板

新增独立能力使用下一个未用 ID，当前下一个为 `PK-039`；多项变更并行新增时在合并前消除 ID 冲突，已有 ID 不重用。同步更新索引、有效条目范围和此处的下一个 ID。

```markdown
## PK-NNN

**功能名称。** 状态：有效 / 已授权替代 / 已授权移除。
引入/最近变更：可核对的提交、版本或本次变更说明。

- 用途和必须保持的行为：
- 默认值、配置/存储字段、计算规则、协议和权限范围（按实际填写）：
- 代码与 UI 入口（包含所有调用路径）：
- 验证方式、已有用例及是否纳入 CI：
- 已知边界、历史数据兼容和变更依据：
```

相同功能扩展时补原条目，不能仅增加标题却漏掉后台、存储、协议或验证。未写入清单的新增二开不算完成。
