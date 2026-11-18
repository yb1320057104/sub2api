# Kilo 六阶段纯 HTTP 执行器

本目录是 Go 控制面的短期执行器，service `1.1.0`、protocol `1`。Go 保存权威任务、执行代次、来源身份与 Gateway Key；执行器在短期会话中运行 `login`、`resume_identity`、`connect_subscription`、`reconcile_subscription`、`reauthorize_subscription`、`approve_device`。它不领取 OpenAI reauth/quota 任务，不替换来源 OAuth，不消费设备 token，不发送模型请求。

构建、固定输入与许可、独立 Compose/systemd 接线和本轮验证边界见 [部署说明](../../deploy/kilo-runtime/README.md)。应用既有生产部署方式保持；本目录的 HTTP 服务必须单独启动，Go 的 release-bound quota worker manager 不会代为启动它。

## 协议与会话

所有接口（含 health/result）要求 `Authorization: Bearer <service token>`。服务默认只监听 `127.0.0.1:8099`，`KILO_SERVICE_TOKEN_FILE` 至少含 32 个非空白可打印字符，并与 Go `KILO_EXECUTOR_TOKEN_FILE` 为同一 token；路径权限按部署说明配置。

| 接口 | 行为 |
| --- | --- |
| `GET /v1/health` | 返回 protocol/version/instance、六阶段 capabilities、容量与依赖检查，缺依赖时 `ok:false`。 |
| `POST /v1/tasks` | 快速 202 返回 queued task；满载 429 和 Retry-After。 |
| `GET /v1/tasks/:id` | 进行中 202、终态 200、不存在 404、已知过期 410。 |
| `POST /v1/tasks/:id/cancel` | 取消任务及其进程树，失效会话。 |
| `POST /v1/tasks/:id/ack` | Go 持久保存结果后 ACK，清结果并保留有界幂等墓碑。 |
| `DELETE /v1/sessions/:handle` | 幂等关闭短期会话。 |

请求绑定 `protocol_version,job_id,revision,operation_id,phase,deadline_at,expected_identity,login_proxy`。交互凭据通过私有 JSON 管道发送，不能放 argv、环境或日志。`source_id_token`、`identity_material` 与 Cookie 只能通过受鉴权且受期限约束的私有任务协议流动，由 Go 加密保存；HTTP 结果采用白名单投影。

同 `(job_id,revision,phase,operation_id)` 相同内容返回同 task，内容改变则 409。新 revision 取消旧执行与会话。会话绑定 job/revision/source_email 和固定代理 ID/URL 的 HMAC 指纹，不允许换代理或跨任务。默认串行、队列 64、会话 900 秒、结果 600 秒，重启后 `instance_id` 变化，旧会话 `SESSION_LOST`，Go 按持久事实恢复。

`resume_identity` 只恢复已验证原 Kilo 身份；`reconcile_subscription` 只观察已有连接事实；`reauthorize_subscription` 在原 Kilo 身份中显式重新授权，不能创建替代账号。个人与 Business 都要求官方来源/工作区证据，`expected_workspace` 不能被回显成已授权事实。BYOK 结果记录 callback、目标工作区、连接时间和来源验证证据；已有 `connected` 状态本身不证明本次已启用。`needs_action` 明确反馈额外邮箱、电话、未知 MFA、未识别 consent 或权限选择，不自动绕过。

## 固定官方契约

官方路由解释基于 `Kilo-Org/cloud@4f7acd6e99ebc66870c07324341eaf53d8b1fa1f`，不代表当前线上部署已验证。

- BYOK 由 Kilo `user.linkAuthProvider` 建立 linking 会话，再从 Kilo 生成的 OpenAI OAuth URL 授权；执行器不自行创建 Kilo confidential-client grant，也不使用本地 Codex client 替代它。
- 同源 consent 仅接受已识别 grant 表单与权限结构；本次 callback、官方来源、工作区、Kilo user ID 和连接变化必须一致。
- 设备申请和 token 消费属于 Go；执行器只使用显示的 `user_code` 调用 Kilo 网页批准接口。legacy GET 和 modern POST 都会单次消费，不能并行；尚无 Go refresh 存储与轮换前不启用 supportsRefresh。
- 设备批准响应不明返回 `REMOTE_OUTCOME_UNKNOWN`，Go 先查相同设备事实。Gateway Key 入库后仍由 Go 读取 profile 复核，不由执行器宣称模型路由可用。

## 离线验证

```sh
python -B tools/kilo-runtime/test_runtime.py -q
TOSUB2_ROOT=tools/kilo-runtime/vendor/tosub2 \
node tools/kilo-runtime/test_protocol.mjs /absolute/jsdom/lib/api.js
```

这里的 vendored 目录仅含两个精确 MIT helpers，协议夹具自行提供 HTTP 依赖和 jsdom。正式镜像通过固定 npm lock 安装 jsdom 并执行相同夹具以及真实 cfsolve wrapper `--check`。现有 Python 生命周期/API 夹具使用离线 fake protocol 验证队列、取消、TTL、ACK、幂等与身份边界，不能称真实登录成功。

`install.sh BUNDLE --replace-legacy` 仅确认现有 legacy unit 的存在，不 stop/disable 旧 `kilo-login.service`。若旧监听仍占 8099，新服务鉴权 health 失败后回滚 current/unit，旧服务保持。先自行安排端口或取得明确停旧服务授权，再安装；参见部署说明。
