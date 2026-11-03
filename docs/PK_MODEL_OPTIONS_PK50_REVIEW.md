# pk.50 模型候选与档位补齐

## 基线与协作范围

基于私库 `yb1320057104/sub2api` main `da1ec74e236cef072a97e1c545a796c686d49762`（pk.49 最终补录）。用户要求参考 `jlcodes99/cockpit-tools` 补齐档位及智商测试模型，并与会话 `01a166d4-5cfd-7712-a333-e3ae35c66445` 的改动合为唯一 `v0.2.19-pk.50` 发行。本部分扩展 PK-028/032，沿用 PK-021/031，不新增独立功能 ID 或数据库迁移。

本工作区只生成可校验的完整补丁，不移动 main、建立第二个版本分支或单独发布。集成会话统一合并根目录四份约束/登记文档，重新构建前端和发行程序，完成同制品、升级回退、接口与隔离数据库验证后发布私库 GitHub Release；生产由用户手动切换。

参考项目源码固定于 `3edfff631a57244ebafde75dc6f7d02ffb6ad7ba`，其界面档位不代表所有上游模型都支持相同参数。未通过真实模型调用核实可用性。

## 最终行为

### 推理档位

OpenAI/composite 分组上限及映射目标增加 `ultra`、`persistent`，完整管理顺序为 `minimal < low < medium < high < xhigh < max < ultra < persistent`；映射来源另保留 `none`。`none` 不作为上限或强制映射目标。Anthropic 分组保持 `low/medium/high/xhigh/max`；composite 转 Anthropic 时将 `minimal` 兼容为 `low`，将 `ultra/persistent` 兼容为 `max`。

该顺序用于本地策略限制，不声明上游独立能力。公共计费归一化函数保持原合同，组策略使用独立归一化，避免把扩展档位引入普通账号计费。Kilo 继续将 `ultra/persistent` 转为上游 `max`，原始请求强度、客户倍率与实际转发强度的既有区分保持。

Kilo 显式原始档位规则优先；未匹配显式 `ultra/persistent` 规则时保留旧 `max` 规则兼容。映射后检查有效上限，避免旧 `max→low` 与 `high/deny` 配置被提前拒绝。由其他档位映射得到的 `ultra/persistent` 不再链式执行旧规则。各入口与普通账号的最终执行边界以定向测试及后述集成结果为准。

### 请求速度

现有 fast/flex 策略增加 `force_ultrafast` 动作，并贯通设置校验、Responses map 转发、HTTP/Chat 共用处理和 WebSocket 帧处理。保持 `pass/filter/block/force_priority` 和缺省行为。省略档位仅由专用 `missing` 条件强制，不改变旧 `all` 的匹配范围。分组原强制 Fast 开关仍先写 `priority`，全局规则可进一步处理。

界面提供强制 ultrafast 及中英文说明。速度档位与推理强度独立；ultrafast 沿用现有独立定价和降档规则，不改价格、倍率、费用公式或首字计时。

### 智商测试

新增完整管理员只读 `GET /admin/intelligence-test/models`，合并现有多平台内置文本模型、账号映射、已保存 Grok 观测目录、活动渠道映射/定价模型及已保存的 SVG/糖果题默认和分组覆盖值。返回 `data.models` 与 `data.excluded_models` 两个名称数组；前端继续合并既有完整静态目录（含旧 Claude、Llama、Mistral 等），统一应用媒体排除元数据，防止静态目录把同名媒体别名重新加入。只投影模型名称和目标，不输出凭据、地址或配置正文；不刷新上游目录、不启动测试、不自动保存。

候选去重排序并排除通配符、非法值、专用媒体模型及媒体别名；仅筛选建议，不重写或禁止管理员已保存的自定义 ID。模型可用性仍取决于具体分组和模型。读取失败返回固定安全错误，前端保留草稿、已有候选和自定义输入。

SVG/糖果题默认模型及分组覆盖均改为可搜索、可创建自定义值的选择器。候选异步更新只修改选项，不覆盖编辑内容；卸载取消读取。推理选项保留 `模型默认/继承` 及 `none/minimal/low/medium/high/xhigh/max/ultra/persistent`，补齐中英文标签，提交原始协议值。定时调度、测试 Key、保存开关、模型请求路径及测试账务不变。

## 验证记录

冻结后的 Go service/handler 两包定向模拟全部通过（exit0，分别 0.177s/0.157s），日志 `.runtime/pk50-models-backend-final.log`。选择集涵盖组强度校验/映射、Kilo 四入口旧规则兼容、普通账号待判定策略在发送前执行、原计费规范化、Fast HTTP/Chat/WS、独立速度计费及 IQ 候选/管理员权限/固定错误。此前速度单独验证也通过，但不累计为独立覆盖。

前端最终 7 文件共 40 项通过；SettingsView 仅运行新增强制 ultrafast 保存用例，其余 59 项按名称过滤跳过，不冒称整个 SettingsView 已回归。`vue-tsc --noEmit` 通过。日志分别为 `.runtime/pk50-models-policies-frontend-final.log`（27项）、`pk50-models-intelligence-frontend-attempt1.log`（9项）、`pk50-models-i18n-final.log`（3项）、`pk50-models-settings-frontend-final2.log`（1项）和 `pk50-models-frontend-typecheck.log`。最后设置用例断言无 XHR 请求、无错误提示；首次用例发现隐藏设置组件缺少 API 模拟，修正测试后复跑，原日志保留。原有 Browserslist/router-link 测试警告未屏蔽。

首次前端命令因原 node_modules 缺少 `pathe` 未启动；依赖按既有 frozen lockfile 恢复，package.json/pnpm-lock.yaml 均未修改。旧不完整依赖目录保留在 runtime。并行源码编辑时的早期 Go 命令出现 handler/service 编译快照不一致，保留失败日志；仅上述全源冻结后的组合命令作为最终后端证据。`git diff --check` 通过。

候选数据库单元测试使用 sqlmock；真实 SQL/schema/API 验证由集成会话在隔离 PostgreSQL 另列 IQ 检查。本工作区不将其写为已完成，也不将本机定向测试冒称发行同制品验收。

未执行全套源码回归或真实模型、授权、币安请求，未部署/重启生产或发布 GHCR。正式发行状态由集成会话在最终发行记录补录。

完整补丁、基线、源文件 SHA256 和定向命令保存在 `.runtime/pk50-models-handoff/`。最终合并改动后必须重新构建前端；此前构建仅为历史阶段证据。

## 集成审查阶段及后续修正

2026-11-04，集成会话对上述冻结交接源与本轮多代理转发增量进行了只读静态审查，发现以下两处可达入口缺口。已将问题交回原协作会话修复，正式构建暂缓；本段不修改或替代原 handoff 的源哈希及定向测试证据，也不将那些已通过的测试扩称为覆盖了下列路径。

1. `openai_gateway_messages_chat_fallback.go` 的 Messages → Chat Completions 转换明确跳过 Fast 策略。因此合法的 `missing → force_ultrafast` 设置不生效；既有 `force_priority` 也受同一缺口影响。应在转换后应用共用策略，并按最终出站值读取计费档位。现有新增转发测试覆盖 Responses/Chat 入口，未覆盖这个 Messages 转换分支。
2. Kilo Messages 内部重试使用已经改写过强度的请求体。首次执行 `ultra → high` 后，`openai_gateway_messages.go` 在 `previous_response_id` 失效或不受支持时，递归传入 `high`；第二次执行不再识别原始别名，随后普通策略会再次映射。例如同时配置 `ultra → high` 和 `high → deny`，同一请求会由首轮放行变为重试本地 403。API Key Kilo 账号使用 `gpt-6-astra` 等兼容模型时可启用此 continuation 路径；历史 `max` 别名映射链也可能受影响。修复需保留原请求或明确的本轮策略处理状态，并验证内部重试不链式应用第二条映射。

其余审查范围内未发现计费归一化、费用公式或首字计时位置的新变更。多代理路由由原请求 context 派生 HTTP/plugin/WS context，保留强度策略、客户端模型与原始请求强度元数据，未发现与本增量的直接上下文冲突。该阶段只读核对未修改产品源码、运行测试、发起真实请求或操作远端。

随后已合入独立增量 `c1fb1d6b2216e6e8d1dd657b58b83a0e5117cb38c4f44dddca549327c119eb98`，两个入口均已修正，原交付证据保持。5 文件基线/最终源码和测试日志哈希已核对；新增 Fast 32、Kilo 22 个场景及相关旧路径共 232 个叶子场景通过，0 跳过。完整说明见 `PK_MODEL_OPTIONS_PK50_FOLLOWUP.md`。本集成工作区随后执行最终统一验证和发行门禁，不能以协作工作区的单包测试替代最终制品结果。
