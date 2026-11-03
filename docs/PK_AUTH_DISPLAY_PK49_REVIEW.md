# pk.49 授权显示分配与回收修复

## 基线与授权范围

基于私有`yb1320057104/sub2api` main `f8dc044d5c8fbc295d685cfdfb0df8356f41e00d`及pk.48正式提交`061d058900d99eacccfe9ad8eece8030f736b36f`。正式工作分支/标签为唯一`v0.2.19-pk.49`；已核对私库、Release未占用、开放PR为空、远端main未漂移。

用户报“后台 Chrome 会话需要图形环境（Xvfb 或 DISPLAY），未提交账号密码”，沿用用户只发布GitHub、自行切换生产的要求。允许本地修复、定向模拟及隔离制品验收；不部署/重启生产、不执行真实登录、授权、模型或币安请求，不跑全套源码回归。此显示修复沿用PK-016/021/036；同版用户追加的审计分级/前端/账务迁移范围另见PK_AUDIT_RISK_PK49_REVIEW.md，以下仅记录显示专项。

## 生产只读证据与根因

`.runtime/authorization-display-diagnostic.json`确认生产已由用户切换pk.48且healthy，Alpine3.21.9，应用用户可找到Chromium/Xvfb，`/usr/bin/Xvfb`可执行，`/tmp`可写，DISPLAY未设置，bootstrap=true、入口包含bootstrap，无安装失败记录。`:90～:99`十个锁均指向已不存在的进程，实际Xvfb进程数0。

旧helper只在90～99扫描锁文件存在性，清理时直接Kill+Wait。Xvfb无机会正常移除锁，残锁占满后返回“缺少图形环境”。此外先检查再Start不具备原子性，多任务可争用同号；固定睡700ms也不能证明Xvfb已经就绪。这次已证实为残锁耗尽，不能套用“重新安装Xvfb”诊断。

只读脚本首轮误读入口路径而失败，修正为仓库实际`/app/docker-entrypoint.sh`后成功；没有安装软件、删除显示锁、启动登录或改变生产环境。

## 修复边界

Linux后台且未设置DISPLAY时，使用Xvfb `-displayfd`自动选择可用显示号并经管道取得就绪通知；绑定context取消及有限启动时间。正常回收优先TERM等待退出，超时再Kill；仅一个Wait负责回收并持有应用reaper子进程守护，不抢其它任务退出状态。缺依赖、启动失败和就绪超时使用固定安全分类，不输出原始子进程日志或凭据。

既有DISPLAY、无头模式、非Linux保持原行为；不清除他人/旧显示锁，不改浏览器身份、CF流程、代理、密码提交保护、授权并发、准入、测智或定时调度。Xvfb只作为外置系统依赖，不打进二进制/发行镜像。

## 验证状态

Windows兼容编译exit0（no tests to run，不是Linux行为测试）；隔离Alpine3.21的8个顶层显示测试全部PASS、无skip，12个并发真实Xvfb以uid1000运行，network=none、无host mounts，10个root-owned旧90～99锁hash保持，新锁/socket/进程均0，容器及镜像清理PASS。attempt1仅SFTP准备写入模式失败，修正wx后attempt2成功。证据.runtime/pk49-display-checks.json及attempt2-test.log，测试二进制SHA256 cf54989d2586230589425c20520de1f18fc875f1eeaacef4a59e1627c94bfd6c，绑定4个显示相关源码hash；全树3519输入快照保留为当时编译记录。完整发行构建与同制品11+7尚待最终范围完成。Linux专项仅创建测试显示进程与合成锁，不启动Chrome或外部登录；旧锁哈希保持、并发显示唯一、正常清理和取消/超时为关注边界。

原仅显示修复时准备的前端复用证据已作为阶段记录保留；用户追加审计UI后最终版必须重新构建并冻结前端，不能复用pk.48产物。正式发布保留同一二进制、私库main/标签归属及最终完整资产哈希证明，不推GHCR、不自动切换生产。
