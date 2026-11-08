"""One approved artifact, two serial API replacements, then their workers."""
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time

NODES = ('primary', 'secondary')

def need(value, code):
    if not value:
        raise RuntimeError(code)

def atomic(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    tmp = path.with_suffix('.pending')
    with tmp.open('w', encoding='utf8') as f:
        os.chmod(str(tmp), 0o600)
        json.dump(value, f, indent=2)
        f.write('\n'); f.flush(); os.fsync(f.fileno())
    os.replace(str(tmp), str(path))

def baseline(value):
    match = re.fullmatch(r'(\d+\.\d+\.\d+-pk\.\d+)(?:-(?:ha|secondary)\.\d{8}\.\d+)?', str(value))
    need(match is not None, 'unknown_running_version')
    return match.group(1)

def command(config, node):
    need(node in NODES and set(config['nodes']) == set(NODES), 'node_inventory_invalid')
    peer = config['nodes'][node]
    if peer.get('local'):
        need(peer.get('node') == node, 'local_node_identity_invalid')
        return [peer.get('python', '/usr/bin/python3'), '-B', peer['agent'], '--config', peer['agent_config']]
    for key in ('identity_file','known_hosts_file'):
        p = Path(peer[key])
        need(p.is_file() and not p.is_symlink(), 'ssh_identity_missing')
    need(re.fullmatch(r'[A-Za-z0-9_.:-]+', peer['host']) and peer['user'] == 'root', 'ssh_target_invalid')
    return ['/usr/bin/ssh', '-T', '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes',
            '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + peer['known_hosts_file'],
            '-o', 'ConnectTimeout=10', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=3',
            '-i', peer['identity_file'], '-p', str(peer.get('port',22)), peer['user'] + '@' + peer['host'], 'rpc']

def rpc(config, node, action, job_id=None, artifact_file=None, timeout=60, **fields):
    payload = dict(fields, action=action)
    if job_id is not None:
        need(re.fullmatch(r'[a-f0-9]{16,64}', job_id), 'job_id_invalid')
        payload['job_id'] = job_id
    header = (json.dumps(payload, separators=(',', ':')) + '\n').encode()
    if artifact_file is not None:
        path = Path(artifact_file)
        need(path.is_file() and not path.is_symlink(), 'binary_not_regular')
        data = header + path.read_bytes()
    else:
        data = header
    try:
        done = subprocess.run(command(config,node), input=data, stdout=subprocess.PIPE,
                              stderr=subprocess.PIPE, timeout=timeout)
    except subprocess.TimeoutExpired:
        raise RuntimeError(node + '_' + action + '_timeout_review_required')
    need(len(done.stdout) <= 1024 * 1024, 'agent_response_too_large')
    try:
        result = json.loads(done.stdout)
    except (ValueError, UnicodeError):
        raise RuntimeError(node + '_' + action + '_response_invalid')
    if done.returncode or result.get('ok') is not True:
        error = result.get('code') or result.get('error') or 'agent_failed'
        need(isinstance(error,str) and re.fullmatch(r'[a-zA-Z0-9_.:-]{1,140}',error), 'agent_failed')
        raise RuntimeError(node + '_' + error)
    need(isinstance(result.get('result'),dict), 'agent_result_invalid')
    return result['result']

def observe(config):
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        jobs = {node:pool.submit(rpc,config,node,'observe',timeout=35) for node in NODES}
        rows = {node:job.result() for node,job in jobs.items()}
    for node,row in rows.items():
        row['baseline'] = baseline(row.get('version'))
        need(row.get('node',node) == node, 'observed_node_mismatch')
    return rows

def ready(config, node, expected_version=None):
    row = rpc(config,node,'observe',timeout=35)
    need(row.get('ready') is True, node + '_not_ready')
    if expected_version:
        need(row.get('version') == expected_version, node + '_version_mismatch')
    return row

def deploy(config, artifact, report):
    """Stop at the first failure; never take the other API out after failure."""
    need(artifact.get('build_type') == 'cluster-ha', 'ha_artifact_required')
    digest = artifact.get('sha256','')
    need(re.fullmatch(r'[a-f0-9]{64}',digest), 'artifact_digest_invalid')
    path = Path(artifact['binary'])
    need(path.is_file() and not path.is_symlink() and path.stat().st_size == artifact['bytes'], 'artifact_size_changed')
    need(hashlib.sha256(path.read_bytes()).hexdigest() == digest, 'artifact_digest_changed')
    need(baseline(artifact['version']) == artifact['baseline'], 'artifact_version_invalid')
    observed = observe(config)
    need(all(v.get('ready') is True for v in observed.values()), 'both_nodes_must_be_ready')
    for node in NODES:
        need(rpc(config,node,'preflight',timeout=45).get('can_update') is True, node+'_preflight_failed')
    target_order = tuple(int(n) for n in re.findall(r'\d+',artifact['baseline']))
    for row in observed.values():
        need(tuple(int(n) for n in re.findall(r'\d+',row['baseline'])) <= target_order, 'cluster_downgrade_refused')
    revisions=[]
    for row in observed.values():
        for key in ('api_receipt','worker_receipt'):
            revision=row.get(key,{}).get('revision')
            need(type(revision) is int and revision>0,'observed_revision_missing')
            revisions.append(revision)
        staged_revision=row.get('highest_staged_revision',max(revisions))
        need(type(staged_revision) is int and 0<staged_revision<999999,'staged_revision_invalid')
        revisions.append(staged_revision)
    revision=max(revisions)+1
    job_id=os.urandom(12).hex()
    record=Path(config['root'])/'rollouts'/job_id/'state.json'
    state={'schema':2,'job_id':job_id,'phase':'staging','artifact':{k:artifact[k] for k in ('version','baseline','commit','sha256','bytes')},
           'revision':revision,'before':observed,'nodes':{},'started_at':int(time.time())}
    atomic(record,state)
    descriptor={k:artifact[k] for k in ('version','baseline','sha256','bytes')}
    descriptor['revision']=revision
    def progress(phase,node=None):
        state['phase']=phase;state['updated_at']=int(time.time());atomic(record,state)
        report({'phase':phase,'node':node,'rollout_id':job_id})
    try:
        # Stage both before changing traffic. All nodes receive identical bytes.
        for node in NODES:
            progress('staging_'+node,node)
            uploaded=rpc(config,node,'upload',job_id,artifact_file=path,artifact=descriptor,timeout=600)
            staged=rpc(config,node,'stage',job_id,artifact=descriptor,binary_path=uploaded['binary_path'],timeout=180)
            need(staged['api_receipt']['binary_sha256']==digest and staged['worker_receipt']['binary_sha256']==digest,'staged_binary_mismatch')
            state['nodes'][node]={'staged':staged};atomic(record,state)
        # API order is fixed and single-threaded; the peer carries new traffic.
        for node in ('secondary','primary'):
            peer='primary' if node=='secondary' else 'secondary'
            ready(config,peer,artifact['version'] if peer=='secondary' else None)
            ready(config,node)
            drains=[]
            attempted_edges=[]
            try:
                progress('draining_'+node,node)
                for edge in NODES:
                    attempted_edges.append(edge)
                    drains.append(rpc(config,edge,'route',job_id,target=node,state='drain',timeout=35))
                ready(config,peer,artifact['version'] if peer=='secondary' else None)
                progress('updating_'+node,node)
                updated=rpc(config,node,'replace_api',job_id,peer_receipt=state['nodes'][peer]['staged']['api_receipt'],drains=drains,timeout=1200)
                ready(config,node,artifact['version'])
                state['nodes'][node]['api']=updated;atomic(record,state)
            finally:
                # A failed node stays removed unless its own rollback proves ready.
                restored_ready=False
                try:restored_ready=ready(config,node).get('ready') is True
                except Exception:pass
                restore_errors=[]
                if restored_ready:
                    for edge in reversed(attempted_edges):
                        try:rpc(config,edge,'route',job_id,target=node,state='ready',timeout=40)
                        except Exception as error:restore_errors.append(type(error).__name__)
                if restore_errors:
                    raise RuntimeError('route_restore_failed_review_required')
            progress('verifying_'+node,node)
            for _ in range(3):
                ready(config,node,artifact['version']);ready(config,peer)
                time.sleep(1)
        # Backing stores are untouched. Only each supervisor's worker is replaced.
        for node in ('secondary','primary'):
            progress('updating_worker_'+node,node)
            for peer in NODES:ready(config,peer,artifact['version'])
            state['nodes'][node]['worker']=rpc(config,node,'update_worker',job_id,timeout=180)
            atomic(record,state)
        final=observe(config)
        need(all(r.get('ready') is True and r.get('version')==artifact['version'] and r.get('worker_version')==artifact['version'] for r in final.values()),'final_versions_not_consistent')
        need(sum(r.get('worker_running') is True for r in final.values())==1,'expected_one_worker')
        need(sum(r.get('local_writer') is True for r in final.values())==1 and
             all(type(r.get('local_writer')) is bool and r.get('worker_running')==r['local_writer'] for r in final.values()),
             'worker_not_on_current_writer')
        state['after']=final;state['completed_at']=int(time.time());progress('completed')
        return {'phase':'completed','rollout_id':job_id,'nodes':final,'revision':revision}
    except Exception as error:
        state['error']=str(error) if re.fullmatch(r'[a-zA-Z0-9_.:-]{1,180}',str(error)) else type(error).__name__
        progress('failed')
        raise
