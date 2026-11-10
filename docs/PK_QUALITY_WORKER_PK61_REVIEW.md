# pk.61 质量测试 worker 兼容核对

本记录对应私库基线 `6ed612909fef4510f63d93a8f2ead58bae35a3c8` 与上游固定提交 `d42bb4fc4b3eef9821994b4ad170d4eb881eca95` 的合并适配，沿用 PK-019/021/034/035/047。它记录源码审查和实际局部验证，不代表发行或生产验收完成。

## 跨节点信号与单 worker

固定上游没有进程内 `QualityTracker`。真实转发的 `RateLimitService.observeQualityResponse` 经过 `quality5xxTrigger.Observe` 写入共享 Redis 有序集合 `quality:5xx:pending`，由质量 worker 消费；API 与 worker 使用独立的小连接池。信号只含账号 ID 和观察时间，不复制上游内容。已有机制能跨 API 节点传递信号，无需另建进程内样本同步。

后台启动必须同时满足 HA 角色 `RunsBackgroundJobs()` 和 writer 激活许可；API 节点保留信号生产者，只有获得许可的一个 worker 启动定时器与消费者。源码共享 provider 已使用同时检查角色和激活许可的 helper，cleanup 已将 runner 排空置于依赖关闭前；最终制品仍需逐项核对。

## 退出风险与源码修复

旧 runner 的启动延时不可取消，运行上下文独立于 Stop；Stop 只等待 cron 三秒，可能让旧测试继续运行而依赖已关闭，或在切换时被容器默认超时强杀。

新增 `qualityRunnerLifecycle` 将停止分为关闭准入和等待已接纳任务。Stop 与 Start 串行，停止后不再接收计划，延时与等待测试槽位可取消；已开始的测试沿原执行期限完成结果保存。触发消费者先停止派发，再等待在途任务，未完成信号不确认删除，最后关闭自己的 Redis 池。HTTP 排空后才可关闭独立的信号生产者池。

二审还修复分组测试与渠道糖果的队列：原分组循环在槽位满时直接阻塞，且每条任务重新开始十分钟预算，退出可能远超单批预算。现在 runner 向两个服务显式传入独立准入上下文；停止后未发任务不再 claim，已发任务继续保存，手动入口保持自己的生命周期。回归分别以九个待测计划、四个已发探测验证停止后只保存这四项，余下五项留给下一 worker。

执行预算为单探测最多十分钟、批次或触发任务最多十二分钟；旧式测试结果保存另有三十秒独立预算。worker 的 supervisor/systemd/容器正常退出等待须至少覆盖十二分三十秒，采用十五分钟或更长的等待，并确认旧 worker 正常退出后才启动新 worker。不能使用 Docker 默认十秒退出或强杀来伪造交接成功。

## 发送兼容

保留 PK 糖果发送前最多三次 TLS 握手尝试，单次探测只解析一次可选 TLS profile；HTTP 响应、已发送证据和未知插件错误不引入重放。普通连接测试仍只发送一次。新 ticket 路径继续携带最终模型、完整 Cookie/出口绑定、严格响应校验与 body 生命周期；糖果上下文禁止在内层再启动网关代理回退链，出口轮换仍由既有糖果控制器负责。

上游把真实插件调用移入 `doOpenAIProxyAttempt`，适配后该位置及账号测试共用插件租约包装：仅选中插件时获取原有全局代理槽位，失败立即释放，响应 body 关闭后释放。未选中插件不额外占槽。管理员显式探测没有普通网关代理路由上下文，保持指定出口。

## 已执行与待验证

- 逐字节复制 production 生命周期 helper，抽取原测试文件中的三个生命周期用例在本机隔离编译执行，三项通过：停止准入仍等待已发结果、Stop-before-Start 不再接纳任务、停止取消槽位等待。临时测试文件已删除。
- helper SHA256：`145defa246654d71dd25605674f0d18f55c735b4ae7ad5675306a509e7f0ab5d`；执行记录保存在 Git 忽略的 `.runtime/pk61/quality-lifecycle-check.log`。
- 已解决本轮 scheduler/ratelimit 十一份文件的二十八处冲突，以及插件传输六处冲突；所属文件 gofmt、diff-check 与 Go parser/import 静态核对完成。保留代理优先级分区、容量、实际组模型准入及暂停规则，接入上游 sticky CAS、RPM、ticket 和更严格限流快照校验。
- service 单元测试编译已实际通过；定向 recovery 运行中 116 个顶层测试通过，另一个“新 5xx 晚于刚完成计划”测试因 Windows 两次 `time.Now()` 同 tick 而没有形成先后顺序，改为显式相差一秒后单项复验通过。包括完整 runner 生命周期、分组/糖果排空、共享 5xx、ticket/plugin、PK52 重试、图片按张、GPT61Sol 订阅/500k、分组保护与角色约束。记录为 `.runtime/pk61/service-unit-compile-attempt5.log`、`service-recovery-targeted-attempt2.jsonl`、`quality-recent-plan-retry2.jsonl`，没有以 helper 通过冒充整个 service 测试套件通过。
- Kilo、用户质量只读投影、插件凭据剥离和其余安全契约有 51 个顶层测试通过；上游新分类测试缺少 PK 要求的显式组配置，补齐测试输入与配置的组 7 后，新分类和豁免矩阵 11 个顶层测试全部通过。记录为 `service-contracts-targeted-attempt1.jsonl`、`security-category-retry1.jsonl`；失败原日志保留。
- 未发真实模型/账号授权请求，未在本记录阶段构建发行程序、发布或部署；HA 逐节点退出预算、启动许可、单 worker 及生产五分钟任务连续性需由最终制品与运维回执证明。
