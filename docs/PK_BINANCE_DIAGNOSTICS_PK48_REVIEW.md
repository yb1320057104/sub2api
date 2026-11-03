# v0.2.19-pk.48：币安错误诊断与分组交互

## 范围与基线

- 基于私有 `yb1320057104/sub2api` main `897c47b5909a9e6a49e7941388dff7f564d64215`，已有pk.47正式提交 `104334fa2157da7d8259441e49e8879fac71e86d`。
- 用户要求查明币安配置香港专属代理后仍查账失败；将安全审计倍率改名红色“破甲倍率”；修复多组叉号不能取消选择。用户补充API Key已设置IP白名单。
- 沿用PK-025/026/027/042/021，无新增ID、迁移、依赖或账务公式变化。保留既有审计成功率排除、首字计时起点、CF、更新凭据、加密密钥与业务数据。
- 本轮授权仅发布GitHub Release，生产由用户手动切换；不部署/重启生产，不发真实模型、授权、币安请求，不跑全套源码回归。

## 已确认的原因与证据边界

现有client会将HTTP拒绝、网络/代理故障与安全code丢弃或在service中统一映射为“币安查收失败，请检查 API 读取权限、收款 UID、凭据和网络”。该句不能证明代理无效，也不能排除IP白名单或权限问题。

只读核查确认生产运行pk.47，渠道已启用且Key、Secret、专属代理均已配置，未启用节点预探测；最近失败计数4，对应普通失败8分钟退避，未见有效429/418锁或进行中token。持久错误为旧泛化提示，现存相关日志没有可恢复的上游code。不解密或输出凭据、不请求币安。受限本机原始脱敏证据：`.runtime/pk48-binance-proxy-diagnostic.json`。

用户启用了IP白名单，因此必须匹配**请求实际代理出口IP**；代理服务商网关地址及“香港”地区不是出口身份，轮换出口可能造成不匹配。建议使用固定出口并匹配白名单；不能据此直接宣称本次已经证实为白名单错误。新版只为后续真实查询保留具体失败类别，不声称已验证用户代理或币安连通。

用户随后明确回复，白名单目前填写的是**部署项目的服务器IP**。这与外部专属代理的出站路径不匹配，已告知需要把代理实际出口IP加入白名单并优先使用固定出口。此为用户确认的配置问题；本轮未登录币安修改白名单，未获取/验证代理出口或观察真实拒绝code，不能冒称已经恢复查账。

## 实现边界

币安使用typed固定错误类别，保留校时、权限检查、Pay账单等阶段，安全HTTP状态和有界数字code。覆盖代理CONNECT 407/拒绝、DNS、连接拒绝/重置、TLS/超时、API Key/IP白名单/权限、签名、时间、地区拒绝、异常HTTP/响应，以及本地解密/收据处理失败。禁止展示签名URL、代理认证信息、原始响应或任意上游message；429/418仍优先走原RateLimitError，不解除硬锁或自动切换直连。

校时 `/api/v3/time`、权限 `/sapi/v1/account/apiRestrictions` 和Pay历史 `/sapi/v1/pay/transactions` 继续复用同一client及专属代理；正常轮询不为诊断新增权限请求。签名参数、UID本地过滤、收据幂等/入账逻辑保持。

前端仅更改可见名称及样式，API的`security_audit_multiplier`与规则`multiplier`字段、精度范围、实际费用乘倍率公式保持。分组徽章及排行榜配置红色展示“破甲倍率”；普通用量倍率和自动扣款状态独立。

多组选项删除后原节点可能从DOM移除，document的外部点击判定误关Teleport弹层并丢草稿。叉号使用明确remove并停止冒泡，行内弹层内部click不冒泡至document；保存提交剩余所选有序ID，取消不保存。可以删空草稿，界面原有至少选一组的保存校验保持，未扩改API的明确清空兼容行为。

## 验证与发布状态

后端最终定向模拟已通过：本机Go1.27.3 windows/amd64，`go test -tags unit -p 2 -count=1 -run '^Test(Binance|PKBinance)' ./internal/pkg/binancepay ./internal/service`，exit0，两包分别0.022秒/0.087秒。最终日志`.runtime/pk48-binance-targeted-final2.log`，SHA256 `cd2e5e22d9d24c1feb1113ff4fcc38d712c6bf32714c3c8866e77fdec67c210a`。覆盖代理CONNECT 407/403/502无绕过、HTTP及安全code、错误脱敏、16KiB错误正文上限、429/418不读取正文且保持冷却、固定端点签名/校时/UID过滤、typed错误持久化及修改连接配置的恢复边界。此前两轮分别通过后又补错误正文上限和测试atomic计数，均保留各自日志，不以早轮结果代替最终版本。全部使用fake transport或本机httptest，无真实币安请求。

前端最终定向模拟：`.runtime/pk48-group-ui-tests-final.log`，KeysView36项、UsageView44项、GroupSelector3项、语言键完整性3项，4文件86项通过、26.37秒。使用真实OrderedKeyGroupSelector、Teleport和原生DOM点击，覆盖创建/编辑/行内删除、删空草稿原校验、保存/取消/外部点击、未知组、重复旧事件及顺序；红色破甲倍率的中英文显示与原普通用量倍率/保存金额保持。首轮85通过/1失败仅为Vitest2不支持matcher，修正断言后复验；负向复现暂恢复旧事件处理的两个目标用例均失败（其余34跳过），随后恢复最终修复再执行上述86项，失败日志分别保留，不冒充通过。此为本机模拟，不是浏览器视觉或真实API测试。

前端正式构建于2026-11-03 03:22完成并冻结，`pnpm run build`实际exit0：语言键3项、vue-tsc通过、Vite1191模块/47.12秒。构建前后1027个非tsbuildinfo输入哈希一致 `25376917ad019bb579ba02fc9e91b308b7f2383be94841be4dd6768d51177f32`。schema2 proof绑定1029个源码输入hash `74251dc67f38517eb27c9c2faa9a11706cff3ec04937c3dcdd28525a01d51182`、197个dist文件hash `2ba1b6071a575088770e20837d4295556c0d53bbca94e07edcfc3ac1171620aa`，record及verify均exit0；锁文件保持 `1e73d5508cbdecd5273421d319199d5e5852cbfb424417ea8f85767209cedbc9`。只读官方registry审计实际exit1、现行例外门禁exit0（critical0/high2/moderate12/low2，原有xlsx导出例外），未改依赖或扩大例外，不冒称零风险。构建警告为既有Browserslist数据旧、动态/静态混用导入及大chunk，无构建后源码修改。

定向验证另绑定`.runtime/pk48-targeted-checks.json`的两组真实通过日志及24个源码文件哈希，owner确认测试后输入保持；此后冻结快照不冒充测试前捕获。最终后端本机构建、服务器独立同制品11项及升级回退7项验收、GitHub Release：进行中，实际结果完成后补录。无新增SQL/迁移或金融行为，本轮不重复pk.47的88项PostgreSQL/13项服务API专项，历史结果仅作历史依据。

只从新私库发布正式非prerelease Release，归档同一程序的网页更新包、Docker镜像、SHA256及实际证据；不宣称GHCR已发布，不移动旧标签，不修改已发布制品。

## 最终发行结果

- 正式提交 `061d058900d99eacccfe9ad8eece8030f736b36f`，tree `1930d4cb7c3e557a05316186058dd5145de6b1f4`；版本分支/附注标签`v0.2.19-pk.48`与main原子推送，远端标签剥离后绑定同一commit。后续只在main补录实际结果，不移动标签、不重打正式包。
- Go1.27.3 windows/amd64本机构建Linux/amd64，`go mod verify`通过，CGO关闭、embed/trimpath/release；二进制SHA256 `ae732f7380b0940998a69741a0e045e15bdda5aaaa6dea513d0190edca1168db`。构建前后前端与定向验证输入哈希一致、源码干净，实际build exit0。
- Package `1793647590-061d058900d9`；同一程序进入网页更新包和Docker镜像归档。更新包SHA256 `0e0029a9f4b01ab0abe0d437d6a933c9b0a2e8fbd1669c4e3e06c3a0aa287e99`；镜像归档SHA256 `ce7a70174bedb93e8603e09b0174590453c25b9ae2911e65f9d92f8dcd7ba0aa`；image ID `sha256:1ae911638cad0d7c2749febc6501fb1b33cc2c3563c6a17a40ff8590b6f78c57`。
- 服务器独立TLS DinD `pk-artifact-v2`十一项全部passed/exit0，独立空库pk.47→pk.48→pk.47七项升级回退全通过，316条既有迁移清单及校验值保持，无新增迁移。分组规则与计数/审计/扣费证据、API Key有序分组、渠道专属代理字段保持；全部合成禁用夹具，启动任务保持未激活，无真实上游调用。子容器/卷/网络及独立daemon清理通过。
- 上线前基线只读确认：生产实际pk.47程序 `104334fa2157da7d8259441e49e8879fac71e86d` 与原镜像seed分别绑定，未把网页更新程序身份误当作镜像seed。生产程序、数据及配置未改动。
- [GitHub Release v0.2.19-pk.48](https://github.com/yb1320057104/sub2api/releases/tag/v0.2.19-pk.48) 已发布并核对私有、latest、非draft/非prerelease，8项资产逐项核对名称、长度与GitHub SHA256。证据`.runtime/pk48-publication.json`。正式Release资产包含网页更新包、镜像归档、checksums、manifest、artifact-validation、local-build（含前端/定向证据）、review、isolated-smoke。
- 用户手动切换生产；本轮未部署/重启生产、未发布GHCR，不跑全套源码回归或真实模型/授权/币安请求。用户需把代理实际出口IP加入币安白名单；此次仅确认其填了服务器IP的配置不匹配，不宣称已恢复真实查账。
