# pk.67 HA 余额缓存未命中 503 修复

## 上线后的未解决问题

北京时间 2026-11-15 22:30:06–22:33:23 的真实流量中仍有 7 条 `balance_read/cache_miss`，单次 212–307ms。pk.67 的有限补读已运行，但不能保证缓存得到回填，本修复尚不充分。代码复核发现持久记账成功后每次都会删除余额缓存，资格检查命中后与并发结算交错时，预约的纯读取可以持续 miss，且该请求没有待执行的回填。该竞争可从调用链解释，现场日志未记录逐次 GET/DEL，不能声称每一条失败均已逐操作确证。继续修复须保留计费原子性与失败拒绝，不能通过增加等待或旧快照无条件写回规避。详见 `PK_DOWNSTREAM_503_PK67_REVIEW.md`。

## 范围与依据

2026-11-15，用户要求修复下游调用频繁出现的 503。主任务只读生产调查确认：两小时窗口中 41 条计费拒绝均为 `billing.inflight_unavailable`、`stage=balance_read`、`cause=cache_miss`，不是 Redis 网络超时或计费断路器。六小时窗口的 220 条计费 503 来自三个普通用户，均无自动授信配置。

源码中，普通余额资格检查在 Redis 未命中后查询数据库并异步回填，HA 在途预留随后直接读取 Redis。回填尚未完成时，旧实现立即拒绝同一请求。此处仅修复这个已确认的时序缺口；其他路由、账号或上游 503 由独立变更处理。

沿用 PK-047 的 HA 共享状态保护及 PK-021 的发行约束，无新增功能 ID、配置项、依赖、迁移或财务计算。

## 实现与保护边界

新建独立 `deploy/cluster-updater/overlays/pk67/` profile，继承 pk.66 的 96 个 HA 路径，旧 pk.65/pk.66 profile 和封存制品不变。相对 pk.66，只改变以下两个已存在的 HA 路径：

- `backend/internal/service/billing_inflight_reservation.go`
- `backend/internal/service/ha_billing_concurrency_test.go`

首次 Redis 读取保持原调用上下文和期限。仅收到 `redis.Nil` 后，允许最多两次额外读取，每次先等待 20ms，共享一个额外 250ms 总预算。命中时立即继续；持续未命中、非未命中错误、取消和无效数值仍返回原计费 503，并仅记录最终失败诊断。

补读不查询数据库、不发布余额、不触发新的回填。现有 Redis 原子预留、金额、预留标识、续期、记账交接和释放逻辑完全不变；不会重试含写入的预留 Lua 或上游请求。普通非 HA 路径仍调用原 `GetUserBalance`。

自动授信用户使用数据库权威余额且不会执行普通余额缓存回填，存在独立的 HA 兼容边界。本次现场计费失败用户未命中该条件，因此未扩展本次修复或修改自动授信路径。持久性缓存缺失仍会拒绝请求，本修复不代表所有 503 已消失。

## 验证记录

测试基于初始不可变源码 `3d6987725c386d1978818702305f3468c9c8e1c3` 的本机独立导出，使用 Go `1.27.3 windows/amd64`，无生产数据库、Redis、模型调用或账号凭据。扩展既有 HA 测试文件，没有新增独立测试文件。

1. 新 profile 实际应用到独立候选文件树；全部 96 个路径逐文件匹配 manifest 的输出 SHA256。相对前一 profile 只有上述两个输出哈希变化，输入哈希保持。
2. 首轮计费 HA 定向检查：10 个顶层测试、28 个包含子项的通过事件，无失败或跳过。
3. 有效负对照：保留同一回归用例，仅换回原 `inflightUserBalance` 函数，`TestSharedStateInflightWaitsForEligibilityAsyncBalanceFill` 实际失败并返回旧计费不可用错误。还原新函数后该用例通过。
4. 最终定向检查：30 个顶层测试、50 个包含子项的通过事件，无失败或跳过，进程退出 0。范围包括 HA 共享状态拒绝、固定费用失败清理、普通预留、预留续期/交接、估价及同步扣减。
5. 新覆盖包括真实资格检查返回后异步回填仍被阻塞的交错、一次/两次未命中恢复、持续未命中最多三次读取、错误后立即拒绝、取消、被阻塞补读的期限，以及两个服务争抢同一余额仍只能放行足额预留的一个请求。原有丢失预留响应只执行一次并清理的断言保留且通过。
6. 补充执行 repository 的 13 个既有在途预留/范围预留/固定费用测试：13 个顶层测试全部通过，无失败或跳过，进程退出 0。测试使用 miniredis 执行现有 repository Lua 路径，覆盖并发限额、首次请求旧语义、过期清理、禁用/订阅/零估价路径、故障和脚本错误、顺序记账窗口、续期不复活过期成员、范围预留释放及 API Key 固定费用额度。该结果补充了服务 stub 检查，仍不等同于真实 Redis 多节点验收。

最终检查命令：

```text
go test -json -tags=unit ./internal/service -run '^(TestSharedState|TestReserveInflightBalance|TestInflightReservation|TestEstimateInflightReservationCost|TestInflightEstimate|TestSyncBalanceCacheAfterDeduction)' -count=1
go test -json -tags=unit ./internal/repository -run '^(TestInflightReservation_|TestInflightScopeReservation_|TestReserveFixedCharge_)' -count=1
```

本轮完整日志与结果保存在本机受限、Git 忽略的 `.runtime/pk67-billing-test/`。最终通过日志 `verified-positive-final.jsonl` 的 SHA256 为 `09af8484bb6f9a864e455be129d2e3f1ed59cab49a99742a012e6e83cb53f817`；有效负对照日志的 SHA256 为 `a51843668c9f9c57cf7b95b255a1a0d13ad679da94a016499b1ff26fd1c06a4d`。

补充 repository 日志 `repository-lua.jsonl` 的 SHA256 为 `e4ef370c0a227ecde47e2b92277744ccf062fe32720d02838a8c551ab49086be`，统计保存在 `repository-verification.json`。两组最终定向检查合计 43 个顶层测试、63 个包含子项的通过事件。

验证辅助脚本发生过两次独立问题，原记录均保留：首次负对照整文件替换移除了新测试引用的常量，导致编译失败，不能当作有效负对照；随后改为只替换目标函数完成有效复现。一次通过后的报告汇总将包级事件的空测试名相加导致脚本异常；修复统计后重新运行最终检查，取得上述退出 0 的结果。未运行完整源码套件、竞态检测、真实 Redis 多节点或同制品生命周期验收。

## 交接

本轮 profile 补丁 SHA256 为 `dfbc70d1699e5310693cb923551984a0b77ad8eb6d8cca222eb4e815c3673677`。profile 使用独立 `.gitattributes` 固定补丁字节；父 profile 哈希取自 pk.66 的 Git blob，避免本机 JSON 换行差异影响来源标识。

最终主线的额外账号拒绝修复需由主任务合并验证。本轮核对其四个业务路径及三个测试路径，与 profile 的 96 个路径没有重叠；未改写这些主线文件。若后续修改任何被列入 profile 的候选文件，仍需重新核对输入/输出哈希后使用既有 `prepare_candidate_overlay.py` 准备候选。

以上记录为源码阶段证据。后续于 2026-11-15 完成不可变 `v0.2.19-pk.67` 正式发布及生产逐节点更新；北京时间 22:31:07 独立终验 15 项通过，HA 版本 `0.2.19-pk.67-ha.20261115.1794750667`、代次 19。新旧程序 7 请求混合计费、HA 592 个顶层/1598 个通过事件、9 项生命周期及普通/HA BPS 各 15 项通过。完整交付和真实流量观察见 `PK_DOWNSTREAM_503_PK67_REVIEW.md`；不将隔离用例或健康检查等同于生产所有 503 已消失。
