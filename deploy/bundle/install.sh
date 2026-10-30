#!/bin/sh
# The application image already contains Chromium and every browser library.
# This entrypoint never installs host packages or downloads a browser.
set -eu
umask 077
bundle=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
fail() { printf '%s\n' "$*" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || fail 'Docker is required to run this deployment package.'
docker compose version >/dev/null 2>&1 || fail 'Docker Compose v2 is required.'
command -v sha256sum >/dev/null 2>&1 || fail 'sha256sum is required to verify this package.'
[ "$(uname -m)" = x86_64 ] || fail 'This package supports Linux amd64 only.'
[ "$(uname -s)" = Linux ] || fail 'Run this deployment package on Linux.'
case "${1:-}" in
    --load-only) mode=load; [ "$#" -eq 1 ] || fail 'Usage: install.sh --load-only' ;;
    '') mode=install; target=./sub2api-deployment ;;
    --help|-h)
        printf '%s\n' 'New deployment: ./install.sh [empty deployment directory]' \
            'Existing deployment: ./install.sh --load-only, then update only the application image in your existing Compose project.'
        exit 0 ;;
    -*) fail 'Unknown option. Use --help.' ;;
    *) mode=install; [ "$#" -eq 1 ] || fail 'Supply one deployment directory.'; target=$1 ;;
esac
[ ! -L "$bundle/images" ] || fail 'Image directory must not be a symbolic link.'
for file in image.txt image-id.txt image-config-id.txt docker-compose.yml README.md install.sh images/sub2api.docker.tar.gz SHA256SUMS; do
    [ -f "$bundle/$file" ] && [ ! -L "$bundle/$file" ] || fail 'Missing or symbolic-link package member.'
done
(cd "$bundle" && sha256sum -c SHA256SUMS) || fail 'Package integrity verification failed.'
image=$(cat "$bundle/image.txt")
image_id=$(cat "$bundle/image-id.txt")
config_id=$(cat "$bundle/image-config-id.txt")
printf '%s\n' "$image" | grep -Eq '^ghcr\.io/pengkang0322/sub2api:[0-9][0-9A-Za-z.-]*$' || fail 'Unexpected application image.'
printf '%s\n' "$image_id" | grep -Eq '^sha256:[0-9a-f]{64}$' || fail 'Invalid application image identity.'
printf '%s\n' "$config_id" | grep -Eq '^sha256:[0-9a-f]{64}$' || fail 'Invalid application config identity.'
if [ "$mode" = install ]; then
    [ ! -L "$target" ] || fail 'Deployment directory must not be a symbolic link.'
    mkdir -p -- "$target"
    target=$(CDPATH= cd -- "$target" && pwd -P)
    [ -z "$(ls -A "$target")" ] || fail 'Directory is not empty. Use --load-only for an existing deployment; retain its configuration and data.'
fi
docker load --input "$bundle/images/sub2api.docker.tar.gz"
loaded_id=$(docker image inspect --format '{{.Id}}' "$image")
case "$loaded_id" in "$image_id"|"$config_id") ;; *) fail 'Loaded image differs from the sealed package.' ;; esac
if [ "$mode" = load ]; then
    printf 'Loaded %s with Chromium. Existing containers and data were not changed.\n' "$image"
    exit 0
fi
random_secret() { od -An -N32 -tx1 /dev/urandom | tr -d ' \n'; }
db_secret=$(random_secret); redis_secret=$(random_secret)
jwt_secret=$(random_secret); totp_secret=$(random_secret); admin_secret=$(random_secret)
project_random=$(random_secret)
for value in "$db_secret" "$redis_secret" "$jwt_secret" "$totp_secret" "$admin_secret" "$project_random"; do
    [ "${#value}" -eq 64 ] || fail 'Cannot generate persistent deployment secrets.'
done
cp -- "$bundle/docker-compose.yml" "$target/docker-compose.yml"
{
    printf 'COMPOSE_PROJECT_NAME=sub2api-%s\n' "$(printf '%s' "$project_random" | cut -c1-12)"
    printf 'SUB2API_PK_VERSION=%s\n' "${image##*:}"
    printf 'BIND_HOST=127.0.0.1\nSERVER_PORT=8080\n'
    printf 'POSTGRES_PASSWORD=%s\nREDIS_PASSWORD=%s\n' "$db_secret" "$redis_secret"
    printf 'JWT_SECRET=%s\nTOTP_ENCRYPTION_KEY=%s\n' "$jwt_secret" "$totp_secret"
    printf 'ADMIN_EMAIL=admin@example.com\nADMIN_PASSWORD=%s\n' "$admin_secret"
} > "$target/.env"
chmod 0600 "$target/.env"
mkdir -p "$target/data" "$target/postgres_data" "$target/redis_data"
docker compose --project-directory "$target" --env-file "$target/.env" -f "$target/docker-compose.yml" up -d
printf '%s\n' 'Deployment started at http://127.0.0.1:8080.' \
    'Chromium and its default configuration are included; no separate browser installation is required.' \
    'Initial administrator credentials and persistent encryption keys are in the mode-0600 .env file. Keep this file with your data backups.'
