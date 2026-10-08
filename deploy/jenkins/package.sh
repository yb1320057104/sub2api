#!/usr/bin/env bash
# Compilation and packaging only. Source tests run before committing;
# the release pipeline checks these exact artifacts before sealing/publication.
set -euo pipefail
set +x
root=$(pwd -P)
source_dir="$root/source"
output_dir="$root/output"
test -f "$output_dir/manifest.json"
test -n "${DOCKER_HOST:-}" || { echo 'A dedicated build Docker daemon is required.' >&2; exit 1; }
case "$DOCKER_HOST" in tcp://*) ;; *) echo 'Host Docker sockets are not allowed for PK builds.' >&2; exit 1;; esac
read_manifest() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$output_dir/manifest.json" "$1"; }
version=$(read_manifest version)
commit=$(read_manifest commit)
build_type=$(read_manifest build_type)
built_at=$(read_manifest built_at)
image_tag=$(read_manifest image_tag)
go_version=$(awk '$1 == "go" { print $2; exit }' "$source_dir/backend/go.mod")
[[ "$go_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
build_uid=$(id -u)
build_gid=$(id -g)
[[ "$build_uid" != 0 ]] || { echo 'PK source builds must run as a non-root user.' >&2; exit 1; }

# Keep candidate sources away from every formal package cache. The legacy
# manual tag workflow also stays separate from releases checked by source notes.
case "${ACTION:-package}:$build_type" in
    release:release) cache_domain=release ;;
    package:release) cache_domain=manual-release ;;
    package:candidate) cache_domain=candidate ;;
    *) echo 'Unexpected action/build type for PK dependency caches.' >&2; exit 1 ;;
esac
node_image=node:24-bookworm-slim
pnpm_version=9.15.9
cache_prefix="pk-sub2api-build-cache-v1-$cache_domain-linux-amd64-u$build_uid-g$build_gid"
pnpm_cache="$cache_prefix-node24-bookworm-pnpm$pnpm_version-store"
npm_cache="$cache_prefix-node24-bookworm-pnpm$pnpm_version-npm"
go_modules_cache="$cache_prefix-go$go_version-bookworm-modules"
go_build_cache="$cache_prefix-go$go_version-bookworm-cgo0-build"

for cache_volume in "$pnpm_cache" "$npm_cache" "$go_modules_cache" "$go_build_cache"; do
    docker volume create --driver local \
        --label pk.cache.purpose=build-dependencies \
        --label "pk.cache.domain=$cache_domain" --label "pk.cache.key=$cache_volume" \
        "$cache_volume" > /dev/null
    # Reject a colliding name backed by an external driver, bind/NFS options, or
    # another purpose. No operator-supplied volume/path is mounted into a build.
    docker volume inspect "$cache_volume" | python3 -c '
import json,sys
items=json.load(sys.stdin)
if len(items) != 1:
    raise SystemExit("Unexpected dependency cache volume count")
v=items[0]
labels=v.get("Labels") or {}
if not (v.get("Name") == sys.argv[1] and v.get("Driver") == "local"
        and v.get("Scope") == "local" and not v.get("Options")
        and labels.get("pk.cache.purpose") == "build-dependencies"
        and labels.get("pk.cache.domain") == sys.argv[2]
        and labels.get("pk.cache.key") == sys.argv[1]):
    raise SystemExit("Dependency cache volume identity or storage options differ")
' "$cache_volume" "$cache_domain"
done

# Initialize only the four mount roots, never traverse cached paths or execute
# cached content as root. CHOWN alone is sufficient; no DAC_OVERRIDE/FOWNER,
# source bind, credentials, socket, or network is available to this helper.
docker run --rm --platform linux/amd64 --network none --read-only \
    --cpus 1 --memory 64m --pids-limit 32 --security-opt no-new-privileges \
    --cap-drop ALL --cap-add CHOWN --user 0:0 --entrypoint /bin/chown \
    --mount "type=volume,src=$pnpm_cache,dst=/cache/pnpm,volume-nocopy" \
    --mount "type=volume,src=$npm_cache,dst=/cache/npm,volume-nocopy" \
    --mount "type=volume,src=$go_modules_cache,dst=/cache/go-mod,volume-nocopy" \
    --mount "type=volume,src=$go_build_cache,dst=/cache/go-build,volume-nocopy" \
    "$node_image" --no-dereference "$build_uid:$build_gid" \
    /cache/pnpm /cache/npm /cache/go-mod /cache/go-build

resources=(--cpus "${PK_BUILD_CPUS:-2}" --memory "${PK_BUILD_MEMORY:-4g}" --pids-limit 512)
common=(--rm --platform linux/amd64 "${resources[@]}" --security-opt no-new-privileges --cap-drop ALL
    --user "$build_uid:$build_gid" --env HOME=/tmp --mount "type=bind,src=$source_dir,dst=/src")

# The source container cannot see the pipeline checkout, credentials, package
# store, evidence store, host Docker socket, or production data. Only dependency
# caches persist in the dedicated daemon; project node_modules stays in this workspace.
# package.json's build also runs Vitest; invoke its compiler/bundler directly.
docker run "${common[@]}" --workdir /src/frontend \
    --mount "type=volume,src=$pnpm_cache,dst=/cache/pnpm,volume-nocopy" \
    --mount "type=volume,src=$npm_cache,dst=/cache/npm,volume-nocopy" \
    --env npm_config_cache=/cache/npm --env npm_config_prefer_offline=true \
    --env "PK_PNPM_VERSION=$pnpm_version" "$node_image" sh -ec '
    npx --yes --package="pnpm@$PK_PNPM_VERSION" pnpm install --frozen-lockfile \
        --prefer-offline --store-dir=/cache/pnpm --verify-store-integrity \
        --package-import-method=copy --side-effects-cache=false
    npx --yes --package="pnpm@$PK_PNPM_VERSION" pnpm exec vue-tsc -b
    npx --yes --package="pnpm@$PK_PNPM_VERSION" pnpm exec vite build
'
docker run "${common[@]}" --workdir /src/backend \
    --mount "type=volume,src=$go_modules_cache,dst=/cache/go-mod,volume-nocopy" \
    --mount "type=volume,src=$go_build_cache,dst=/cache/go-build,volume-nocopy" \
    --env CGO_ENABLED=0 --env GOOS=linux --env GOARCH=amd64 --env GOMAXPROCS=2 \
    --env GOTOOLCHAIN=local --env GOCACHE=/cache/go-build \
    --env GOMODCACHE=/cache/go-mod --env GOPATH=/tmp/go \
    --env "PK_VERSION=$version" --env "PK_COMMIT=$commit" \
    --env "PK_DATE=$built_at" --env "PK_BUILD_TYPE=$build_type" \
    "golang:${go_version}-bookworm" sh -ec '
        go mod download
        go mod verify
        go build -mod=readonly -p 2 -tags embed -trimpath \
          -ldflags="-s -w -X main.Version=$PK_VERSION -X main.Commit=$PK_COMMIT -X main.Date=$PK_DATE -X main.BuildType=$PK_BUILD_TYPE" \
          -o /src/sub2api ./cmd/server
    '
test -f "$source_dir/sub2api" && test ! -L "$source_dir/sub2api"
tar -C "$source_dir" -czf "$output_dir/$(read_manifest binary_archive)" sub2api

# Reuse the protected packaging recipe and the exact compiled binary.
# The later runtime stage starts only isolated --version/persistence fixtures.
docker build --platform linux/amd64 \
    --file "$root/pipeline/Dockerfile.goreleaser" \
    --label "org.opencontainers.image.source=https://github.com/$PK_REPOSITORY" \
    --label "org.opencontainers.image.version=$version" \
    --label "org.opencontainers.image.revision=$commit" \
    --tag "$image_tag" "$source_dir"
docker image inspect "$image_tag" --format '{{.Id}}' > "$output_dir/image-id.txt"
docker save "$image_tag" | gzip -1 > "$output_dir/$(read_manifest image_archive)"
python3 -c 'import hashlib,pathlib,sys; p=pathlib.Path(sys.argv[1]); print(hashlib.file_digest(p.open("rb"), "sha256").hexdigest())' \
    "$source_dir/sub2api" > "$output_dir/binary-sha256.txt"
rm -- "$source_dir/sub2api"

# Vite clears its output directory. Older source trees may track a placeholder.
# Restore it only when HEAD actually contains it; absent placeholders are normal.
# Git inspection and restoration errors still fail; seal checks all other changes.
tracked_placeholder=$(git -C "$source_dir" ls-tree --name-only HEAD -- backend/internal/web/dist/.keep)
if [[ -n "$tracked_placeholder" ]]; then
    git -C "$source_dir" restore --source=HEAD -- backend/internal/web/dist/.keep
fi
