# pk.57 网页更新与启动守卫联动

## 2026-11-08 最终发行及启动许可核对

本节替代下文同版待验收的阶段状态。正式Release已发布并核对；本次只在main补录，版本分支、正式标签、正式源码tree及封存制品保持不变。

正式commit `211da2227f502a118d2df8751cfa1dd5522f622f`，tree `f91a0b8db7fc18845cc2fbaf320938421559989d`，package_id `1794073843-211da2227f50`；二进制SHA256 `29347934effe0b2c0a2e510dc04f3de59f2851ce49d798ee7b25a9678db3654e`。镜像ID `sha256:ddda4cf9787459018b40a95237d6273218ba13c5be7f29129adff2b82e0fa20c`。私库 [v0.2.19-pk.57](https://github.com/yb1320057104/sub2api/releases/tag/v0.2.19-pk.57) 为发布核对时的latest，9项资产与本地封存名称／长度／SHA256逐项一致。

前后端最终通过的构建均绑定本轮正式源；前端源码与pk.56相同，但本轮重新执行构建，不将旧版测试日志重标为pk.57。Vite实际1219模块，耗时43.86秒，产物文件198项；i18n 3项及类型检查通过。

Windows Go 3包：顶层通过56、顶层跳过1；父子测试合计pass事件123、skip事件6，无失败。跳过原样列于下表，不冒称所有平台语义通过。Linux另执行交叉编译测试程序，33个顶层通过、无跳过，临时目录清理通过；该执行通过WSL wrapper完成，不能伪称直接运行go test，也不与Windows重复场景累加。

Python宿主事务逻辑、Linux真实文件语义、publisher门禁分别以本轮原始unittest日志统计，全部无跳过；publisher外部I/O为模拟。Windows为已声明源码冻结下的运行后哈希快照，不冒称预先采集；publisher有前后精确哈希，Linux Go有编译前后源码绑定。Git切换产生的五份Python源码CRLF换行已恢复为实际测试原字节，Git blob语义未改变；修复记录保存在本轮证据目录。

后端正式构建并非首轮全部成功。首轮Go构建生成的二进制带有 `vcs.modified=true`，干净VCS门禁拒绝继续封装；原因是五份新增Python源码checkout autocrlf换行及陈旧Git索引状态。恢复实际测试原字节并用git add刷新索引后，staged diff为空、正式HEAD不变，未修改语义源码或移动标签。首轮二进制SHA256 `9082daac2754bdc0e8f46041e9afa9ba33398e031b962de889150cc14dcde38c` 及embed输入／overlay保存在 `.runtime/pk57-build-attempt1`。第二次准备因 `existing_embed_evidence_requires_preservation` 提前拒绝，未执行Go编译；相应准备证据及失败记录已移存同目录。之后重建的最终 `pk57-local-build.json`、二进制和build-info才用于制品、发布与本节通过结论；最终build-info确认 `vcs.modified=false`。原失败证据未改写为通过，也不把第二次准备拒绝算作一次已执行编译。

隔离制品运行基线为实际pk.56正式commit `547aa51fd9553321ce0ce940296904941d764afc`。324份迁移和校验和完全保留、无新增；金融函数由本版二进制安装后在隔离数据库校验，审计计数断言执行正式源SQL，不伪称编译后的repository Go方法已被集成调用。保留pk54 SQL夹具标记只是保留原字节，本节通过结果全部来自本轮新执行。隔离daemon、容器及临时资源清理通过。

生产启动授权在公开Release前已prepare，最后check及发布后只读验证均绑定本次manifest／commit／tree／package／binary。最终宿主与容器守卫SHA256一致为 `9759afa16d38e72d390cc55bc920e83e39e290941c9e0904c0cbd16370e4a86a`；安装receipt SHA256 `b3ee8932837dc1ec35912ca034f7ebca601e1c1c652309c3bc5940a213f0b9e5`，journal SHA256 `033d6823e362bf5759d196ca31c2201ce7c4763519f0acf460d68eb65d265a8e`，epoch证据SHA256 `4e6f18285d2877db48ad3710fa60348a4bec6eeaa9645439752bc46db98c62b3`，发布后授权回执规范JSON SHA256 `637ddd6f8a91cebcdcd2b47c66a585fa023c71e7d29497a6abd542cd9746a9c1`。这些回执证明许可与实例绑定、核对时healthy且epoch未变；发布流程未替换应用二进制、未发送重启、未授HA仲裁权。生产外部守卫及回执确有变更，不能概括为生产未变化；应用的后续手动升级仍与发布区分。

本轮local入口未发布GHCR，不执行真实模型／账号授权／币安请求、不运行全套源码回归。HA监测failed及guardian／armed／fence inactive的既有记录不因启动许可而失效；本次没有HA恢复结论，不能宣称集群恢复。固定containerID、两条受管最终执行路径、守卫挂载和活跃HA拒绝边界见下文。

生产依赖审计实际为0 critical／2 high／12 moderate／2 low；原始审计退出1、既有依赖例外门禁退出0，未宣称漏洞清零。

| 本轮检查 | 实际通过数量 |
| --- | --- |
| Linux文件语义 | 6 |
| publisher门禁 | 11 |
| Python宿主事务 | 19 |
| 同制品运行时 | 11 |
| 升级回退 | 7 |
| 多代理API | 7 |
| IP分组API | 7 |
| 模型目录API | 7 |
| 金融迁移函数 | 8 |
| 审计源SQL | 6 |

| Windows保留的跳过测试 |
| --- |
| `TestUpdateServiceRemovePreviousBackupRejectsNonExecutableAndPreservesIt` |
| `TestUpdateServiceRollbackRejectsUnsafeBackupsWithoutChangingInstall/non-executable` |
| `TestUpdateServiceStartupGuardRejectsSymlinkPaths/guard` |
| `TestUpdateServiceStartupGuardRejectsSymlinkPaths/guard_ancestor` |
| `TestUpdateServiceStartupGuardRejectsSymlinkPaths/target` |
| `TestUpdateServiceStartupGuardRejectsSymlinkPaths/target_ancestor` |

| Release资产 | 字节数 | SHA256 |
| --- | --- | --- |
| `artifact-validation.json` | 3797 | `3e04b22d7fd9e1d5370d5252a3460b4d4be1f783ced57f86e076b8d5ff7381b9` |
| `checksums.txt` | 106 | `9fbf43a966c7662060517a219f276b3188dd1e7d8acd5e6d2932648fc7f2e77c` |
| `isolated-smoke.json` | 84072 | `b4ed4865bdccd6d63bcc670c7a0cacd5cce0a652783f61a30feeea70d4be428e` |
| `local-build.json` | 172233 | `6ca3734b9d1a02e63d98ab0138b77111fef6d94220bc81b576a016e419e0d61d` |
| `manifest.json` | 1282 | `36a145a8775b4fc5579494936982aa1893d3d8ea3e43b2147477e76d9c974f05` |
| `migrations-validation.json` | 43893 | `dbd828f07440082098f54f3963266473e4d78cf7c2ae0d15669a0417cf69d0d3` |
| `review.json` | 1309 | `c621312a54531a4c96b9e9eb7ad2720b58e19d4772c077489780de9e5e40df3a` |
| `sub2api_0.2.19-pk.57_linux_amd64.docker.tar.gz` | 50994877 | `dc3711f5a6c5fd97c2bdf967a7da8638cd5da13bb68f6ca92348d3d9f3fec93c` |
| `sub2api_0.2.19-pk.57_linux_amd64.tar.gz` | 40548494 | `293a272828faa8a68c9f5d646e44690dbc5be6126832c42711810a7ead38158f` |

| 本轮主要证据 | SHA256 |
| --- | --- |
| `.runtime/pk57-publication.json` | `636e66223663782ac3ce190ac87e4142f97b33b6a19f611c5a44d05d4bb7b88f` |
| `.runtime/pk57-local-build.json` | `6ca3734b9d1a02e63d98ab0138b77111fef6d94220bc81b576a016e419e0d61d` |
| `.runtime/pk57-frontend-build.json` | `b20a964f9ad210fd91e630b35af2295453f8b19ac19627d766c5706bd88ee592` |
| `.runtime/pk57-targeted-checks.json` | `750524df579a9506486771e14ba2468f1a22512b2f18fc2897a7877c8d2f316f` |
| `.runtime/pk57-guard-evidence/linux-go-supplementary.json` | `09779feab9fe205ab9ed4045b636e30b96dec32f363635e9b7c7731738a78da5` |
| `.runtime/pk57-release/review.json` | `c621312a54531a4c96b9e9eb7ad2720b58e19d4772c077489780de9e5e40df3a` |
| `.runtime/pk57-release/isolated-smoke.json` | `b4ed4865bdccd6d63bcc670c7a0cacd5cce0a652783f61a30feeea70d4be428e` |
| `.runtime/pk57-release/migrations-validation.json` | `dbd828f07440082098f54f3963266473e4d78cf7c2ae0d15669a0417cf69d0d3` |
| `.runtime/pk57-build-attempt1/failure.json` | `3ddac297c8b6e70f139de8607e5054d3fbed2f871b457721496c8e3b09ca60e5` |
| `.runtime/pk57-build-attempt1/attempt2-prepare-failure.json` | `13ca2442f9ab2142ff574086d598b8eae17ede2b082c45786205c379cbb27313` |

2026-11-08 阶段记录。目标版本为 `v0.2.19-pk.57`，基于私库 main `8339df3e15c73ad0a0ab2f713f416719a9070cc7`。正式提交、制品、发布与生产许可准备结果均须在实际完成后补录；本记录不表示已发布或已部署 pk.57。

当前已实现发布侧许可准备与应用侧只读预检。Python 宿主逻辑19项、Linux文件语义6项的阶段运行通过，最终日志哈希与最终源码绑定尚待完成。Windows Go三个包56个顶层测试通过、1个顶层跳过，含子项总计6个skip事件（执行位2、符号链接4），无失败；Linux专项正在补验，不能以Windows的平台跳过代替。正式前后端构建、同一制品隔离验收、升级回退、私库 Release 和附件核对尚待实际执行。当前没有本版完整发行成功结论。

## 故障依据与范围

用户网页安装 pk.56 后，应用文件哈希已是正式发行版本，但外部启动守卫仍只有旧许可。日志明确出现 `sub2api: startup guard rejected executable`，容器退出126并反复重启，因而反代返回502。应用尚未进入业务启动，此证据不能解释为内容审计、扣费或数据库迁移故障。

此前已经按故障恢复授权追加 pk.56 正式哈希并恢复原容器，实际进程、健康状态及五个本地/公网探针核对通过。该历史恢复记录保留在 [pk.56 核对记录](PK_LONG_CONTEXT_PK56_REVIEW.md)，不计作本版测试或本版部署。

本版沿用下列既有登记，不新增功能ID：

| 登记 | 原有含义 | 本版扩展 |
| --- | --- | --- |
| PK-016 | Docker网页更新持久化；数据卷程序、种子哈希与镜像回退边界 | 更新、回退、立即重启前核对目标程序启动许可，并保护重启等待窗口 |
| PK-017 | 私有发行与部署入口；正式标签、同制品验收和私库发布 | 可信publisher绑定正式制品准备启动许可，并在公开Release前最后复核 |
| PK-021 | 二开登记与项目约束；唯一清单、增量登记、逐项审查 | 真实阶段结果、许可操作与应用部署分别登记，保留历史合同和证据 |

前端、依赖、324份迁移及其校验和保持。首字计时、审计处罚、费用回执、账务、代理分配与现有业务数据不在本版修改范围。

## 发布侧：先准备许可，最后复核，再公开

`deploy/local-release.py` 与 `deploy/jenkins/control.py` 均接入 `deploy/jenkins/startup_guard_publication.py`。对本仓库 pk.57 起的新版本，新门禁与原正式 tag/main、manifest、同一二进制验收、私库身份及附件摘要门禁同时生效。

1. `check/plan` 只读核对运维端是否就绪，不修改生产守卫。
2. publisher 在原有制品及正式来源校验完成后调用 `prepare`。请求绑定仓库、版本、commit、tree、package_id、binary SHA256 和 manifest SHA256；未准备成功不得继续公开发行。
3. 可信运维钩子通过既有受信SSH通道调用独立宿主工具 `deploy/startup-guard-release.py`。该工具读取独立安装、root私有的部署策略，核对运行实例身份、健康状态、进程epoch、Compose、宿主/容器守卫、安装回执、日志及原有HA保护状态。
4. `prepare` 在复查、互斥与备份后，只向已核对的守卫追加本次候选哈希，同步宿主和容器守卫以及相互绑定的安装回执/状态，并保存本次发行授权回执。失败按已执行步骤恢复并保留故障证据，不假报许可成功。
5. 上传附件、验证摘要并再次核对正式来源后，publisher 执行 `check`，确认本次制品的授权回执、健康状态及守卫一致性仍成立，才把草稿改为正式Release。

本轮使用local发布入口，不推送GHCR。Jenkins入口的顺序另有边界：`prepare`成功后会先推送固定版本GHCR镜像，再上传Release资产并做最终`check`。最终检查失败会阻止公开Release和推送`latest`，但固定版本GHCR镜像可能已经存在；须保留并核对该事实，不能宣称发布失败就没有任何外部变更，不能自动删除或重推来掩盖中断。

可信钩子来自发布环境配置，不能由下载包、网页请求或应用设置指定。固定参数以结构化请求传递，错误只返回固定安全原因，不输出SSH凭据或远端诊断正文。应用不获得root、SSH、Docker socket或修改守卫的能力。

准备许可本身会修改生产外部守卫及回执，不能再笼统记录为“生产未变化”。它不替换当前应用二进制，不发送重启信号，不启停服务，也不授予HA仲裁权。发布核对报告应分别记录 `application_binary_changed` 与 `production_startup_guard_changed`，并绑定实际许可回执。

## 受管部署边界与可信钩子配置

受管守卫实际只允许最终执行路径 `/app/sub2api` 和 `/app/data/bin/sub2api`。宿主工具强制核对当前进程对应这两个路径之一，并将部署策略、安装回执和发行许可绑定到已审核实例的固定`containerID`，不能拿其他路径或重建后的新容器自动复用旧授权。应用在临时文件上做候选哈希预检，不会因此扩展最终启动路径。

容器重建导致ID变化、守卫文件被直接挂载到容器或HA处于活跃/armed状态时，本轮宿主工具会拒绝许可。运维人员须先协调对应部署身份、安装方式和HA状态，重新审查策略及回执，不能通过放宽校验或关闭守卫继续。本版覆盖已核对的受管实例，不能宣称所有部署会自动恢复，也不能把HA拒绝解释为可以擅自停用仲裁。

启用前，运维人员须把经过审查的 `deploy/startup-guard-release.py` 安装为应用不可写的宿主工具；策略放在应用目录之外的独立root私有目录，目录权限0700、策略文件0600，并绑定实际部署身份、已审核守卫结构及现有回执。SSH主机别名、密钥和known_hosts沿用可信运维环境，禁止在Release或应用配置中分发。

下面仅示例发布端PowerShell的环境配置，尖括号内容和示例绝对路径必须替换为经核对的部署参数。SSH别名必须使用已授权的root运维身份；第一个argv元素必须是发布主机上SSH程序的绝对路径。Jenkins/Linux发布环境可用相同JSON结构，将第一项换为实际的SSH绝对路径。

```powershell
$pkGuardHookArgv = @(
    'C:\Windows\System32\OpenSSH\ssh.exe',
    '-T',
    '-o', 'BatchMode=yes',
    '-o', 'StrictHostKeyChecking=yes',
    '<已核对的SSH主机别名>',
    'python3', '-B',
    '/absolute/root-owned/startup-guard-release.py',
    '--config', '/absolute/root-private/startup-guard-policy.json'
)
$env:PK_STARTUP_GUARD_HOOK_ARGV = ConvertTo-Json -InputObject $pkGuardHookArgv -Compress
```

该argv直接由publisher执行，发行身份JSON通过stdin送到宿主工具，stdout只允许返回一份JSON回执；不要加`ssh -n`截断stdin，也不要在stdout打印欢迎信息或诊断。发布端先执行原有`check`入口验证`plan`，随后正常`publish`自动调用`prepare`和最终`check`；环境变量缺失、身份不匹配、SSH失败或回执不合格均应中止。不得用固定返回成功的脚本替代真实钩子。

截至本阶段，当前主机已安装独立root私有helper及策略配置，活动守卫、应用程序和启动epoch未改。此安装只为后续受控许可准备提供工具，尚不等于候选制品已获许可；具体主机、secret路径及凭据不入库。

## 应用侧：只读预检及操作互斥

`update_service.go` 与 `update_startup_guard*.go` 对更新、指定版本回退、本地备份回退和立即重启使用同一进程内操作互斥，并保留管理员入口原有系统操作锁。

- 更新包先完成原来源、摘要和解包校验，再只读检查候选二进制及受信守卫。没有取得候选许可时，在删除旧备份或重命名当前程序前返回错误；本地备份回退同样先核对备份程序许可。
- 应用只识别已审查的最终执行守卫结构及明确SHA256允许列表，不执行shell，不把任意脚本分支当成授权。Linux读文件拒绝符号链接，核对普通文件、root属主/权限、祖先目录和读取期间的身份稳定性。
- 重启先取得数据库操作锁和服务层reservation，核对实际安装路径及其哈希，再返回已接受响应。延迟回调仍持有操作锁，实际发信号前复查目标及守卫；目标变化或许可无效时取消重启并记录固定失败原因。
- reservation执行只调用同步信号入口，且最多一次。SIGTERM成功后继续保留进程内重启pending状态直到退出，避免优雅关闭窗口再接受程序替换。失败/取消释放reservation，不伪造重启成功。客户端断开不会取消已接受的有限时间重启准备流程。
- 固定错误码包括 `STARTUP_GUARD_INVALID`、`STARTUP_GUARD_NOT_APPROVED`、`RESTART_TARGET_CHANGED`；并发操作沿用现有busy错误。HTTP成功响应表示已接受重启安排，实际信号前的失败通过日志记录，不能冒称服务已完成重启。

未安装受管守卫的普通部署保留原兼容路径。显式要求守卫，或本进程已经观察到受管守卫后，守卫缺失、损坏或变成不可验证结构应拒绝操作；不得通过静默降级绕过既有保护。应用预检不能代替宿主的最终执行守卫，外部许可仍由可信运维端维护。

## 历史版本与生产边界

pk.56及更早封存Release保留原发布合同、正式标签、二进制、附件和校验方式，不回写或重新标记旧结果。新发布门禁不把旧版本自动重签为pk.57；应用回退也不能跳过所在主机的真实启动许可。

本轮截至本记录未替换或重启生产应用，未发布GHCR，未执行真实模型、账号授权或币安请求，未运行全套源码回归。正式发布仍按本机新构建、定向检查及服务器独立同制品验收推进；不能将隔离环境的启动/回退写作生产已升级。

当前生产基线为已恢复的 pk.56：正式提交 `547aa51fd9553321ce0ce940296904941d764afc`，二进制SHA256 `fa55b8fa82a1853b28cb14cdc22d73d9457e61fc602b1a31db15d7f40ac471ab`。正式验收前仍需重新只读确认实际运行基线，不能只凭此文档或旧镜像标签。

既有HA监测仍为failed，guardian、armed、fence仍为inactive；本版启动许可流程不恢复HMAC仲裁、不启用监测/守护服务，也不改变该事实。应用恢复健康与完整集群恢复是不同结论，本版不宣称集群已恢复。

## 验收阶段与待补录证据

| 检查 | 本记录状态 | 结论边界 |
| --- | --- | --- |
| Python宿主准备逻辑 | 阶段运行19项通过，待最终日志/源码绑定 | 本地模拟，不等于生产许可已准备 |
| Linux真实文件语义 | 阶段运行6项通过，待最终日志/源码绑定 | 本地Linux独立夹具，不等于生产变更或完整发行验收 |
| publisher门禁专项 | 等待最终执行记录绑定 | 不用宿主逻辑测试替代发布调用顺序及失败门禁验证 |
| Windows Go服务、管理员重启及信号专项 | 三包56个顶层测试通过、1个顶层跳过；含子项共6个skip事件（执行位2、符号链接4），无失败；待最终绑定 | Windows平台限制如实保留，测试注入信号/调度器，不发送真实SIGTERM |
| Linux Go专项补验 | 执行中，待完成并绑定 | 必须实际覆盖Windows跳过的执行位/符号链接语义，不得沿用平台跳过冒称通过 |
| 正式前后端构建及依赖审计 | 尚待执行 | 前端源码未变仍需按本轮正式源绑定实际构建证据 |
| 同制品11项、升级回退7项及既有API/SQL检查 | 尚待执行 | 以pk.56为实际基线，324份迁移不新增，不复制历史通过报告 |
| 正式版本分支/标签、Release及9项附件摘要 | 尚待完成并核对 | 不移动旧正式标签，不把准备脚本或草稿视为发布成功 |
| 宿主可信工具与策略安装 | root私有helper/config已安装；活动守卫、应用程序及启动epoch未改 | 安装工具与授予本次候选启动许可分别记录，secret路径不入库 |
| 生产启动许可prepare与最终check | 尚待实际执行及回执绑定 | 仅守卫/回执变更；不替换或重启应用、不授HA权 |

最终结果须补录正式commit/tree、同一二进制与制品摘要、实际检查数量及跳过/失败记录、隔离资源清理、生产许可回执及Release链接。未完成项保留“待完成”，不得用旧版日志或单纯静态检查补齐成功状态。
