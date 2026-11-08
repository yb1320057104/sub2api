"""Read-only verification of a freshly installed site's storage and replication."""
import argparse
import http.client
import ipaddress
import json
import os
from pathlib import Path
import re
import socket
import subprocess


def need(value, code):
    if not value:
        raise RuntimeError(code)


def http_json(host, port, path):
    connection = http.client.HTTPConnection(host, port, timeout=4)
    try:
        connection.request('GET', path, headers={'Connection': 'close'})
        response = connection.getresponse()
        raw = response.read(1024 * 1024 + 1)
        need(len(raw) <= 1024 * 1024, 'http_response_too_large')
        return response.status, json.loads(raw)
    finally:
        connection.close()


def read_resp(stream):
    line = stream.readline(65537)
    need(line.endswith(b'\r\n') and len(line) <= 65536, 'redis_reply_invalid')
    kind, payload = line[:1], line[1:-2]
    if kind == b'+':
        return payload.decode()
    if kind == b'-':
        raise RuntimeError('redis_command_rejected')
    if kind == b':':
        return int(payload)
    if kind == b'$':
        length = int(payload)
        if length == -1:
            return None
        need(0 <= length <= 1024 * 1024, 'redis_bulk_too_large')
        data = stream.read(length + 2)
        need(len(data) == length + 2 and data.endswith(b'\r\n'), 'redis_bulk_truncated')
        return data[:-2].decode()
    if kind == b'*':
        length = int(payload)
        if length == -1:
            return None
        need(0 <= length <= 10000, 'redis_array_too_large')
        return [read_resp(stream) for _ in range(length)]
    raise RuntimeError('redis_type_invalid')


def redis_command(host, port, password, *parts):
    def send(connection, stream, items):
        values = [str(item).encode() for item in items]
        request = ('*%d\r\n' % len(values)).encode()
        for value in values:
            request += ('$%d\r\n' % len(value)).encode() + value + b'\r\n'
        connection.sendall(request)
        return read_resp(stream)

    with socket.create_connection((host, port), timeout=4) as connection:
        with connection.makefile('rb') as stream:
            need(send(connection, stream, ('AUTH', 'default', password)) == 'OK', 'redis_auth_failed')
            return send(connection, stream, parts)


def redis_info(host, password):
    raw = redis_command(host, 6379, password, 'INFO', 'replication')
    return dict(line.split(':', 1) for line in raw.splitlines() if line and not line.startswith('#') and ':' in line)


def run(command, env=None):
    process = subprocess.run(command, env=env, stdout=subprocess.PIPE,
                             stderr=subprocess.PIPE, timeout=20)
    need(process.returncode == 0, 'storage_command_failed')
    return process.stdout


def postgres_query(args, secret, port, query, app=False):
    env = dict(os.environ, PGPASSWORD=secret['pg_app_password'] if app else secret['pg_superuser_password'])
    command = ['docker', 'exec', '-e', 'PGPASSWORD', args.site_id + '-postgres',
               'psql', '-X', '-q', '-A', '-t', '-v', 'ON_ERROR_STOP=1',
               '-h', args.mesh_ip, '-p', str(port), '-U', 'sub2api_app' if app else 'postgres',
               '-d', 'sub2api' if app else 'postgres', '-c', query]
    return json.loads(run(command, env))


def verify(args):
    secret = json.loads((Path(args.site_dir) / 'storage/private/storage-secrets.json').read_text())
    members = args.members.split(',')
    data = args.data_members.split(',')
    for address in members + data + [args.mesh_ip]:
        need(ipaddress.ip_address(address).version == 4, 'ipv4_mesh_required')
    need(len(set(members)) == len(members) and set(data).issubset(members) and
         args.mesh_ip in members, 'mesh_members_invalid')
    result = {'ok': True, 'mode': args.mode, 'node_role': args.node_role, 'site_id': args.site_id}
    if args.mode == 'ha':
        need(len(members) == 3 and len(data) == 2, 'ha_topology_invalid')
        for address in members:
            status, value = http_json(address, 2379, '/health')
            need(status == 200 and value.get('health') in (True, 'true'), 'etcd_member_unhealthy')
        endpoints = ','.join('http://' + address + ':2379' for address in members)
        statuses = json.loads(run(['docker', 'exec', args.site_id + '-etcd', '/usr/local/bin/etcdctl',
                                   '--endpoints=' + endpoints, '--write-out=json', 'endpoint', 'status']))
        need(len(statuses) == 3 and len({entry['Status']['header']['cluster_id'] for entry in statuses}) == 1 and
             len({entry['Status']['leader'] for entry in statuses}) == 1 and
             all(entry['Status']['leader'] and not entry['Status'].get('errors') for entry in statuses),
             'etcd_cluster_disagrees')
        result['etcd_healthy_members'] = 3

        postgres_roles = {}
        for address in data:
            status, value = http_json(address, 8008, '/patroni')
            need(status == 200 and value.get('state') == 'running' and
                 value.get('patroni', {}).get('scope') == args.site_id and
                 value.get('patroni', {}).get('name') in ('primary', 'secondary'), 'patroni_not_ready')
            postgres_roles[address] = value['role']
        need(sum(role in ('primary', 'master') for role in postgres_roles.values()) == 1 and
             sum(role == 'replica' for role in postgres_roles.values()) == 1, 'postgres_single_writer_required')
        result['postgres_roles'] = postgres_roles

        sentinel_masters = []
        for address in members:
            quorum = redis_command(address, 26379, secret['redis_sentinel_password'], 'SENTINEL', 'CKQUORUM', args.site_id)
            need(isinstance(quorum, str) and quorum.startswith('OK'), 'sentinel_quorum_unavailable')
            master = redis_command(address, 26379, secret['redis_sentinel_password'],
                                   'SENTINEL', 'get-master-addr-by-name', args.site_id)
            need(isinstance(master, list) and len(master) == 2 and master[0] in data and master[1] == '6379',
                 'sentinel_master_invalid')
            sentinel_masters.append(tuple(master))
        need(len(set(sentinel_masters)) == 1, 'sentinels_disagree')
        redis_writer = sentinel_masters[0][0]
        redis_roles = {}
        for address in data:
            value = redis_info(address, secret['redis_password'])
            redis_roles[address] = value['role']
            if address == redis_writer:
                need(value['role'] == 'master' and int(value.get('connected_slaves', 0)) == 1,
                     'redis_primary_replication_missing')
            else:
                need(value['role'] == 'slave' and value.get('master_host') == redis_writer and
                     value.get('master_link_status') == 'up' and value.get('master_sync_in_progress') == '0',
                     'redis_replica_not_streaming')
        result.update(sentinel_healthy_members=3, redis_roles=redis_roles, redis_writer=redis_writer)
    else:
        need(len(members) == 1 and members == data and args.mesh_ip == '127.0.0.1', 'single_topology_invalid')
        need(redis_info(args.mesh_ip, secret['redis_password'])['role'] == 'master', 'single_redis_not_master')
        result['redis_role'] = 'master'

    if args.node_role != 'arbiter':
        query = """BEGIN READ ONLY; SET LOCAL statement_timeout='5s';
SELECT json_build_object('database',current_database(),'user',current_user,
 'in_recovery',pg_is_in_recovery(),'max_connections',current_setting('max_connections')::int,
 'role_connection_limit',(SELECT rolconnlimit FROM pg_roles WHERE rolname=current_user)); COMMIT;"""
        value = postgres_query(args, secret, 6432, query, app=True)
        need(value == {'database': 'sub2api', 'user': 'sub2api_app', 'in_recovery': False,
                       'max_connections': 100, 'role_connection_limit': 90}, 'application_database_contract_failed')
        result['application_database'] = value
        if args.mode == 'ha':
            query = """BEGIN READ ONLY; SET LOCAL statement_timeout='5s';
SELECT json_build_object('in_recovery',pg_is_in_recovery(),
 'streaming_replicas',(SELECT count(*) FROM pg_stat_replication WHERE state='streaming')); COMMIT;"""
            local = postgres_query(args, secret, 5432, query)
            if result['postgres_roles'][args.mesh_ip] in ('master', 'primary'):
                need(not local['in_recovery'] and local['streaming_replicas'] == 1, 'postgres_replica_not_streaming')
            else:
                need(local['in_recovery'], 'postgres_replica_is_writable')
            result['local_postgres'] = local
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--site-dir', required=True)
    parser.add_argument('--site-id', required=True)
    parser.add_argument('--mode', choices=('single', 'ha'), required=True)
    parser.add_argument('--node-role', choices=('primary', 'secondary', 'arbiter'), required=True)
    parser.add_argument('--mesh-ip', required=True)
    parser.add_argument('--members', required=True)
    parser.add_argument('--data-members', required=True)
    args = parser.parse_args()
    need(re.fullmatch(r'[a-z][a-z0-9-]{1,23}', args.site_id), 'site_id_invalid')
    print(json.dumps(verify(args), sort_keys=True))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(json.dumps({'ok': False, 'error': str(error) if isinstance(error, RuntimeError) else type(error).__name__}))
        raise SystemExit(1)
