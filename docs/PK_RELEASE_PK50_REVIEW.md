# pk.50 集成与发布记录

本版从私有仓库 `yb1320057104/sub2api` 的 main `da1ec74e236cef072a97e1c545a796c686d49762` 整合。新增 PK-044 账号多代理，扩展审计分级与可观测性、币安备用出口、授权入库出口及 PK-028/032 模型档位。四份专项核对记录分别为 `PK_AUDIT_PK50_REVIEW.md`、`PK_MULTI_PROXY_PK50_REVIEW.md`、`PK_BINANCE_PK50_REVIEW.md`、`PK_MODEL_OPTIONS_PK50_REVIEW.md`。

## 集成边界

两个会话共用单一 `v0.2.19-pk.50` 版本。模型/Fast 完整交接补丁 SHA256 为 `89ff326c4716d7b8f47121dd3b43e5c4dbfbe7838ab6743505405a1124a76c3b`，33 文件的源码与证据已核对；唯一原始源码交叉 `backend/internal/server/routes/admin.go` 同时保留模型候选与账号代理分配路由。根四文档合并增量，不覆盖既有约束。原始交付日志与随后修复证据分别保留。

本版新增兼容迁移 261、262、263。既有迁移不重写；应用回退继续读取首个旧主代理，旧版不具备新多代理路由及限额能力。观察模式不追算处罚，已完成账务及合法账号变更不因应用回退自动撤销。

## 已完成验证

- 最终前端构建：语言完整性 3 项、TypeScript 和 Vite 1203 模块通过，Vite 耗时 92 秒。构建前后 1038 个输入哈希相同；最终清单包含 1040 个源/工具输入和 199 个 dist 文件。日志 `.runtime/pk50-frontend-build-integrated-final.log`，构建与清单证据 `.runtime/pk50-frontend-build.json`。
- 生产依赖审计仍为 2 high、12 moderate、2 low、0 critical；沿用已有例外策略，策略校验通过。两会话均未变更 package/lock，不将原始 audit exit1 写成无漏洞。
- 工作树隔离 PostgreSQL：ordinary/partitioned 两种布局共 12 组通过，覆盖 261～263、观察证据/实际排行 SQL、多代理容量与回退等；原始证据保持 `release_evidence=false`，待最终提交按源码等价绑定。该记录不冒称正式二进制验收。
- 各专项模拟、已修复的阶段失败及其证据见对应文档。首次宽范围 Go 汇总失败保全于 `.runtime/pk50-final-go-tests-attempt1.log`，其中旧观察缓存预期与设置 fake 已修正；补修的 service/repository/admin 三包定向全部通过。
- 最终合并源码的八包 Go 定向检查全部通过，exit0：service 10.846s、repository 0.315s、securityaudit 0.342s、middleware 0.688s、handler 2.400s、admin 0.288s、DTO 0.214s、binancepay 0.928s。包含模型/Fast 两处增量、授权多代理和观察修正。日志 `.runtime/pk50-final-go-tests.log`；具体选择器和不执行真实可选服务的边界保存在 `.runtime/pk50-final-tests.ps1`。最终源码和实际日志统一绑定于 `.runtime/pk50-targeted-checks.json`。

## 正式发行状态

发行程序、同一二进制的 11 项制品检查、7 项升级回退、7 项多代理 API、7 项模型候选 API 及观察计数实际 SQL 的 5 项断言，按实际完成后补录。发布门禁要求全部证据和源码、程序、归档哈希绑定；失败报告保留，不将计划写为通过。

本轮只发布 GitHub Release，供用户后台手动更新/回退；不部署或重启生产，不发布 GHCR，不调用真实模型、账号授权或币安接口。运行时检查使用独立数据库、内部网络及合成数据，完成后清理本轮环境。

## pk.50 最终发行结果（2026-11-04，仅 main 补录）

本节替代上述本版本“尚未提交/发布、最终构建/制品待验收”等阶段状态，保留阶段失败、专项测试及验证边界。正式源码为 `080e6513dcdf08ecfc0f4d6f2eca5ddfd4b6f014`，tree 为 `54254ede44e9933e7ed7786a87d8218f5bc6cce7`，package ID 为 `1793726324-080e6513dcdf`。版本分支与正式标签 `v0.2.19-pk.50` 绑定此提交；此后只在 main 补录交付文档，不移动标签、不重制已封存制品。

私库 GitHub Release 已发布并验证为 latest：[v0.2.19-pk.50](https://github.com/yb1320057104/sub2api/releases/tag/v0.2.19-pk.50)。`.runtime/pk50-publication.json` 记录正式发布和九项资产 digest 核对；服务器更新仍由用户手动选择版本，本轮未部署或重启生产，未发布 GHCR，未发送真实模型、授权或币安业务请求。manifest 中的 GHCR image tag 仅是镜像归档内的名称，不能当作 registry 推送证据。Jenkins 未调用，使用本机构建和服务器隔离验收。

### 正式构建与定向检查

最终前端构建全量重新生成：i18n 3 项、TypeScript 检查通过，Vite 1203 模块、92 秒、199 个输出文件。生产源码冻结快照与构建产物哈希已绑定；前端依赖审计依然有 2 high、12 moderate、2 low、0 critical，原有例外门禁 exit0；`pnpm audit` 本身 exit1，不写成依赖零漏洞或开发工具审计已通过。

最终后端使用 Go 1.27.3，`-mod=readonly -tags embed -trimpath`，目标 linux/amd64、CGO_ENABLED=0；`go mod verify` 通过，嵌入 SQL 来自正式 Git blob，构建身份和实际二进制版本由隔离制品检查确认。最终二进制 SHA256 为 `1248b06618c05a61ac3fe51d08464c3072f9aa144a2318e44eac15d586274f38`。

`local-build.json` 保留 13 组定向检查记录及 176 个源码文件快照；这是已执行的检查组数，不是 176 个测试用例，也不把重叠专项测试相加当作唯一覆盖。最终统一 Go 检查的 service、repository、securityaudit、middleware、handler、admin handler、DTO、binancepay 共 8 包全部通过，日志 `.runtime/pk50-final-go-tests.log`，exit0。此前首次失败日志保存在 `.runtime/pk50-final-go-tests-attempt1.log`。文中原有前端审计、币安、多代理、授权、分组倍率/模型选择及临时授权 harness 的实测项目数和限制继续有效，临时 harness 已按维护要求移除；不冒称执行全部仓库回归。

### 同一制品的隔离验收

第三轮正式隔离 stage 的 `review.json`、`artifact-validation.json` 和 `isolated-smoke.json` 均为 passed，身份和二进制/归档哈希一致。实际结果分别为：

- 11 项制品检查：完整性、镜像来源/加载、二进制身份、非 root 运行、同 seed 保留、中断恢复、夹具准备、换 seed 替换、原镜像回退及清理全部通过。
- 7 项升级/回退检查：pk.49 在空库初始化、干净停止、pk.50 启动与健康/公开设置、兼容迁移、候选停止、同一 schema 上 pk.49 二进制回退、回退停止全部通过。保留原 317 项 migration checksum，新增 261/262/263 后总计 320 项；既有 schema、币安专属代理、API Key 有序分组及审计计数回退证据保持。
- 7 项真实隔离 API 检查：账号代理编辑/读取、次代理删除保护、容量失败原子回滚、成功分配及全局数量、重复请求稳定、批改和明确直连、夹具删除全部通过。代理地址为回环合成数据，不触发外部模型或账号授权。
- 7 项智商测试模型目录 API 检查：内置模型、已保存配置合并、本地账号/渠道候选合并、失效/删除/合成候选排除、非文本/通配候选排除、仅标识且只读、夹具删除全部通过。
- 5 项旧内容审计计数 SQL 断言通过：观察/后台不计数且严格拦截和 type4 保留、type4 可选排除、用户隔离、时间窗、封禁后重置。该查询从最终 `content_moderation_repo.go` 的 Git blob 抽取，在临时表事务中执行并回滚，持久日志未变；报告明确 `final_source_sql_only_not_compiled_query_execution`，不冒称通过编译程序的 repository 调用执行此查询。

独立 PostgreSQL 迁移夹具在 ordinary/partitioned 两种 usage 布局共 12 组通过，覆盖观察证据、排行榜真实 SQL、币安兼容列、多代理继承/容量/原子回滚/删除/到期恢复快照和旧 writer；合成金额 0.125×2=0.25 已核实。新增 `migrations-validation.json` 将原始已测 12 个源文件逐一绑定最终 commit/tree/blob，263 只允许 CRLF→LF 的 Git 标准化差异。它明确为 `tested_source_equivalence_only`、`artifact_validation_performed=false`，内嵌原报告仍为 `source_state=working-tree`、`release_evidence=false`；不改写原始证据，也不把 SQL 夹具当作全部 Go integration 方法或二进制验证。

### 失败保全、重建和清理

第一轮隔离启动在 migration checksum 检查失败：准备元数据使用 raw SHA 而迁移执行器使用 `strings.TrimSpace` 后内容，另有 Windows 工作树的 263 CRLF 被直接嵌入。修正的是制品准备/打包流程：校验全部已嵌入 SQL，使用正式 Git blob overlay 重建，并按执行器相同规则生成 migration checksum；没有重写旧迁移或修改产品源码。旧二进制 SHA256 `eea6b8e3d5b8be0a375bcc0c5a4b780ba0e1ac1129d36dbb8acbc13a71a8b74b` 不用于发行，首轮构建及四份失败报告保存在 `.runtime/pk50-build-attempt1/` 和 `.runtime/pk50-failed-stage-1/`。

第二轮已通过 11 项制品检查、320 项 migration checksum 和多代理 API 前三项，随后被夹具 URL 正则遗漏 `_` 拒绝，错误为 `multi_proxy_invalid_fixture_api_path`。仅修正 smoke 夹具字符范围，复用同一最终二进制，产品源码不变。第二轮原 state/metadata/smoke 保存在 `.runtime/pk50-stage-attempt2/`，原四份失败报告保存在 `.runtime/pk50-failed-stage-2/`；第三轮完整重跑通过。前两轮报告仍保持 failed，不以第三轮成功覆盖。

两轮失败的 inner smoke 均清理成功；随后按各自精确 DinD ID 和两个专属卷清理 outer 资源，独立 `cleanup.json` 均记录 passed、原报告哈希未变、host stage 和 release stage 证据保留。第三轮 `cleanup_passed=true`、`isolated_daemon_cleanup_passed=true`；多代理/模型 API 合成夹具均移除。独立 PostgreSQL 验证的 fixture 与 DinD 清理也通过。清理未使用 prune，未删除生产挂载或改动生产容器。

### 九项封存资产

下表来自最终 `.runtime/pk50-release/`；本地长度及 SHA256 已与正式发布资产逐项验证。`local-build.json` 中“远端验证待执行”属于本地构建当时的阶段声明，正式远端结果见独立的 `artifact-validation.json`、`isolated-smoke.json` 和 `review.json`，不回写原构建报告。

| 资产 | 字节数 | SHA256 |
| --- | ---: | --- |
| `sub2api_0.2.19-pk.50_linux_amd64.tar.gz` | 40348960 | `bf89aab051c1df331ff07226c2e9775950389fabce26a72eba0c077db545bbfc` |
| `sub2api_0.2.19-pk.50_linux_amd64.docker.tar.gz` | 50794104 | `6005dfcc46b634f54909f532cb936e0e9edec677adf018fbb6015a9558fce3fa` |
| `manifest.json` | 1282 | `99faa6d2453e4669cfb62efab16810a4281395dc6fea11b426858d9306a6d750` |
| `checksums.txt` | 106 | `f60a45c92c3e463e7ef4e6ecd859783107e3e3e88d9b309b11cecd9b89867786` |
| `artifact-validation.json` | 3797 | `607b6bc8f0fbca77db63a6ff7a5528eb14ac527eb89b2eabe478223d7b33d874` |
| `isolated-smoke.json` | 78432 | `c10f5d17f99bd5a48460c7411d691cfcdb4b5c3f7826d3a87c9f1ec5aeabf10b` |
| `local-build.json` | 191303 | `158db6c1a7611474366fa8056e4b8e02cd254a14792cba4fa284a7dcfba12e19` |
| `migrations-validation.json` | 10371 | `de20b3f344bc73fcc69f1facea4797e3206c75f2fef0722586aa3018e306dd67` |
| `review.json` | 1309 | `79d05bc720496d01d7f8e766718bb71b92b0179e2339822eb9ae3916735cb21d` |

最终镜像 ID 为 `sha256:b7ca1cb72f4d117a33912bde0bb848104d87a71f78c5c64e3fe305a4174ed789`；制品和证据保持原身份，生产切换不在本次自动执行范围。
