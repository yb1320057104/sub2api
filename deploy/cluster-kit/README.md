# Sub2API 独立站点部署包

用一份配置创建全新的 Sub2API 站点，可选单机或三机高可用。每个站点有独立的 PostgreSQL、Redis、管理员、加密密钥和账号数据。

下载：[工具包 ZIP](https://github.com/yb1320057104/sub2api/raw/refs/heads/main/deploy/cluster-kit/releases/sub2api-cluster-kit-0.1.0.zip) · [SHA256](https://github.com/yb1320057104/sub2api/blob/main/deploy/cluster-kit/releases/SHA256SUMS)。需要登录有仓库访问权限的 GitHub 账号。

本工具是**新建站点安装器**。重复运行只继续同一安装，不覆盖已有容器、密钥、复制角色或数据库；不是现有生产集群的升级、迁移或在线扩容工具。

| 模式 | 服务器 | 应用与数据 |
| --- | --- | --- |
| `single` | 1 台 | API、后台任务、PostgreSQL、Redis、HTTPS 同机 |
| `ha` | 2 台应用机 + 1 台独立仲裁机 | 两台 API 接流；Patroni / etcd 管理 PostgreSQL 主从；Redis / Sentinel 自动接管；后台任务跟随数据库主节点 |

高可用模式中，两个入口域名属于**同一个站点**，共用该站点的账号、余额、配置和数据库。两个域名各自优先本机 API，API 不健康时由存活入口转发至另一台，已发送的模型请求不跨节点重放。

## 准备

- 全新或专用 **Ubuntu 24.04+ amd64** 服务器。首版不覆盖已有业务，不自动迁移 CentOS 等旧系统。
- 数据节点建议至少 4 CPU / 8 GB / 40 GB；源码首次构建建议 8 GB 以上内存。仲裁机建议 2 CPU / 2 GB。容量建议不等于模型并发保证。
- 部署控制端使用 Linux 或 WSL，Python 3.11+、`python3-venv`、Git 和 SSH。可以在主应用机执行，并通过 SSH 管理自身及其他节点。
- SSH 用户为 root 或可 sudo 用户。推荐密钥；密码方式需控制端安装 `sshpass`，命令支持 `--ask-ssh-password`，逐节点输入且不写入发布包。
- 为每台应用机准备一个不同的域名，其 DNS 指向该机公网地址。允许 TCP 80/443，Caddy 自动申请免费证书。无需 Cloudflare API Token；使用 Cloudflare 时 SSL 模式选择 Full (strict)。
- 云安全组放行 SSH，以及三个节点之间的 UDP 51820。数据库、Redis、etcd 只监听 WireGuard 私网；仲裁机不需要公开 80/443。
- 首次连接使用 SSH `accept-new` 记录主机密钥，已记录密钥变化会拒绝连接。可先按自己的流程核对并写入 `known_hosts`。

## 快速开始

有该私库读取权限的控制端：

```bash
sudo apt install python3-venv openssh-client sshpass git
git clone https://github.com/yb1320057104/sub2api.git
cd sub2api/deploy/cluster-kit
bash deploy.sh init
```

按提示填写站点名、模式、服务器地址、域名和邮箱。向导生成 `sites/<站点名>/site.json` 与独立的 `secrets.json`。随后执行：

```bash
bash deploy.sh plan --config sites/mysite/site.json
bash deploy.sh deploy --config sites/mysite/site.json
```

使用 SSH 密码时：

```bash
bash deploy.sh deploy --config sites/mysite/site.json --ask-ssh-password
```

默认从固定私库提交和本包的 HA 修复补丁准备干净源码，只在主应用机构建一次镜像，再经 SSH 传给副机。不会使用控制端未提交的应用改动，也不会把 GitHub 凭据复制到新服务器。

安装成功后会给出 HTTPS 地址和管理员邮箱。初始管理员密码见本地 `secrets.json` 的 `admin_password`，登录后可在网站内修改密码。请保留整个站点配置目录，用于今后的检查和灾难恢复。

已构建的外部应用镜像可通过 `init --image registry/image@sha256:...` 指定。它必须带有本构建流程的 `io.sub2api.cluster.capabilities=ha-r4` 标签；普通旧版镜像缺少 HA 生命周期修复，不能直接替代。私有镜像需先在两台应用机完成 `docker login`。

## 无交互配置示例

示例地址为文档保留地址，需要替换为实际服务器。三个节点地址必须不同；不同集群在相同网络中共存时还要选择不重叠的 WireGuard 网段和端口。

```bash
# 单机
bash deploy.sh init --site demo --mode single \
  --primary 192.0.2.10 --domain app.example.com \
  --email admin@example.com --ssh-key ~/.ssh/id_ed25519

# 三机高可用
bash deploy.sh init --site team --mode ha \
  --primary 192.0.2.10 --secondary 192.0.2.20 --arbiter 192.0.2.30 \
  --domain team.example.com --secondary-domain backup.example.com \
  --email admin@example.com --ssh-key ~/.ssh/id_ed25519
```

## 状态、重试与维护

```bash
bash deploy.sh status --config sites/team/site.json
```

状态检查验证 PostgreSQL / Redis、实际应用就绪、鉴权、worker 主节点跟随和公网 HTTPS。它不发送模型请求。

安装中网络断开或镜像下载失败时，修复原因后使用**原配置和原 secrets.json** 重跑 deploy。它拒绝把另一套密钥写到已有数据上，保留 Redis/Sentinel 重写的当前复制角色。变更域名、节点身份、镜像或模式应另做有计划的维护，不能修改配置后当作新安装覆盖。

本包不执行 `down -v`、清库、移除旧站或故障演练；正常检查不重启存储。站点所有持久数据位于各机 `/opt/sub2api-sites/<站点名>/`。请配置独立异地备份：复制提供可用性，不能恢复误删除和同时损坏的数据。

高可用的边界：

- PostgreSQL 与 Redis 角色独立选举，worker 跟随 PostgreSQL writer。应用 readiness 同时检查可写数据库和 Redis master；不把“进程在运行”当作可用。
- 一台入口服务器整机失效时，仍可访问另一台的域名。若要求**同一域名**也自动绕开已故障公网 IP，需要另外配置 DNS/全局负载均衡的健康检查；仅 HTTPS 反向代理无法接到发给已关机服务器的连接。
- 三机拓扑依赖两个仲裁成员可达，两个数据节点不能同时故障。复制和故障切换仍有一致性边界，详情见存储角色 README。
- worker 优雅停止不会把已发出的外部请求撤回。网络分区或主机冻结期间，不能把后台任务称为全局恰好执行一次。
- 本包保留手动糖果测试、共享并发与计费准入、有序退出和多域名登录修复；不自动开启网页自更新，避免普通更新覆盖 HA 功能。

## 发布包与源码

`build/prepare_source.py` 验证固定源提交和补丁哈希；`patches/` 保存必要 HA 修改；`build/Dockerfile` 在目标服务器重建前后端。LGPLv3 许可证和可构建源码与部署工具一同提供。

站点配置、密码、私钥、数据库、账号、镜像归档和构建缓存都被排除在可分享 ZIP 之外。构建工具包归档：

```bash
python3 tools/package.py
```

具体检查范围见 `VALIDATION.md`；发布包的模板验证不等于已在新购三台主机完成整机断电演练。
