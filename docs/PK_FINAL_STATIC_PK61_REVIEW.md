# pk.61 最终全局静态复核

2026-11-11，本记录对应固定私库 `6ed612909fef4510f63d93a8f2ead58bae35a3c8` 和固定上游 `d42bb4fc4b3eef9821994b4ad170d4eb881eca95`。仅记录源码完整性、声明、依赖及定向证据汇总；发行、同制品验收、生产迁移和容量结论必须另有实际回执。

## 输入与声明

对当前 tracked/untracked 非忽略文件重新扫描，快照为 8145 个文件，冲突分隔符候选为零；未把 Git index 尚未暂存的冲突状态当作工作区文本冲突。Go AST 分别按 `linux/amd64` 与 `windows/amd64`、`unit` 标签检查，包含生成 Ent、测试和第三方嵌套 module，分别解析 4974/4972 个文件，解析错误、重复声明及重复 import 均为零。这是静态检查，不是全套编译或测试。

重新运行声明清点：上游 23407、私库 18506、当前 24860。缺失旧私库名从 34 降到 27，七个历史保护声明已恢复；其余 27 项与上游三个声明均有明确替代链，见 `PK_DECLARATION_COMPAT_PK61_REVIEW.md`。清点没有用同名来证明语义等价。

原始证据为 `.runtime/pk61/postmerge-symbol-review.json`、`postmerge-symbol-final.log`、`all-declarations-final.json` 与 `final-static-inventory.json`。脚本采用 NUL 分隔的 Git 路径读取，避免中文文件名转义误报丢失。

## 上游新文件和测试来源

重新逐路径比较后，固定上游新增文件在当前工作区缺失 84 个，均有明确处置：

- 77 个 GitHub Actions workflow：沿私库构建/发行约束禁用；其业务源和检查脚本仍按任务清单保留。
- 两张友链图片：沿已授权的私库品牌移除，不恢复友链。
- 五个新增防蒸馏测试文件：测试已并入现有对应文件，避免同包重复声明。逐文件分别含 1、6、1、5、11 个顶层测试名，当前均找到承接位置，新增 `UsageUnavailable`/流读取不完整场景已并入既有 usage integrity 回归。出处表为 `duplicate-test-source-provenance.json`。

除上述处置，没有发现上游新增业务源文件遗漏；上游与私库共同存在的文件没有缺失。此结果证明路径完整性，不声称全部业务行为已逐个动态测试。

首次清点识别六份本轮原创测试：BEpusdt 的 `shared_test.go`/`shared_integration_test.go`，更新器的 `test_candidate_overlay.py`/`test_migration_compatibility.py`/`test_worker_shutdown_budget.py`，以及临时 `pk61_schema_compat_validation_test.go`。来源清单先交主集成，扫描者未自行删除仍在使用的测试。主集成处理后再次扫描，本轮原创测试文件剩余零；固定上游原有新增测试与既有回归不属于清理对象。最终清单见 `final-static-inventory.json` 的 tests 分类，真实执行日志保留。

## 64 个功能域与交叉证据

`PK_UPSTREAM_INTEGRATION_PK61_REVIEW.md` 包含全部 64 个域的当前源码处置与未覆盖边界。`final-domain-evidence.json` 为每一个 R01～R64 关联合同、源码结论、文件数和实际日志，64 行齐全，所有引用日志存在并保存 SHA256。

账务域关联原始 usage/quote/耐久事务及仓储验证；协议域关联首字/终态、Messages/Key/Kilo、reasoning-only 和 handler/server；账号质量域关联单 worker、5xx、糖果排空、共享错误率与权限；HA/迁移域关联角色、退出、激活门及旧迁移哈希；前端域关联最终 65 文件/1551 项结果、构建和路由 AST。这个索引保留各轮的真实失败/修复/复验状态，不把跨轮重复用例累计成“独立总数”，也不把某个域引用日志等同该域全部功能都已执行。

新增协议/后台能力的默认关闭、实际组权限、原始计费和首字三模式沿对应专项。实验、外置浏览器/链请求、真实支付和生产 3000 并发等未做动态验证的边界继续明确保留。

## Go replacement 与 BEpusdt 来源

`backend/go.mod` 的 Gin `v1.9.1`、validator `v10.14.0` replacement 均存在对应 `go.sum` 项，不能用 require 行的较新名义版本代替实际编入版本。两处本地 replacement 目录及各自 `go.mod` 存在。

BEpusdt 本地模块含 67 个文件、63 个 Go 文件，嵌入 wrapper 与模块源引用的 11 个内部包路径全部存在且有 Go 源；未发现对遗漏的上游管理员 web/router 资源的运行时引用。`THIRD_PARTY_NOTICES.md` 与 `README.sub2api.md` 标明固定来源 `aa3bd5097f25`、本地维护位置、对应源码和构建/测试命令。模块 `LICENSE` 与 `LICENSES/BEpusdt-GPL-3.0.txt` 统一 LF 后逐字节一致，SHA256 为 `3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986`。实际发行仍须携带完整对应源码和这些许可证，不以本次静态检查代替制品清点。

`glebarez-go-sqlite` 本地模块是两份文件的薄适配，直接别名复用 modernc 的 Error/Driver，不是删减缺文件的 SQLite 实现。前端 package manifest 没有失效的 `file:`/`link:` 本地依赖。pnpm 锁中 postcss specifier 使用已登记安全 override `>=8.5.18`，不应按 package 原 `^8.4.32` 文本不同误报。

## 前端实际审计与修补尝试

官方 npm 审计文件 `frontend-final-audit-npm.json`：0 critical、0 high、12 moderate、2 low。最初配置的镜像源没有 audit endpoint，失败文件 `frontend-final-audit.json` 原样保留，不能视为审计通过。

未修问题来自间接依赖：yaml 1.10.2、uuid 11.1.0/13.0.0、mermaid 11.14.0、colord 2.9.3、KaTeX 0.16.45。旧异常名单中的 xlsx、lodash/lodash-es、axios、nanoid 与这些公告不匹配，部分旧项已过期；本轮未借用、延期或扩大例外。实际运行现有高危门禁 `tools/check_pnpm_audit_exceptions.py` exit 0，因为本轮报告没有 high/critical。

尝试只为 yaml/uuid/mermaid/colord 加兼容版本 override；先遇缓存权限问题，按规则在沙箱外重试，随后网络连接中断，离线官方缓存缺 `@dnd-kit/core`。未得到可验证新锁，按主集成收束指示撤销本轮五条 override，不修改任何已有上游依赖或例外。完整失败/中止日志保存在 `frontend-audit-remediation-*`，不把尝试写成漏洞修复成功。

撤销后 `frontend/package.json` SHA256 为 `603f16dd43f25e51ece5806cf7df476c6d47d9ad4532f12c1c671db495b8fb6c`，`frontend/pnpm-lock.yaml` 为 `80fe363f46758d898ffaa18039801e1b3e19ca878c11c5d16bc02a89efa2149d`，均逐字节匹配已成功构建的 `frontend-final-source-sha256.json`。尝试只执行 lockfile-only，未改 node_modules，不冒称重新构建或风险清零。
