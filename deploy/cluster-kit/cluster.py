#!/usr/bin/env python3
"""Create a fresh independent Sub2API site; existing sites are never overwritten."""
import argparse
import base64
import getpass
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import subprocess
import sys
import tarfile

ROOT = Path(__file__).resolve().parent
VERSION = '0.1.0'
BASE_COMMIT = 'fd80b8314fc2ddb9ec6470755abed0f741b27680'
REPOSITORY = 'https://github.com/yb1320057104/sub2api.git'
ROLES = ('primary', 'secondary', 'arbiter')


def require(value, message):
    if not value:
        raise ValueError(message)


def write_private(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w', encoding='utf8', newline='\n') as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.write('\n')


def read_json(path):
    require(path.is_file() and not path.is_symlink(), '配置文件不存在或为符号链接')
    return json.loads(path.read_text(encoding='utf-8-sig'))


def domain(value):
    require(isinstance(value, str) and len(value) <= 253 and '.' in value and
            all(re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', p) for p in value.split('.')),
            '域名只能填写小写域名，不含协议、端口或路径')
    return value


def validate(config):
    require(config.get('schema') == 1, '不支持的配置版本')
    require(re.fullmatch(r'[a-z][a-z0-9-]{0,22}[a-z0-9]', config.get('site_id', '')), '站点名须为 2–24 位小写字母、数字或连字符，不能以连字符结尾')
    mode = config.get('mode')
    require(mode in ('single', 'ha'), '部署模式必须为 single 或 ha')
    for key in ('acme_email', 'admin_email'):
        require(re.fullmatch(r'[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}', config.get(key, '')), key + ' 邮箱格式无效')
    hosts = config.get('hosts', {})
    require(set(hosts) == (set(ROLES) if mode == 'ha' else {'primary'}), '单机需要 primary；高可用需要 primary、secondary、arbiter')
    addresses = set()
    domains = set()
    for role, host in hosts.items():
        address = ipaddress.ip_address(host.get('address', ''))
        require(address.version == 4 and not address.is_loopback and not address.is_unspecified and not address.is_multicast,
                'SSH 地址须为实际服务器 IPv4 地址')
        require(str(address) not in addresses, '每个节点必须使用不同服务器地址')
        addresses.add(str(address))
        require(type(host.get('ssh_port', 22)) is int and 1 <= host.get('ssh_port', 22) <= 65535, 'SSH 端口无效')
        require(re.fullmatch(r'[a-z_][a-z0-9_-]{0,31}', host.get('ssh_user', 'root')), 'SSH 用户名无效')
        if host.get('ssh_key'):
            require(isinstance(host['ssh_key'], str) and '\n' not in host['ssh_key'], 'SSH 密钥路径无效')
        if role != 'arbiter':
            name = domain(host.get('domain', ''))
            require(name not in domains, '两台应用节点请使用不同入口域名')
            domains.add(name)
    net = config.get('network', {})
    cidr = ipaddress.ip_network(net.get('cidr', '10.86.0.0/24'))
    require(cidr.version == 4 and cidr.is_private and 24 <= cidr.prefixlen <= 28, '集群网段须为 RFC1918 私网 /24 至 /28')
    require(cidr.subnet_of(ipaddress.ip_network('10.0.0.0/8')) or cidr.subnet_of(ipaddress.ip_network('172.16.0.0/12')) or
            cidr.subnet_of(ipaddress.ip_network('192.168.0.0/16')), '集群网段须为 RFC1918 私网')
    require(not any(ipaddress.ip_address(a) in cidr for a in addresses), '集群网段不得覆盖 SSH 地址')
    port = net.get('port', 51820)
    require(type(port) is int and 1024 <= port <= 65535 and port not in (2379, 2380, 5432, 6379, 6432, 8008, 18080, 26379), 'WireGuard UDP 端口无效')
    image = config.get('app_image')
    if image:
        require(re.fullmatch(r'[a-z0-9][a-z0-9._:/-]*@sha256:[a-f0-9]{64}', image), '外部镜像必须使用 registry/image@sha256:... 固定摘要')
    return config


def validate_secrets(value, config):
    for key in ('pg_superuser_password', 'pg_replication_password', 'pg_app_password', 'redis_password',
                'redis_sentinel_password', 'admin_password', 'jwt_secret', 'totp_key'):
        require(re.fullmatch(r'[a-f0-9]{64}', value.get(key, '')), 'secrets.json 中 ' + key + ' 必须保留生成的 64 位十六进制值')
    require(re.fullmatch(r'[a-f0-9]{32}', value.get('installation_id', '')), '部署身份无效')
    require(set(value.get('wireguard', {})) == set(config['hosts']), 'WireGuard 身份与节点配置不一致')
    from cryptography.hazmat.primitives.asymmetric.x25519 import X25519PrivateKey
    from cryptography.hazmat.primitives import serialization
    for pair in value['wireguard'].values():
        private = base64.b64decode(pair['private'], validate=True)
        public = base64.b64decode(pair['public'], validate=True)
        calculated = X25519PrivateKey.from_private_bytes(private).public_key().public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)
        require(calculated == public, 'WireGuard 密钥对不匹配')
    return value


def make_secrets(roles):
    from cryptography.hazmat.primitives.asymmetric.x25519 import X25519PrivateKey
    from cryptography.hazmat.primitives import serialization
    result = {name: secrets.token_hex(32) for name in ('pg_superuser_password', 'pg_replication_password',
              'pg_app_password', 'redis_password', 'redis_sentinel_password', 'admin_password', 'jwt_secret', 'totp_key')}
    result['wireguard'] = {}
    for role in roles:
        key = X25519PrivateKey.generate()
        result['wireguard'][role] = {
            'private': base64.b64encode(key.private_bytes(serialization.Encoding.Raw, serialization.PrivateFormat.Raw, serialization.NoEncryption())).decode(),
            'public': base64.b64encode(key.public_key().public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)).decode(),
        }
    result['installation_id'] = secrets.token_hex(16)
    return result


def generate_inventory(config, secret_values, state_dir):
    validate(config)
    validate_secrets(secret_values, config)
    roles = tuple(config['hosts'])
    addresses = list(ipaddress.ip_network(config.get('network', {}).get('cidr', '10.86.0.0/24')).hosts())
    hosts = {}
    for role in roles:
        source = config['hosts'][role]
        host = {'ansible_host': source['address'], 'ansible_user': source.get('ssh_user', 'root'),
                'ansible_port': source.get('ssh_port', 22), 'node_role': role,
                'mesh_ip': '127.0.0.1' if config['mode'] == 'single' else str(addresses[ROLES.index(role)]),
                'domain': source.get('domain', ''),
                'ansible_ssh_common_args': '-o StrictHostKeyChecking=accept-new'}
        if source.get('ssh_key'):
            host['ansible_ssh_private_key_file'] = str(Path(source['ssh_key']).expanduser().resolve())
        password = os.environ.get('CLUSTER_SSH_PASSWORD_' + role.upper()) or os.environ.get('CLUSTER_SSH_PASSWORD')
        if password:
            host['ansible_password'] = password
        hosts[role] = host
    variables = {'site_id': config['site_id'], 'mode': config['mode'],
        'site_dir': '/opt/sub2api-sites/' + config['site_id'], 'installation_id': secret_values['installation_id'],
        'config_sha256': hashlib.sha256(json.dumps(config, sort_keys=True).encode()).hexdigest(),
        'secrets_sha256': hashlib.sha256(json.dumps(secret_values, sort_keys=True).encode()).hexdigest(),
        'wg_name': 'sc' + hashlib.sha256(config['site_id'].encode()).hexdigest()[:10],
        'wg_port': config.get('network', {}).get('port', 51820),
        'mesh_cidr': config.get('network', {}).get('cidr', '10.86.0.0/24'),
        'acme_email': config['acme_email'], 'admin_email': config['admin_email'],
        'app_image': config.get('app_image') or 'sub2api-cluster/' + config['site_id'] + ':' + VERSION,
        'source_build': not bool(config.get('app_image')), 'cluster_secrets': secret_values,
        'controller_state_dir': str(state_dir.resolve()), 'toolkit_root': str(ROOT)}
    return {'all': {'vars': variables, 'children': {
        'cluster': {'hosts': hosts}, 'data': {'hosts': {k: {} for k in roles if k != 'arbiter'}},
        'witness': {'hosts': {'arbiter': {}} if config['mode'] == 'ha' else {}}}}}


def initialize(args):
    name = args.site or input('站点名（例如 mysite）：').strip()
    mode = args.mode or ('ha' if input('部署模式：1 单机 / 2 三机高可用 [2]：').strip() != '1' else 'single')
    email = args.email or input('管理员 / HTTPS 证书邮箱：').strip()
    hosts = {}
    for role in (ROLES if mode == 'ha' else ('primary',)):
        address = getattr(args, role) or input(role + ' 服务器 IPv4：').strip()
        host = {'address': address, 'ssh_user': args.ssh_user, 'ssh_port': 22}
        if args.ssh_key:
            host['ssh_key'] = str(Path(args.ssh_key).expanduser().resolve())
        if role != 'arbiter':
            host['domain'] = (args.domain if role == 'primary' else args.secondary_domain) or input(role + ' 入口域名：').strip().lower()
        hosts[role] = host
    config = {'schema': 1, 'site_id': name, 'mode': mode, 'acme_email': email, 'admin_email': email,
              'hosts': hosts, 'network': {'cidr': args.mesh_cidr, 'port': 51820}, 'app_image': args.image or None}
    validate(config)
    target = Path(args.output).resolve() if args.output else ROOT / 'sites' / name
    require(not target.exists(), '目标目录已存在；不会重写原站点配置或密钥')
    target.mkdir(parents=True, mode=0o700)
    write_private(target / 'site.json', config)
    write_private(target / 'secrets.json', make_secrets(hosts))
    print('已生成 ' + str(target / 'site.json'))
    print('密码和集群密钥保存在同目录 secrets.json；请妥善保管，不要提交到 Git。')
    print('部署：./deploy.sh deploy --config ' + str(target / 'site.json'))


def prepare(args, run=False):
    config_path = Path(args.config).resolve()
    config = validate(read_json(config_path))
    secret_path = config_path.parent / 'secrets.json'
    if run:
        require(os.name == 'posix', '实际部署请在 Linux / WSL 运行 ./deploy.sh，Windows 可执行配置和计划命令')
        require(shutil.which('ansible-playbook'), '请通过 ./deploy.sh 运行以准备 Ansible 依赖')
        secret_values = validate_secrets(read_json(secret_path), config)
        if args.ask_ssh_password:
            require(shutil.which('sshpass'), '密码 SSH 需要控制端安装 sshpass：sudo apt install sshpass')
            for role in config['hosts']:
                os.environ['CLUSTER_SSH_PASSWORD_' + role.upper()] = getpass.getpass(role + ' SSH 密码（仅本次）：')
        if os.environ.get('CLUSTER_SSH_PASSWORD') or any(os.environ.get('CLUSTER_SSH_PASSWORD_' + role.upper()) for role in config['hosts']):
            require(shutil.which('sshpass'), '密码 SSH 需要控制端安装 sshpass')
        state = config_path.parent / 'runtime'
        state.mkdir(mode=0o700, exist_ok=True)
        inventory = generate_inventory(config, secret_values, state)
        import yaml
        inv_path = state / 'inventory.private.yml'
        require(not inv_path.is_symlink(), '运行配置路径不能是符号链接')
        # Transient inventory may contain SSH credentials; remove after this run.
        fd = os.open(str(inv_path), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, 'w', encoding='utf8') as stream:
            yaml.safe_dump(inventory, stream, sort_keys=False, allow_unicode=True)
        try:
            if not config.get('app_image') and args.command == 'deploy':
                prepare_source_archive(args, state)
            command = ['ansible-playbook', '-i', str(inv_path), str(ROOT / 'ansible' / ('verify.yml' if args.command == 'status' else 'site.yml'))]
            if args.ask_become_pass:
                command.append('--ask-become-pass')
            env = dict(os.environ, ANSIBLE_CONFIG=str(ROOT / 'ansible/ansible.cfg'))
            result = subprocess.run(command, env=env)
            if result.returncode:
                raise RuntimeError('部署/检查未完成；修复显示的问题后可以重新运行。已生成的数据库、密钥和复制配置不会重置。')
            print('检查通过。入口：' + ', '.join('https://' + h['domain'] for h in config['hosts'].values() if h.get('domain')))
            print('管理员：' + config['admin_email'] + '；初始密码见 ' + str(secret_path) + ' 的 admin_password。')
        finally:
            inv_path.unlink(missing_ok=True)
            for role in config['hosts']:
                os.environ.pop('CLUSTER_SSH_PASSWORD_' + role.upper(), None)
    else:
        result = {'site_id': config['site_id'], 'mode': config['mode'], 'new_independent_data': True,
                  'servers': {role: {'address': h['address'], 'domain': h.get('domain')} for role, h in config['hosts'].items()},
                  'application': config.get('app_image') or '从固定私库源码构建一次并同步两台应用节点',
                  'storage': 'PostgreSQL + Redis（独立数据目录）' if config['mode'] == 'single' else 'Patroni 双副本 + 三成员 etcd + Redis 双副本 / 三 Sentinel',
                  'changes_applied': False}
        print(json.dumps(result, ensure_ascii=False, indent=2))


def prepare_source_archive(args, state):
    source = state / 'source'
    archive = state / 'source.tar.gz'
    repo = Path(args.source_repo).expanduser().resolve() if args.source_repo else None
    if repo is None:
        repo = next((p for p in ROOT.parents if (p / '.git').exists() and (p / 'backend/go.mod').is_file()), None)
    if repo is None:
        repo = state / 'source-repository'
        if not repo.exists():
            subprocess.run(['git', 'clone', '--no-checkout', REPOSITORY, str(repo)], check=True)
    # Preparation verifies its exact base and output manifest; never packages dirty files.
    command = [sys.executable, str(ROOT / 'build/prepare_source.py'), '--repo', str(repo), '--output', str(source)]
    subprocess.run(command, check=True)
    temporary = archive.with_suffix('.pending')
    with tarfile.open(temporary, 'w:gz') as bundle:
        for child in sorted(source.iterdir()):
            require(not child.is_symlink(), '源码归档不允许符号链接')
            bundle.add(child, arcname=child.name)
    os.replace(temporary, archive)


def main():
    parser = argparse.ArgumentParser(description='Sub2API 独立站点部署包 ' + VERSION)
    sub = parser.add_subparsers(dest='command', required=True)
    init = sub.add_parser('init', help='生成独立站点配置和全新密钥')
    for name in ('site', 'email', 'primary', 'secondary', 'arbiter', 'domain', 'secondary-domain', 'ssh-key', 'output', 'image'):
        init.add_argument('--' + name)
    init.add_argument('--mode', choices=('single', 'ha'))
    init.add_argument('--ssh-user', default='root')
    init.add_argument('--mesh-cidr', default='10.86.0.0/24')
    for name in ('plan', 'validate', 'deploy', 'status'):
        cmd = sub.add_parser(name)
        cmd.add_argument('--config', required=True)
        cmd.add_argument('--source-repo')
        cmd.add_argument('--ask-ssh-password', action='store_true')
        cmd.add_argument('--ask-become-pass', action='store_true')
    args = parser.parse_args()
    try:
        if args.command == 'init':
            initialize(args)
        else:
            prepare(args, run=args.command in ('deploy', 'status'))
    except (ValueError, RuntimeError, OSError, subprocess.CalledProcessError) as error:
        if isinstance(error, subprocess.CalledProcessError):
            print('失败：子步骤退出码 ' + str(error.returncode), file=sys.stderr)
        elif isinstance(error, OSError):
            print('失败：无法访问所需文件或程序（' + type(error).__name__ + '）', file=sys.stderr)
        else:
            print('失败：' + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
