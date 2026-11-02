# pk.47 审计模板、独立豁免、排行榜自动扣费与 API Key 有序多组

## 范围、基线与当前状态

从新私库 main `91b4882949d16ed81ba9f5406732dd4a710cce8e` / 已发布 pk.46 继续，目标唯一版本为 `v0.2.19-pk.47`。本记录按2026-11-03最新追加范围整理，替代同版初稿“无自动扣款/无新增ID”的描述；手动扣款仍沿用原规则，新增自动扣款独立配置。保留pk.46首页注册、两套明确选组审计、本地规则及其余有效二开。

截至本次文档更新，已完成下列前端定向模拟、最终类型检查/打包和依赖例外门禁，前端源码与产物已冻结。后端审计/排行/代理/多组路由、资源归属与缓存列表定向模拟也已按最终日志复核通过；标签前工作区快照的88项隔离PostgreSQL预检已通过。最终后端发行构建、正式提交绑定的PostgreSQL复验、同制品十一项与升级回退验收均待完成，本记录不是完整回归或发布通过报告。

## PK-041：模板与三套独立用户豁免

- 内容审计提供9类、每类12中文+12英文、合计216条关键词。按类或全部追加，大小写无关去重并保留自定义内容及原大小写，超出10000条整体拒绝。词组仅作忽略大小写的字面子串匹配，不是正则或语义判别。
- 无API Key时内容审计需选择前置拦截、仅关键词并明确选组；提示词审计可选本地规则，无需远端节点。两者空范围、无组、未选组都跳过，仍保持相互独立。
- 提示词默认模板为8类24条，20条warn与4条窄范围block，降低广泛字面命中直接拦截的风险。逐条启停/编辑处理方式；只有开启同步阻止的block才拒绝，warn继续放行。规则使用Go RE2，不理解上下文；可编辑说明文本不参与本地执行，远端固定协议也不执行该说明。
- 关键词追加、规则模板替换、说明模板替换均只修改草稿，不自动保存、启用、切模式或选组。规则与说明按钮独立；已有内容替换需确认，旧保存规则和自定义说明不自动升级。
- 内容审计、提示词审计、排行榜分别保存自己的`user_allowlist_ids`，以邮箱/ID搜索，支持保留无法解析的旧ID供明确移除、关闭用户可选、最多1000项；显式[]清空。服务端保存仍校验存在且非删除，不能把UI保留旧ID当成绕过校验。
- 两套审计豁免仍审计和留日志，只跳过本审计拦截/惩罚，不跳过另一审计和榜规则，不恢复已禁用账户。未发生实际审计拦截不写榜拦截事件。内容审计首次独立保存前兼容读取旧全局有效名单，并返回`user_allowlist_inherited`提示；保存后独立。提示词与排行榜不共享该继承。
- 排行榜名单仅豁免其自动封禁计数及自动扣费，命中仍展示；不影响原手动扣款，不清旧累计或解除禁用。

## PK-026/027：检测范围、证据、入榜与自动扣费

- 排行榜独立`group_ids`明确多选；只有保存范围内的新安全策略用量或真实审计拦截有资格执行计数/自动扣费。未选、无组及空范围仍展示，标记跳过。配置按实际入库时快照，不将异步入库时间冒称上游命中瞬间。
- 用量计数证据保存分组快照、配置版本、counted/skipped和原因；旧无证据为unknown，不按当前范围追算，不清既有累计。管理员豁免、原人工启用重置、稳定用户锁顺序、真实新增才累加及冲突/回滚不多算继续保留。
- 原榜仅读type4用量；新增真实内容/提示词/双审拒绝独立入榜，与用量联合展示。采用服务端请求身份及WS轮次去重，同请求双审一次，fallback解析后捕获实际分组；不信任客户端RequestID作为唯一键，不伪造usage或账号。warn、放行、技术不可用和既有封禁拒绝不新增，旧审计事件不追算。
- 审计证据写入有2秒独立取消边界，仅作用于拒绝请求；失败保留拒绝并记录固定错误，不宣称计数成功。BEFORE只快照/分类及锁用户，AFTER实际插入后才累计，冲突不重复计数。
- `group_rules`每条为`{group_id,multiplier:string,auto_deduct_enabled:boolean}`，最多1000条、分组须有效；默认倍率1、自动扣费关闭，范围0.000001～1000且最多6位小数。可保存未选检测组的规则，取消检测不自动删除规则；真正执行仍需`group_ids`匹配。倍率与普通用量倍率及旧手动“次数×倍率”独立。
- 自动计算额为`ROUND(max(actual_cost,0) × 该组安全审计倍率, 8)`，按数据库精确十进制处理。自动扣费与自动封禁开关独立；前置审计没有转发成本，actual_cost为0，仅有资格计数而不扣款，不伪造usage费用。余额不足整笔不扣、不产生负余额、不自动补扣。
- 正额自动扣费成功与原人工扣款共用用量身份认领，防止手动再次扣同一用量；零额不占手扣身份。人工预览仍只使用原type4未认领记录和原手动倍率，不因新榜范围/豁免改变规则，独立审计事件不进入手动待扣。
- 自动扣款技术失败必须回滚余额、ledger、认领和outbox等金融副作用，保留原usage及计数，不自动重试；记录`failed/charge_failed`。若证据表本身不可写，则放弃扣款、固定warning，不能让原用量丢失。该子事务故障注入与并发已通过下述工作区真实PostgreSQL预检，正式提交绑定的同制品复验仍待完成。
- 明细返回`auto_charge_status/reason`及可为空的`actual_cost/audit_multiplier/auto_charge_amount`十进制字符串。状态含deducted、zero_cost、insufficient_balance、skipped、previously_charged、failed；无证据为historical_unknown。界面区分计算额与本次实际扣除，未知金额不显示成0。
- 管理员可见来源、用量/审计ID、组快照、计数和扣费证据；公开榜仍为脱敏只读，不能返回管理明细。证据与用量清理分离，清日志不能洗累计/扣款认领。

## PK-037：本地确定审计拒绝的成功率归因

只有实际本地block、明确拦截动作、响应码与WS轮次相符时，写入精确`error_type=security_audit_block/error_source=security_audit`标记；保留错误明细与审计日志，但不将其当作上游失败或请求成功率失败。账号最近成功/连续失败、健康度与channel monitor v2使用相同精确标记排除。上游403或相似错误文本、warn、技术不可用不能获得此排除；历史日志不重写。后端定向模拟及工作区快照的真实SQL隔离预检已通过；正式提交绑定的同制品故障场景待验证。

## 新增 PK-042：API Key 有序最多20组

- `group_ids`有序去重、最多20个正整数；legacy `group_id`保持首项。旧NULL数组派生原单组，不回填；显式[]无组，更新省略保留，旧group_id-only更新收敛单组，两字段显式矛盾拒绝。
- 逐组存在/active、用户公开或专属权限及订阅active准入，sk-iq专用Key不可改组。DTO返回全候选groups，缓存v26、分组筛选/失效及订阅迁移覆盖任一候选；保存组不写余额/用量。
- UI支持搜索、选中顺序、上下移动/移除、最多20组与不可用旧ID明确移除。创建/编辑继续至少一组校验，行内修改显式保存/取消；类别切换保留原清选规则。旧bulk单组操作保持兼容，修改无关设置不发送分组字段。
- 组标签分别展示普通/用户/高峰用量倍率和安全审计倍率、自动扣费开关，避免混为同一倍率。
- 路由实现发送前按已保存顺序择可用组、重新校验实际组准入/限额及审计；已发送请求不跨组重放，计费/审计快照使用实际服务组。此链路已实施并通过本机定向模拟，正式制品、多平台完整协议及并发组合仍待验证；UI保存测试不能作为路由通过依据。
- 资源创建使用当前选定组；已有Gemini缓存/视频按原Key/用户、资源组与账号绑定访问，不因候选重排改组或跨组重放。身份资源查询不要求生成号池当前可用，仍核对权限/有效订阅与协议兼容；生成请求保留实际号池准入。
- Gemini缓存列表使用认证阶段返回的当前可访问组ID，只逐组读取同一API Key、有效且在同一beforeID游标后的记录。每组使用相同limit和一次过期时间快照，合并按ID降序去重截limit；handler沿用pageSize+1及原nextPageToken。明确空集合不回退首组，单组旧路径保持；未获准组、其他Key及过期记录不显示，任一查询失败不返回半页，新增服务/handler模拟记录见验证节。

## PK-025：币安代理输入规范化与安全原因

仅清理URL边界空白/指定零宽字符、尖括号/反引号、标签和目标相同的Markdown链接包装；不修复、解码重编码或删除凭据内部内容。歧义链接、空结果、内部空白/控制符、非法协议/主机/端口/路径/查询/片段拒绝；错误仅固定原因分类，不能输出原URL、凭据或解析器文本。规范化不能将失败输入变为清除已保存代理，原HTTP(S)/SOCKS5(SOCKS5H)及香港预探测限制保留，非法配置/出口失败不直连兜底。尚无真实币安请求验证。

## 迁移与兼容

| 迁移 | 范围 | 待验证边界 |
|---|---|---|
| 258 | 榜分组/豁免/组规则、配置revision、用量计数证据、独立审计事件及自动扣费证据/触发器 | 并发、重复入库、人工恢复、正额与手扣互斥、零额、余额不足、故障子事务、清理和旧程序回退 |
| 259 | API Key nullable JSONB有序group_ids、GIN、首组同步触发器 | 不回填旧记录、显式空/缺省区别、旧程序仅写group_id兼容、缓存及并发保存 |

不修改245等已发布迁移或旧标签/制品，不批改历史计数/扣款/审计，不删除回退保留的新对象。回退旧程序不能产生新审计桥接，且只能按兼容首组工作；具体旧程序升级/回退实测仍待完成。首字起点、CF、更新凭据、加密密钥及现有生产数据保持。

## 实际前端定向验证记录

均在本机Windows的frontend目录以Vitest模拟API执行，未调用真实模型、授权或币安服务；复用既有测试文件，重复文件/用例不能累计为独立覆盖数。

| 执行时间（Asia/Shanghai） | 范围 | 实际结果 | 原始日志 |
|---|---|---|---|
| 2026-11-02 23:43:57 | RiskControlView、PromptAuditView、components、viewModel、UsageView、payment、locale完整性 | 7文件105项通过，18.64秒 | `.runtime/pk47-audit-user-exemptions-frontend-tests.log` |
| 2026-11-03 00:01:50 | KeysView、BulkEditKeysModal、keys.bulkUpdate、locale完整性 | 4文件48项通过，50.59秒 | `.runtime/pk47-key-multigroup-frontend-tests.log` |
| 2026-11-03 00:32:24 | UsageView、payment、locale完整性；包含新增failed/charge_failed展示 | 3文件60项通过，24.81秒 | `.runtime/pk47-ranking-autocharges-frontend-tests.log` |

最后一次排行榜测试覆盖组规则在取消检测后保留、与自动封禁独立、倍率边界/精度、豁免保存/清空、缺失组规则移除、服务端精确金额、零额/余额不足/已扣/技术失败/历史未知及手扣原交互。00:26:13同组60项曾通过，最终日志由上表00:32:24复验覆盖，不作为另一组独立覆盖。`git diff --check -- frontend`已通过；后续源码变化需按实际重新验证，不能沿用记录冒称新代码通过。

后端定向模拟：见下节实际日志。最终后端发行构建、隔离PostgreSQL迁移/事务与升级回退、服务器同二进制十一项制品验收：**待验证**。浏览器视觉验收、真实上游/授权/币安请求及全套源码回归：**未执行**。

## 实际后端定向模拟（2026-11-03）

使用本机Go工具链及`-tags unit`定向选择，模拟API/数据库接口与本地测试服务，不连接真实模型、授权、币安或生产数据。以下是互补的实际执行记录，不能改写原失败日志为全通过，也不能累计称作完整源码回归。

| 日志 | 实际结果及边界 |
|---|---|
| `.runtime/pk47-backend-final-tests.log` | handler、middleware、repository、securityaudit、binancepay、cmd/server所选用例通过；routes显示no tests to run，只是编译成功。service唯一失败为`TestAPIKeyGroupAvailabilityOAuthOnly/anthropic`测试夹具，其余所选用例未报失败；该轮整体失败，保留原日志。 |
| `.runtime/pk47-service-group-router-final.log` | 修正上述OAuth夹具后按`^TestAPIKeyGroupAvailability`复验service通过，exit0，0.079秒；覆盖模型/账户准入、平台端点、WS、OAuth、compaction/图片/视频能力及绑定资源归属等定向场景。 |
| `.runtime/pk47-service-routes-final-tests.log` | service最终目标集重跑通过，0.375秒；同轮routes的`TestGatewayRoutesGroupModelAllowlistMountedOnEveryGatewayRoute`仍按旧auth源码形态断言而失败，该轮整体失败，保留原日志，不将service包通过改写为整轮通过。 |
| `.runtime/pk47-gateway-routes-final-tests.log` | 更新两条源码断言以适配`WithAPIKeyGroupRouter`包装，并继续要求auth→groupModelAllowlist→composite顺序；routes最终重跑通过，exit0，0.117秒。 |
| `.runtime/pk47-backend-routing-wiring-final.log` | middleware 0.069秒、cmd/server 0.060秒，exit0；最终模型/body/WS/资源归属及后台清理注入的定向用例通过。 |
| `.runtime/pk47-gemini-multigroup-list-tests.log` | service 0.078秒、handler 0.079秒，exit0；4个顶层用例包括原list/get、多组全局ID游标分页、owner/获准组/过期隔离、重复去重、同一时间快照/每组limit、明确空范围、旧单组回退、查询失败无半页及取消。 |
| `.runtime/pk47-security-audit-metrics-renderer-fix.log` | 最后复核发现旧OpenAI/Anthropic内容审计仅返回error.type、Google仅返回数字code，原分类未匹配。补齐内部分类兼容后，`^TestSecurityAuditMetrics`定向通过，exit0，0.088秒；覆盖6种实际HTTP renderer的提示词/内容拦截经Ops middleware入队、旧响应格式保持，以及WS首轮、成功后拒绝、先真实上游失败后拒绝。WS拒绝仅标记当前轮次，101状态与此前真实失败保留。未改SQL或前端。 |

Gemini列表准确筛选为`^TestGeminiCachedContent(sListAndGet|sListForGroupsPaginationAndOwnerIsolation|ListForGroups.*)$`，仅`./internal/service ./internal/handler`、`-p 1`、`-count=1`；测试代码加入既有文件，没有新增遗留临时测试文件。以上通过仅覆盖所选模拟场景；迁移258/259、金融原子性/锁序、真实SQL、同制品服务API及升级回退须另行完成隔离验证。

## 最终前端构建与证据冻结（2026-11-03 00:50:25）

- 使用已安装依赖执行`pnpm run build`，实际exit0，包含语言键1文件3项通过、`vue-tsc -b`通过和Vite1191模块/81秒构建完成。日志为`.runtime/pk47-frontend-build-final.log`；未安装或改变依赖。Browserslist数据旧、动态/静态混合导入和部分chunk超过500kB为构建警告，未冒称已优化。
- 构建前后1027个非增量缓存输入哈希一致：`91b7c237f15a506bc66d08c8af0dccfbb578bdfeb17a70eec09744ca1f5ad17a`。冻结工具按schema2再次枚举全部1029输入（含tsbuildinfo）及197个dist文件，记录后立即verify通过；无构建后非生产文件变更。
- `.runtime/pk47-frontend-build.json`及`.runtime/pk47-frontend-files.json`绑定source hash `85ac97a93f0e0fa812fa7f5b37517d24274ea70102dacf6fc003dd49f6b5b22a`、dist hash `5fe1b046cd30169f01fa95e0fe34ed8625e754fb273ccdd9a66fd4ad71e6eea5`；lock hash `1e73d5508cbdecd5273421d319199d5e5852cbfb424417ea8f85767209cedbc9`。这是构建后冻结证明，额外以构建前快照核实期间无输入变化，不冒充后端或运行时验收。
- 早前审计未保留可核退出码，因此只读刷新审计取得真实退出证据；默认镜像源无审计接口的错误JSON被proof拒绝，随后仅该命令指定官方npm registry成功取得有效报告。实际`pnpm audit --prod --json --registry=https://registry.npmjs.org` exit1，现行`check_pnpm_audit_exceptions.py`门禁exit0；剩余critical0/high2/moderate12/low2，两个high均为原有xlsx只写导出例外，未扩大例外或声称零风险。原始报告、门禁日志及退出码文件均在`.runtime/pk47-frontend-audit*`。
- 后续若前端源码、锁文件、产物、依赖审计或政策证据变化，必须重新检查/冻结，不能沿用本快照作为新输入的通过证据。

## 标签前工作区PostgreSQL预检（非发行证据）

2026-11-03 01:14:53～01:20:01（Asia/Shanghai）在服务器全新、用完即清理的独立DinD中执行PostgreSQL 18隔离测试。只挂载本次新建证书/数据卷，不挂载生产卷或宿主Docker socket；内层数据库使用独立internal网络、无宿主端口，测试数据均由fixture生成，未连接业务数据库、真实账号、模型或币安接口。

- 真实基准HEAD为`91b4882949d16ed81ba9f5406732dd4a710cce8e`，报告明确`source_state=working-tree`、`release_evidence=false`，profile为`pk47-prerelease-working-tree-pg-v1`。输入快照SHA256为`c30e2aa08bce36363456f865971a1594759f03b1c05eaec25cc3e46ad2b6ac8e`；不能将该HEAD冒称未提交修改的正式发行提交。
- 普通表和分区表各44项，共88项通过：计数36、独立审计24、自动扣费24、附加4。覆盖真实新增/重复/并发/反向批次/手动恢复/配置修订/保留历史，精确十进制扣费、零额/余额不足/豁免/手扣互斥/回滚，ledger及证据表故障保留原usage且不扣款，以及259有序组兼容与回滚。
- 附加检查直接提取三份Go文件中的实际SQL并在合成数据上执行：只有精确本地审计标记被排除；真实403/503、只伪装type或source的记录仍计失败；同WS较早成功仍保留首字和成功记录。此项不使用重新手写的替代统计查询。
- 通过报告：`.runtime/pk47-prerelease-pg-7a1fa609127c/report.json`。迁移258原文SHA256为`dd43387aab0c7dbe27f142f0a71597f33f40432277bcbf093b2e367de007b18f`，259为`fcf6ba6d8a07ebe38caa20f73f719f6a96f078915393d171cc10340cba5d8795`；244/245保持原文哈希，三份实际SQL源码哈希随报告保存。内层数据库资源和外层DinD/卷均清理通过。
- 此前两个独立run在第一条fixture初始化SQL处失败，0项通过，固定错误为`prerelease_pg_fixture_failed/isolated_docker_exec_failed`；第二次记录无SQLSTATE/数据库对象。原失败报告保留在`.runtime/pk47-prerelease-pg-ddc4e76652af/report.json`和`.runtime/pk47-prerelease-pg-afa83c1b92c3/report.json`，两次清理均通过，未覆盖或改写成通过。只改ignored工具的数据库启动就绪判定：官方镜像完成初始化后再检测，而不接受初始化临时服务器的`pg_isready`。随后第三次快照完成上述88项，真实迁移与业务源码未因这两次启动失败修改。
- 归档工具进一步将就绪检测收紧为初始化完成后对目标fixture库实际`psql SELECT 1`，并保持固定错误类别/输入哈希诊断、禁止输出SQL或数据库值；该最终工具版本由正式同制品验收重新执行。上述通过结论仅绑定列出的工作区快照，不能替代最终提交的88项、13项服务API、7项升级回退及11项运行时验收。

## 发布边界

仅新私库 `yb1320057104/sub2api` 的GitHub Release交付同一二进制网页更新包、Docker镜像归档、SHA256及绑定源码的实际证据，用户自行切换生产；不宣称GHCR已发布，不部署/重启生产，不触发真实模型、授权或币安请求，不运行全套源码回归。正式标签仍为唯一`v0.2.19-pk.47`，旧标签/制品不移动；前端构建已完成，最终后端/制品构建、验收和Release发布尚未完成，不把计划写成结果。

## 最终发行结果

- 正式提交 `104334fa2157da7d8259441e49e8879fac71e86d`，tree `7fc3cfc271685a381895ff78e8a6f6f83b700162`；版本分支及正式标签均绑定该提交，提交已在私库 main 历史中。后续仅补录此结果，不移动标签、不重打已发布制品。
- Go 1.27.3 windows/amd64 本机构建 Linux/amd64，CGO关闭、embed、trimpath、release标识；程序 SHA256 `0521096f87857bce06a32c000719389e1d79cb97da924894826e7e54048b57f7`。本轮源码、前端产物、工具和二进制证据绑定一致。
- Package `1793641195-104334fa2157`；同一程序进入网页更新包和 Docker 镜像归档。更新包 SHA256 `d8b965c240db012eae268f616452edbb7e0c57a2592e4d8bd106a85d4db45e3b`；镜像归档 SHA256 `b2cf4fef2966febbfbcf6798313fd2e5202d7874b6ac76ee45425a2d5f422c4c`；镜像ID `sha256:d42f51e8e484ac797fa2207e113eec4c2a37a12257ae5fb2cde0fce264f13b26`。
- 服务器独立 DinD 的 `pk-artifact-v2` 十一项全部 passed/exit 0，独立空库升级回退七项及同一制品服务API十三项全部通过。旧迁移清单及校验值保留，仅新增258/259；兼容基线 `0.2.19-pk.46`，实际程序与镜像seed分别绑定。新分组配置/计数/扣费证据、有序分组对象及原币安专属代理字段在升级回退后保留；旧程序保存旧设置字段兼容，仅写group_id时有序分组兼容收敛为该单组。测试容器、数据卷、内部网络和专用daemon清理已验证。
- 第一轮正式验收的十一项制品检查、四项升级/迁移检查及前十项服务API通过，随后分组列表返回数组被夹具的统一对象断言拒绝，整轮失败。仅修正该GET端点的严格数组/元素校验，业务源码、正式标签及本地二进制保持；第一轮原报告及独立清理回执保留于`.runtime/pk47-stage-attempt1/`，随后在新隔离环境用同一本地二进制完整重验取得本次通过证据。未将首次失败改写为通过。
- 隔离PostgreSQL普通表/分区表专项共88项通过：计数36项、独立审计24项、自动扣费24项、附加4项（有序分组迁移/回退和实际成功率SQL各覆盖两种表布局）。报告绑定正式提交、244/245/258/259迁移源码哈希及三份实际统计SQL源码哈希；具体检查与清理证据完整归档于cyber-count-migration.json，未使用生产数据库或业务数据。
- 三份实际统计SQL源码SHA256：`channel_monitor_v2_aggregation.go` `bfb51647d52384ddb528c5084b876df1bb1f0b8bfc7028889443c14e41e66425`；`account_health_stats.go` `60fa1177dd315bb8eff945a9e7bdc152e8568062a868e0cab85f920ea6f98ef7`；`account_request_stats.go` `e62a964f3b88a25e27bc23232f2f3adff8091610f62622255e9e71b35ca36ede`。
- 正式私库 Release 已发布并核对为 latest、非draft、非prerelease：[v0.2.19-pk.47](https://github.com/yb1320057104/sub2api/releases/tag/v0.2.19-pk.47)。9项资产逐项核对名称、长度及GitHub SHA256，包含网页更新包、镜像归档、checksums及本轮构建、制品、计数专项和回退证据。
- 本轮未执行生产切换、重启或生产数据修改，未发布GHCR；由用户手动切换 pk.47。首字计时起点保持；成功率、账号健康度/连续失败及channel monitor v2使用新口径，仅精确排除实际本地审计block且响应码/WS轮次匹配的security_audit_block/security_audit记录，上游403、相似文案、warn及技术失败不在豁免范围，历史日志不回写。不运行全套源码回归或真实模型、授权、币安接口。
