#!/usr/bin/env bash
# Install a prebuilt bundle. No downloads, system pip installs or source builds.
# A legacy-login acknowledgement never stops or disables kilo-login.service.
# Switch only after offline checks; commit only after authenticated HTTP is ready.
# A failed switch restores the previous release/unit, or removes the new unit.
set -euo pipefail
if [ "$#" -lt 1 ]; then
  echo 'usage: install.sh BUNDLE.tar.gz [--replace-legacy]' >&2
  exit 2
fi
bundle=$1
replace=${2:-}
app_dir=${KILO_APP_DIR:-/opt/kilo-runtime}
service_user=${KILO_SERVICE_USER:-kilosvc}
service_name=${KILO_SERVICE_NAME:-kilo-runtime}
port=${KILO_PORT:-8099}
unit_dir=${KILO_SYSTEMD_UNIT_DIR:-/etc/systemd/system}
ready_timeout=${KILO_READY_TIMEOUT_SECONDS:-30}
if [[ ! "$ready_timeout" =~ ^([1-9]|[12][0-9]|30)$ ]]; then
  echo 'KILO_READY_TIMEOUT_SECONDS must be between 1 and 30' >&2
  exit 2
fi
if [[ ! "$service_name" =~ ^[A-Za-z0-9][A-Za-z0-9_.@-]*$ ]] || [ "$service_name" = kilo-login ]; then
  echo 'invalid Kilo runtime service name' >&2
  exit 2
fi
if [ "$(id -u)" != 0 ]; then echo 'run as root' >&2; exit 2; fi
if systemctl is-active --quiet kilo-login.service && [ "$replace" != '--replace-legacy' ]; then
  echo 'legacy kilo-login.service is active; arrange its port separately and use --replace-legacy to acknowledge it; this installer leaves it unchanged' >&2
  exit 3
fi
if ! id -u "$service_user" >/dev/null 2>&1; then
  useradd --system --home "$app_dir" --shell /usr/sbin/nologin "$service_user"
fi
install -d -m 0750 -o "$service_user" -g "$service_user" "$app_dir" "$app_dir/releases"
release="$app_dir/releases/$(date -u +%Y%m%dT%H%M%SZ)-$$"
install -d -m 0750 "$release"
python3 - "$bundle" "$release" <<'PY'
import hashlib,json,sys,tarfile
from pathlib import Path
bundle,target=map(Path,sys.argv[1:])
with tarfile.open(bundle,'r:gz') as archive:
    members=archive.getmembers()
    if sum(m.size for m in members)>2*1024**3:
        raise SystemExit('bundle too large')
    for m in members:
        p=Path(m.name)
        if p.is_absolute() or '..' in p.parts or not (m.isfile() or m.isdir()):
            raise SystemExit('unsafe archive member')
    for m in members:
        if m.isdir(): (target/m.name).mkdir(parents=True,exist_ok=True)
        else:
            path=target/m.name; path.parent.mkdir(parents=True,exist_ok=True)
            with archive.extractfile(m) as src, path.open('xb') as dst:
                import shutil; shutil.copyfileobj(src,dst)
            path.chmod(0o700 if m.mode&0o111 else 0o600)
manifest=json.loads((target/'kilo-runtime-manifest.json').read_text())
for name,digest in manifest['files'].items():
    p=Path(name)
    if p.is_absolute() or '..' in p.parts or hashlib.sha256((target/p).read_bytes()).hexdigest()!=digest:
        raise SystemExit('manifest verification failed')
PY
export TOSUB2_ROOT="$release/tosub2" NODE_EXECUTABLE="$release/node" TOSUB2_PYTHON="$release/python"
"$release/python" -c 'import requests, socks'
"$release/python" -B "$release/kilo-runtime/runtime_service.py" --check
install -d -m 0700 -o "$service_user" -g "$service_user" "$app_dir/config"
if [ ! -s "$app_dir/config/service.token" ]; then
  "$release/python" - "$app_dir/config/service.token" <<'PY'
import os,secrets,sys
fd=os.open(sys.argv[1],os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
with os.fdopen(fd,'w') as f: f.write(secrets.token_urlsafe(48)+'\n')
PY
fi
chown -R "$service_user:$service_user" "$app_dir"
unit="$unit_dir/$service_name.service"
install -d -m 0755 "$unit_dir"
state="$release/.install-state"
install -d -m 0700 "$state"
previous_release=''
had_unit=0
was_enabled=0
had_previous_marker=0
if [ -L "$app_dir/current" ]; then
  previous_release=$(readlink "$app_dir/current")
elif [ -e "$app_dir/current" ]; then
  echo 'current must be a managed release symlink' >&2
  exit 2
fi
if [ -f "$app_dir/previous-release" ]; then
  cp -p "$app_dir/previous-release" "$state/previous-release"
  had_previous_marker=1
fi
if [ -e "$unit" ] || [ -L "$unit" ]; then
  cp -a "$unit" "$state/unit"
  cp -a "$unit" "$unit.previous"
  had_unit=1
fi
if systemctl is-enabled --quiet "$service_name.service"; then was_enabled=1; fi
rollback_armed=1
rollback_install() {
  rollback_failed=0
  systemctl stop "$service_name.service" || rollback_failed=1
  systemctl disable "$service_name.service" >/dev/null 2>&1 || true
  rm -f "$app_dir/current.next" "$app_dir/current.rollback"
  if [ "$had_unit" = 1 ] && [ -n "$previous_release" ]; then
    ln -s "$previous_release" "$app_dir/current.rollback" || rollback_failed=1
    mv -Tf "$app_dir/current.rollback" "$app_dir/current" || rollback_failed=1
  else
    rm -f "$app_dir/current" || rollback_failed=1
  fi
  rm -f "$unit" || rollback_failed=1
  if [ "$had_unit" = 1 ]; then cp -a "$state/unit" "$unit" || rollback_failed=1; fi
  if [ "$had_previous_marker" = 1 ]; then
    cp -p "$state/previous-release" "$app_dir/previous-release" || rollback_failed=1
  else
    rm -f "$app_dir/previous-release" || rollback_failed=1
  fi
  systemctl daemon-reload || rollback_failed=1
  if [ "$had_unit" = 1 ]; then
    if [ "$was_enabled" = 1 ]; then systemctl enable "$service_name.service" >/dev/null || rollback_failed=1; fi
    systemctl restart "$service_name.service" || rollback_failed=1
  fi
  if [ "$rollback_failed" = 0 ]; then
    echo 'KILO_INSTALL_ROLLED_BACK: previous release/unit restored; legacy login service unchanged' >&2
  else
    echo 'KILO_INSTALL_ROLLBACK_FAILED: inspect saved state before another install' >&2
  fi
}
install_exit() {
  result=$?
  trap - EXIT INT TERM
  if [ "$result" -ne 0 ] && [ "$rollback_armed" = 1 ]; then
    set +e
    rollback_install
  fi
  exit "$result"
}
trap install_exit EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if [ -n "$previous_release" ]; then printf '%s\n' "$previous_release" > "$app_dir/previous-release"; fi
ln -sfn "$release" "$app_dir/current.next"
mv -Tf "$app_dir/current.next" "$app_dir/current"
cat > "$unit" <<UNIT
[Unit]
Description=Kilo pure HTTP onboarding executor
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=$service_user
Group=$service_user
WorkingDirectory=$app_dir/current/kilo-runtime
Environment=KILO_BIND=127.0.0.1
Environment=KILO_PORT=$port
Environment=KILO_SERVICE_TOKEN_FILE=$app_dir/config/service.token
Environment=TOSUB2_ROOT=$app_dir/current/tosub2
Environment=NODE_EXECUTABLE=$app_dir/current/node
Environment=TOSUB2_PYTHON=$app_dir/current/python
Environment=KILO_CFSOLVE_BIN=$app_dir/current/kilo-runtime/bin/kilo-cfsolve
Environment=KILO_CFSOLVE_PROFILE=$app_dir/current/kilo-runtime/profiles/safari-26-ios.json
Environment=KILO_SENTINEL_ASSETS=$app_dir/current/kilo-runtime/sentinel_assets
Environment=PYTHONDONTWRITEBYTECODE=1
ExecStart=$app_dir/current/python -B $app_dir/current/kilo-runtime/runtime_service.py
Restart=on-failure
RestartSec=3
KillMode=control-group
LimitCORE=0
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
UMask=0077
StandardOutput=null
StandardError=null
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable "$service_name.service" >/dev/null
systemctl restart "$service_name.service"
"$release/python" - "$app_dir/config/service.token" "$port" "$ready_timeout" <<'PY'
import http.client,json,signal,sys,time
from pathlib import Path

class ReadinessDeadline(Exception):
    pass

def expired(*_):
    raise ReadinessDeadline()

seconds=int(sys.argv[3])
if not 1 <= seconds <= 30:
    raise SystemExit('invalid readiness deadline')
required={'login','resume_identity','connect_subscription','reconcile_subscription','reauthorize_subscription',
          'approve_device','cancel','ack','fixed_proxy','session_ttl','workspace_identity','identity_session',
          'identity_rebind_guard','credential_snapshot','existing_subscription_observation'}
deadline=time.monotonic()+seconds
signal.signal(signal.SIGALRM,expired)
signal.setitimer(signal.ITIMER_REAL,seconds)
try:
    token=Path(sys.argv[1]).read_text().strip()
    while True:
        remaining=deadline-time.monotonic()
        if remaining<=0:
            raise ReadinessDeadline()
        connection=None
        try:
            connection=http.client.HTTPConnection('127.0.0.1',int(sys.argv[2]),timeout=min(1,remaining))
            connection.request('GET','/v1/health',headers={'Authorization':'Bearer '+token})
            response=connection.getresponse()
            raw=response.read(65537)
            data=json.loads(raw) if len(raw)<=65536 else None
            if (response.status==200 and isinstance(data,dict) and data.get('ok') is True
                    and data.get('protocol_version')==1 and isinstance(data.get('instance_id'),str)
                    and data['instance_id'] and isinstance(data.get('capabilities'),list)
                    and required.issubset(data['capabilities'])):
                signal.setitimer(signal.ITIMER_REAL,0)
                print(json.dumps({k:data.get(k) for k in ('protocol_version','version','instance_id','capabilities')}))
                break
        except (OSError,ValueError,TypeError,http.client.HTTPException):
            pass
        finally:
            if connection is not None:
                connection.close()
        remaining=deadline-time.monotonic()
        if remaining>0:
            time.sleep(min(.25,remaining))
except ReadinessDeadline:
    raise SystemExit('KILO_INSTALL_NOT_READY: authenticated health did not become ready within deadline')
finally:
    signal.setitimer(signal.ITIMER_REAL,0)
PY
rollback_armed=0
trap - EXIT INT TERM
