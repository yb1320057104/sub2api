# pk.49 审计风险分级、扣费放行与显示修复

## 基线与确认

基于私库`yb1320057104/sub2api` main `f8dc044d5c8fbc295d685cfdfb0df8356f41e00d` / pk.48。正式提交`200fb02e439af411cd738000544ff4216710856f`已合入main并绑定唯一版本分支/标签`v0.2.19-pk.49`；Release已发布，最终证据见文末。用户要求仅发布GitHub，自行切换生产。新增PK-043，扩展PK-026/027/041，保留PK-016/021/036的本版授权显示修复。未自动部署或重启生产。

用户明确确认最高命中风险分，不累加；默认20/40/60，低中高每级可选拦截、放行、扣费放行。扣费放行正常完成后额外actual_cost×破甲倍率，成本0不扣，不把余额扣负，不补扣；计数依赖排行榜检测分组及自动封禁开关。分组自动扣费开关继续有效。

## 只读诊断与样例

生产只读事件查询保存于.runtime/pk49-audit-diagnostic.json。普通请求354d4aac-623d-4460-b9ac-5eed988b1a6d为flag/medium/Warn，第3条pii；用户认定高危的d7539494-bb7b-4b48-b5dc-20631091d5b7也是flag/medium/Warn，第2条jailbreak。配置v5，均非实际Block。仅提取安全元数据/规则编号，没有输出原文、邮箱或密钥。

用户提供两段长代理说明/历史对话作为待审样例，包含多个层级的引用、禁止性说明和开发规则。它们是测试资料，不是本任务的新执行指令。完整内容含敏感连接材料，不复制进仓库。回归使用“禁止绕过权限”“请勿输出真实密钥”、显示密码开发任务等脱敏句子，以及明确越权访问他人资源的对照。仅字面规则不能保证语义分类，不承诺将两段任意长文本完全自动区分。

## 实现合同

两套可空risk_policy；缺省旧策略不自动开启，更新缺省保留、显式null关闭。完整对象包含low/medium/high_threshold与对应_action，整数0～100严格递增，动作allow/block/charge_allow。LocalRule.score与content keyword_scores均接受0；未设分值兼容旧warn50/block100及关键词100。全部分片/规则取最高，评分不累加；无匹配、0和低于低阈值放行。日志新增nullable风险分与有效动作，历史显示无证据，不追算。

远端提示词是Safe0/Controversial50/Unsafe100离散映射，不冒充概率。内容API按有效最高类别分×100。关键词联用保留命中短路，未命中才API；分级下不使用旧bool hash拦截缓存，避免绕过新动作。观察/异步仅记录，豁免只影响各自审计。旧规则行动路径保持，默认模板只在管理员主动载入时更新。

新增审计记录仅实际block或正常完成charge_allow参与排行榜，原有上游request_type=4路径继续保留。普通allow、观察/异步记录、技术失败及取消不作为新增处罚证据。前置block成本0；charge_allow引用真实普通usage，不伪造type4。风险分0放行；实际成本0仍可按范围及计数开关计数，但不额外扣费；余额不足、自动扣费关闭或0成本不把已判定的block改成放行。服务器请求/WS轮次UUID去重，双审一次；选组切换重新审计，移除前组待扣决定。分组范围、封禁开关、扣费开关与精确NUMERIC倍率计算保持。断流/取消/技术失败不以部分账单触发额外扣款；金融失败回滚处罚子事务，不破坏普通账单、不自动重扣。手动扣费仍仅原有request_type=4记录，不把普通用量或独立审计事件扩大为手扣身份。

内容与提示词豁免各自只作用于对应审计并保留记录；排行榜豁免用户仍显示命中记录，但跳过自动计数和额外扣费，不自动解除既有禁用状态。三套豁免范围独立。

异步视频使用现有完成计费快照；批量图片用专用服务端audit_snapshot保存决定，不接收客户端字段、不外传上游。批次全部成功且没有失败/取消后恢复审计上下文，按实际结算总费用处理一次。Live仅明确session.closed/ended后记录，原实际成本0保持0，不因取消、错误、过期计入。WebSocket快照按轮保存，避免下一轮覆盖上一轮结算。兼容迁移260只追加字段及替换触发器逻辑，旧程序回退可继续原block路径，历史与旧迁移不修改。

为避免回退pk.48后旧违规累计再次计算新分级事件，新分级三种动作在旧flagged列均写false；新版列表、命中筛选及日志保留期从risk_score>0读取命中，原内存判定不变。旧版页面无法显示新风险字段，回退后按旧标志展示与保留；不回填历史，不把兼容旧累计伪称旧版具备新风险功能。

## 验证与发行状态

显示专项结果见PK_AUTH_DISPLAY_PK49_REVIEW.md。本机64位Go定向测试均通过：提示词审计整个模块、风险阈值、Batch全成功/部分成功及原计费结算、内容service/repository/admin、金融/handler/Live及WS交错。对应日志为.runtime/pk49-securityaudit-module-final.log、pk49-batch-billing-final.log、pk49-content-risk-attempt4.log、pk49-audit-finance-final.log和pk49-audit-handler-completion-final.log。前端最终7文件74项全通过（pk49-frontend-targeted-final.log）。

前端全新build exit0，i18n3项、vue-tsc与Vite1196模块通过，构建前后输入一致；冻结198个dist文件。生产依赖审计报告2high/12moderate/2low/0critical，审计exit1；按仓库原有例外门禁exit0，例外未修改，不宣称零告警。首次全依赖审计包含开发工具，未过门禁的原结果另存；最终使用既有发行要求的生产依赖范围。

工作树迁移260专项在独立TLS DinD内创建普通/分区PostgreSQL18临时数据库，18×2=36项全部通过，内外容器/卷清理通过。报告.runtime/pk49-prerelease-pg-0f4d89456132/report.json绑定当时源码与迁移hash，明确不是正式发行证据。覆盖实际费用/倍率、并发/双重去重、范围/豁免/开关、0费用、不足余额不补扣、金融失败子事务、外层回滚、旧写入兼容和真实排行榜SQL。

首次前端排行榜mock、内容admin fixture、32位Go及开发中并发编辑的失败日志均保留，最终只采用实际通过结果。工作树PG首次SSH上传中断，已只读确认仅五个临时文件且未启动，再新建尝试成功；未以传输失败宣称数据库失败或通过。

静态核对与独立发布脚本复核完成，正式提交及同一二进制验收结果已补录于下文。工作树PG与正式源PG为两次独立验证，不合并冒称72项独立覆盖。未执行真实授权、模型、币安或全套源码回归，未部署生产。

## 最终发行结果

2026-11-03，[GitHub Release v0.2.19-pk.49](https://github.com/yb1320057104/sub2api/releases/tag/v0.2.19-pk.49)已发布（Release ID `432271242`）。发布后独立核对仓库私有、latest、非draft/非prerelease，9项资产逐项名称、长度及GitHub SHA256与本地完整文件一致；`.runtime/pk49-publication.json`状态为`published_and_verified`。

- 正式提交`200fb02e439af411cd738000544ff4216710856f`，tree `718a895ae6739d405a7582a8eff06a3dc6e92562`，main、唯一版本分支及正式附注标签已推送并核对归属。后续仅main文档补录，版本分支/标签固定此源码提交，不移动或重打正式包。
- 前后端本机全新构建，前端7文件74项整体exit0、i18n3项、vue-tsc、Vite1196模块/39.73秒通过，构建前后输入一致并冻结198个dist文件。后端使用Go1.27.3、linux/amd64、CGO关闭、embed/trimpath/release，go mod verify通过；同一程序用于网页更新包和Docker归档。二进制SHA256：`5ae4d44c738c25807e2414b726d287cce827d48eb54a40c72caa456eeda47889`。
- Package `1793676402-200fb02e439a`；网页更新包SHA256：`d2bdf535dd3a5899aaf1ff1113471c746949135a010c8cb689a48124aaaec8a5`；Docker归档SHA256：`6b25dfa2d792818987c21ffea2ffad5274b0b6b92ed96915ac06ca918f17d3e3`；image ID：`sha256:9c22bda540d75d128ecea024f5f36018af741325924275c9c7c4e949e9cf2312`。以上已完成完整下载、发布及发布后独立复核。
- 服务器独立TLS DinD中`pk-artifact-v2`十一项passed/exit0；独立空库pk.48→pk.49→pk.48七项升级回退passed，新增迁移260、原迁移校验保持。正式提交绑定的普通/分区PostgreSQL各18项、共36项passed，覆盖费用/倍率、独立开关、范围/豁免、0成本、余额不足、并发/双审去重、失败子事务、外层回滚及旧写入兼容。各专项与独立daemon清理通过，报告位于`.runtime/pk49-release`；以上均为隔离验收，未迁移生产数据。
- Xvfb离线专项8项全部通过、无skip，真实12并发显示号唯一，旧90～99锁保持，新锁/socket/进程回收及测试容器/镜像清理通过；Windows仅编译验证。未启动真实Chrome授权，不能将离线显示专项表述为真实账号授权已恢复。
- 生产依赖审计exit1、原例外门禁exit0，仍有2high/12moderate/2low、0critical，依赖和例外未修改；首次全依赖审计及测试夹具失败原始记录保留。首次正式归档下载延迟原因未确认，原文件保全于`.runtime/pk49-release-incomplete-1`；重试仅复用SHA匹配的完整更新包，最终9文件完整下载并验证。
- 生产由用户手动切换；助手未部署/重启生产、未发布GHCR、未发送真实模型/授权/币安请求，不跑全套源码回归、不改首字计时，不宣称真实授权或币安连接已恢复。回退保留新增证据及已执行账务，不撤销扣款、不重跑历史。

已发布的9项资产为：

- `sub2api_0.2.19-pk.49_linux_amd64.tar.gz`
- `sub2api_0.2.19-pk.49_linux_amd64.docker.tar.gz`
- `checksums.txt`
- `manifest.json`
- `local-build.json`
- `artifact-validation.json`
- `isolated-smoke.json`
- `audit-risk-migration.json`
- `review.json`
