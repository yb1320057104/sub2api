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
