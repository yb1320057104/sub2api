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
resources=(--cpus "${PK_BUILD_CPUS:-2}" --memory "${PK_BUILD_MEMORY:-4g}" --pids-limit 512)
common=(--rm "${resources[@]}" --security-opt no-new-privileges --cap-drop ALL
    --user "$(id -u):$(id -g)" --env HOME=/tmp --mount "type=bind,src=$source_dir,dst=/src")

# The source container cannot see the pipeline checkout, credentials, package
# store, evidence store, host Docker socket, or production data.
# package.json's build also runs Vitest; invoke its compiler/bundler directly.
docker run "${common[@]}" --workdir /src/frontend node:24-bookworm-slim sh -ec '
    npx --yes --package=pnpm@9.15.9 pnpm install --frozen-lockfile
    npx --yes --package=pnpm@9.15.9 pnpm exec vue-tsc -b
    npx --yes --package=pnpm@9.15.9 pnpm exec vite build
'
docker run "${common[@]}" --workdir /src/backend \
    --env CGO_ENABLED=0 --env GOOS=linux --env GOARCH=amd64 --env GOMAXPROCS=2 \
    --env GOTOOLCHAIN=local --env GOCACHE=/tmp/go-build --env GOPATH=/tmp/go \
    --env "PK_VERSION=$version" --env "PK_COMMIT=$commit" \
    --env "PK_DATE=$built_at" --env "PK_BUILD_TYPE=$build_type" \
    "golang:${go_version}-bookworm" sh -ec '
        go build -p 2 -tags embed -trimpath \
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

# Vite clears its output directory, including this tracked empty placeholder.
# Restore only that packaging artifact; seal rejects every other source change.
git -C "$source_dir" restore --source=HEAD -- backend/internal/web/dist/.keep
