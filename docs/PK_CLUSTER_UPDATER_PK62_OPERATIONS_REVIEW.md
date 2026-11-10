# pk.62 独立更新器维护复核

应用正式源码、版本分支与标签保持 `fdec871f36b965dfc272b46bd12eca76ba448300`；本记录中的 Python 工具维护在 main 独立提交，不重编应用，不覆盖 pk.61/pk.62 或旧 r4 归档。

## 两处实际问题及修复

更新器迁移准入原先要求所有证明的 baseline_commit 等于旧 r4 的 pk.58。实际当前生产与本轮正式回退基线为 pk.59，不能将实际执行结果改写成 pk.58。新增私有操作配置 migration_compatibility_baselines，按候选 commit 严格固定 baseline_commit、overlay_manifest_sha256、adaptation_profile_sha256；同时检查 overlay 的候选提交。证明文件本身不能选择基线；没有显式 pin 的旧配置仍要求 pk.58。完整原迁移映射、旧 SQL 字节、三场景检查及受限证据文件规则保持。

API replace RPC 原 1200 秒小于实际最坏正常路径与回退预算：前向排空 900 秒、TERM 后等待 120 秒、ready 60 秒；失败回退还可能使用 180+120+60 秒。父进程超时会终止本地 agent，可能中断回退。RPC 提升为 2100 秒，worker RPC 原 2100 秒保持。本修改不将 API TERM 等待声称为 900 秒，也不添加 Docker stop、SIGKILL 或任何强杀。

旧 worker supervisor 配置仍为 45 秒时，超时只会退出为 worker_grace_expired_no_force_kill；新 agent 继续按真实容器 Running=false、ExitCode=0 与 supervisor 状态等待，不能把 supervisor 提前退出当作后台业务已排空。

## 真实验证与边界

最小补丁独立审阅通过。Linux 隔离运行 20 项定向检查通过，覆盖旧默认、没有配置时拒绝自报新基线、正确三项 pin、错误候选/overlay/profile、旧 SQL 不可改和受限证据路径/权限/符号链接。临时测试源码不进入交付，日志保存在本轮私有运维材料中。

已核实际旧边缘更新逻辑在 gate active=0、HTTP 在途=0、已接收 socket=0、started_http 稳定后才发 TERM，并以真实退出 0 确认计费收尾。若旧进程收到 TERM 后长期不 ready 且仍在清理，安全行为是保持该节点摘流并停止后续节点；不能宣称所有失败均自动恢复该节点流量。实际发布、工具安装、节点排空与恢复结果必须另按本次现场回执确认。
