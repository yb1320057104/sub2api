# 币安香港节点网关

`gateway.py` 使用 Python 3 标准库，建议使用服务器发行版维护的 Python 3.10 或更新版本。它监听应用容器可访问的私有 IPv4 地址，不修改系统路由。

每个上游端口必须是已经固定到**单个香港节点**的本地 HTTP CONNECT listener。不要接到会自动切换国家的全局 selector。网关不读取订阅，也不持有节点密码。

## 配置

配置文件必须使用绝对路径；Linux 文件权限为 `0600`，所有者为网关服务用户。示例中的认证密码必须替换为随机生成的长密码。

```json
{
  "bind": "172.19.0.1",
  "port": 17990,
  "auth_username": "binance-app",
  "auth_password": "REPLACE-WITH-A-RANDOM-PASSWORD-AT-LEAST-24-CHARS",
  "nodes": [
    {"name": "hk-01", "host": "127.0.0.1", "port": 19901},
    {"name": "hk-02", "host": "127.0.0.1", "port": 19902}
  ]
}
```

- 支持 1～8 个候选，名称和 listener 地址不能重复；`host` 必须是 loopback IP，端口为 1～65535。
- 用户名为 1～128 位可见 ASCII 且不能包含冒号；密码为 24～512 位可见 ASCII。
- `bind` 必须为私有或 loopback IPv4，不能为 `0.0.0.0`。防火墙仅允许受信应用容器访问该端口。应用到网关的 Basic 认证使用 HTTP，只适合受信主机内部网络；业务请求仍使用端到端 HTTPS。
- 地区条件固定为 `HK`，不可通过配置关闭或改成其他国家。

## HTTP 契约

| 请求 | 认证及返回 |
| --- | --- |
| `POST /_binance/preflight` | `Authorization: Basic …`，无请求体；成功返回 `200 {"route_token":"…"}` |
| `CONNECT api.binance.com:443` | `Proxy-Authorization: Basic …` 和 `X-Binance-Route-Token: …`；成功返回 `200 Connection Established`，随后转发 TLS 字节 |
| `GET /health` | `Authorization: Basic …`；返回 `{"status":"ok","configured_nodes":N}`，只表示网关存活，不代表节点可用 |

每次 preflight 并行探测所有配置候选。每个候选在同一个固定 listener 上先请求 `https://www.cloudflare.com/cdn-cgi/trace`，要求 TLS 证书有效、HTTP 200、唯一 `loc=HK`；随后请求 `https://api.binance.com/api/v3/time`，要求 TLS 有效、HTTP 200、JSON 中 `serverTime` 为正整数。两次请求共用每节点 6 秒绝对超时，整轮最多 12 秒；不跟随重定向。

至少一个节点通过才签发随机令牌，选择本轮两项探测总耗时最短的节点。令牌固定该节点，240 秒后失效；可在同一查账操作中重复建立连接，现存隧道也受令牌到期时间限制。需要同一 Basic 凭据才能使用令牌。网关重启或更换凭据后旧令牌失效。

令牌指向的节点后续故障时连接失败，不会换节点或直连。用户 CONNECT 仅允许 `api.binance.com:443`；Cloudflare 只供网关内部探测。地理验证依赖 Cloudflare 对该固定节点本轮出口的判断，节点服务必须保持 listener 与单一节点的绑定。

错误响应只含固定错误标识：普通认证失败 `401`，CONNECT 认证失败 `407`，目标或令牌无效 `403`，上游隧道失败 `502`，没有合格节点 `503`。最多同时进行四轮 preflight，覆盖三个后台账户 worker 并为管理员查询保留容量；超限返回 `429`。`429/503` 附 `Retry-After: 15`，客户端仍应遵守自身更长的退避时间。

网关最多接收 64 条连接、保存 512 个有效令牌。请求头最多 16 KiB；探测响应体最多 8 KiB。单隧道缓冲每方向最多 64 KiB，总传输最多 64 MiB，空闲 30 秒断开。接口不接受请求体或 HTTP 代理形式的普通转发。

## systemd 示例

先由部署流程创建独立用户 `binance-proxy`，安装脚本和配置，并启动各固定节点 listener。将下列单元中的路径调整为实际安装路径：

```ini
[Unit]
Description=Binance fixed Hong Kong node gateway
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
User=binance-proxy
Group=binance-proxy
ExecStart=/usr/bin/python3 /opt/binance-proxy/gateway.py --config /etc/binance-proxy/gateway.json
Restart=on-failure
RestartSec=3
TimeoutStopSec=20
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
UMask=0077

[Install]
WantedBy=multi-user.target
```

`SIGTERM/SIGINT` 停止接收新请求，给已建立连接最多 5 秒排空，再关闭连接并等待探测线程退出。脚本只记录启动、停止或固定启动失败提示，不记录认证、令牌、签名 URL、请求体或原始异常。

测试在云端或获准的隔离远端执行。仓库无需新增测试文件；可导入 `GatewayState` 和 `GatewayServer`，用 loopback 假 listener 验证协议，或替换 `probe_response` 验证香港地区和币安响应判定。生产实现始终开启 TLS 主机名和证书验证。
