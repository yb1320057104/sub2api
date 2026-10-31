# v0.2.19-pk.31 测智分组旧ID静态核对

日期：2026-10-31。最新代码基线为用户更新后的私库main / v0.2.19-pk.30，提交098aebfaabc25658d4c32f447bd7b6d631a1ddc9；未从pk.27恢复覆盖。沿用PK-034/035/036/021，无新ID/表/迁移。

## 确认的原因

1. 糖果测试设置保存链正确写入pk_radar_account_config.target_group_id。工作台刷新后也显示当前不合格组。
2. 工作台旧代码只在第一次load初始化pass_group_ids。组删除后旧ID不再有勾选框，但仍在数组中；选新组可能提交“隐藏旧ID+可见新ID”，触发严格存在性校验。用户截图所示当前组可见不能证明提交数组不含旧值。
3. 重试接口直接反序列化旧任务的PassGroupIDs和FailGroupIDs，再一起校验。保存新不合格组不改旧任务JSON，所以重试仍可能校验已删除ID。
4. 无分组账号的立即/定时重授权取旧pk_authorization_config.group_ids作为fallback，但工作台实际及格选择在settings.pk_authorization_workbench.pass_group_ids；两套来源不一致。
5. 原生删除组会清理account_groups。主要遗留在前端数组和任务/配置JSON；本修复不改组删除业务或数据。

## 修复与边界

- 工作台load每次同步当前启用OpenAI组，剔除不可见旧ID并提示具体ID；保留有效选择、账号输入和其他参数。提交前再加载；若本次剔除旧ID或读取失败，则本次不POST，需要用户确认后再次点击。较早异步响应不能覆盖较新配置。
- 新重试任务使用当前糖果不合格目标，及格目标读取当前保存的workbench选择；重授权来源仍优先账号现有非删除组，没有时回退工作台。新任务reauthorization_retry元数据防止连续手动重试丢失原重授权分组语义；旧版重试按retry_of_job_id追溯，父ID严格递减避免环，保留历史不重写。原任务记录、模型快照、并发/优先级/出口保持。
- 立即与定时重授权使用相同及格fallback来源。不存在workbench记录/字段时才兼容旧值；明确保存空数组时不复活旧选择。无有效及格选择的手动动作明确拒绝。
- 目标验证仍按ID排序加共享锁，保留存在性、启用OpenAI类型及原生隐私/调度准入。缺失与停用错误显示具体ID。没有跳过失效组以伪造成功。
- 已排队/执行中的旧任务不批量改写，已完成历史结果不回填；修复作用于刷新后新提交/新重试及后续定时轮次。糖果不及格实际转组读取当前目标的规则不变。
- 用户在pk.28、pk.29、pk.30的全部浏览器更新保留，account_authorization_browser.go相对基线无变化。未改HTTP/代理授权、凭据加密、首字/计费、健康度或部署流程。

## 核对与交接

仅执行gofmt、源码/SQL/调用链与git diff --check、Vue SFC与TypeScript/locale源码静态解析；不是构建、类型检查或动态测试。未生成测试文件，未进行本地或服务器构建/测试、真实账号登录/模型请求、生产数据读取/写入、Jenkins或部署。

按既定授权交付私库main与正式标签v0.2.19-pk.31，发布前检查未占用，推送后核对main/tag剥离commit及祖先关系，旧标签不动。Jenkins由用户选择ACTION=release、SOURCE_REF=v0.2.19-pk.31（不带origin/），COMMIT_SHA/PACKAGE_ID留空。真实环境结果仍待用户构建更新后验证。
