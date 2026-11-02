#!/bin/bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

cat > "$TEMP_DIR/curl" <<'EOF'
#!/bin/bash
printf '%s\n' "$@" > "$CURL_ARGS_LOG"
env > "${CURL_ARGS_LOG}.env"
cat > "${CURL_ARGS_LOG}.stdin"
EOF
chmod +x "$TEMP_DIR/curl"

mkdir "$TEMP_DIR/home"
cat > "$TEMP_DIR/home/.curlrc" <<'EOF'
url = "https://example.com/collect"
header = "X-Leaked-From-Curlrc: yes"
EOF

run_api_curl() {
    CURL_ARGS_LOG="$1" HOME="$TEMP_DIR/home" PATH="$TEMP_DIR:$PATH" UPDATE_GITHUB_TOKEN="${2:-}" \
        GITHUB_TOKEN="github-fallback" GH_TOKEN="gh-fallback" \
        bash -c 'source <(head -n -1 "$1"); github_api_curl -s "$2"' bash \
        "$ROOT_DIR/deploy/install.sh" "https://api.github.com/repos/yb1320057104/sub2api/releases/latest"
}

run_api_curl "$TEMP_DIR/authenticated" "update-secret"
test "$(head -n 1 "$TEMP_DIR/authenticated")" = '-q'
grep -Fxq -- '--config' "$TEMP_DIR/authenticated"
grep -Fxq -- '-' "$TEMP_DIR/authenticated"
grep -Fxq -- '--globoff' "$TEMP_DIR/authenticated"
grep -Fxq 'header = "Authorization: Bearer update-secret"' "$TEMP_DIR/authenticated.stdin"
if grep -Fq 'update-secret' "$TEMP_DIR/authenticated"; then
    echo "installer exposed the update token in curl argv" >&2
    exit 1
fi
if grep -Eq 'update-secret|github-fallback|gh-fallback' "$TEMP_DIR/authenticated.env"; then
    echo "installer exposed a token in curl environment" >&2
    exit 1
fi
test "$(grep -Fxc 'https://api.github.com/repos/yb1320057104/sub2api/releases/latest' "$TEMP_DIR/authenticated")" -eq 1
if grep -Fq 'example.com/collect' "$TEMP_DIR/authenticated" || grep -Fq 'X-Leaked-From-Curlrc' "$TEMP_DIR/authenticated" ||
    grep -Fq 'example.com/collect' "$TEMP_DIR/authenticated.stdin" || grep -Fq 'X-Leaked-From-Curlrc' "$TEMP_DIR/authenticated.stdin"; then
    echo "installer allowed hostile curl config into authenticated invocation" >&2
    exit 1
fi

if run_api_curl "$TEMP_DIR/anonymous" 2>"$TEMP_DIR/missing-token-error"; then
    echo "private release API unexpectedly allowed an anonymous request" >&2
    exit 1
fi
test ! -e "$TEMP_DIR/anonymous"
grep -Fq 'UPDATE_GITHUB_TOKEN' "$TEMP_DIR/missing-token-error"

assert_unsafe_invocation_rejected() {
    local name=$1
    shift
    rm -f "$TEMP_DIR/$name" "$TEMP_DIR/$name.stdin"
    if CURL_ARGS_LOG="$TEMP_DIR/$name" PATH="$TEMP_DIR:$PATH" UPDATE_GITHUB_TOKEN="update-secret" \
        bash -c 'source <(head -n -1 "$1"); shift; github_api_curl "$@"' bash \
        "$ROOT_DIR/deploy/install.sh" "$@" 2>/dev/null; then
        echo "installer accepted unsafe curl invocation: $name" >&2
        exit 1
    fi
    if [ -e "$TEMP_DIR/$name" ]; then
        echo "installer invoked curl for unsafe request: $name" >&2
        exit 1
    fi
}

assert_unsafe_invocation_rejected non-api -s \
    "https://github.com/yb1320057104/sub2api/releases/download/v1/asset"
assert_unsafe_invocation_rejected mixed-host -s \
    "https://api.github.com/repos/yb1320057104/sub2api/releases/latest" \
    "https://example.com/collect"
assert_unsafe_invocation_rejected multiple-api -s \
    "https://api.github.com/repos/yb1320057104/sub2api/releases/latest" \
    "https://api.github.com/repos/yb1320057104/sub2api/releases"
assert_unsafe_invocation_rejected url-option -s --url \
    "https://example.com/collect" \
    "https://api.github.com/repos/yb1320057104/sub2api/releases/latest"

assert_unsafe_invocation_rejected official-repository -s \
    "https://api.github.com/repos/Wei-Shaw/sub2api/releases/latest"
assert_unsafe_invocation_rejected redirect-option -s -L \
    "https://api.github.com/repos/yb1320057104/sub2api/releases/latest"
assert_unsafe_invocation_rejected traversal -s \
    "https://api.github.com/repos/yb1320057104/sub2api/../../Wei-Shaw/sub2api/releases/latest"
assert_unsafe_invocation_rejected arbitrary-api -s \
    "https://api.github.com/repos/yb1320057104/sub2api/contents/.env"

# Every installer release API request must use the scoped helper.
test "$(grep -c 'github_api_curl .*https://api.github.com/' "$ROOT_DIR/deploy/install.sh")" -eq 3

# Disabled legacy entrypoints must fail before any network request or file setup.
mkdir "$TEMP_DIR/deployment"
for script in install.sh docker-deploy.sh; do
    if (
        cd "$TEMP_DIR/deployment"
        CURL_ARGS_LOG="$TEMP_DIR/blocked-$script-curl" PATH="$TEMP_DIR:$PATH" \
            UPDATE_GITHUB_TOKEN="update-secret" bash "$ROOT_DIR/deploy/$script"
    ) >"$TEMP_DIR/blocked-$script-output" 2>&1; then
        echo "legacy deployment entrypoint unexpectedly succeeded: $script" >&2
        exit 1
    fi
    test ! -e "$TEMP_DIR/blocked-$script-curl"
    grep -Fq 'PK_MAINTENANCE.md' "$TEMP_DIR/blocked-$script-output"
    grep -Fq 'yb1320057104/sub2api' "$TEMP_DIR/blocked-$script-output"
done
test ! -e "$TEMP_DIR/deployment/docker-compose.yml"
test ! -e "$TEMP_DIR/deployment/.env"

# Sourcing the old binary installer must not provide an anonymous asset fallback.
if CURL_ARGS_LOG="$TEMP_DIR/blocked-asset-curl" PATH="$TEMP_DIR:$PATH" \
    bash -c 'source <(head -n -1 "$1"); download_and_extract' bash \
    "$ROOT_DIR/deploy/install.sh" >"$TEMP_DIR/blocked-asset-output" 2>&1; then
    echo "legacy binary downloader unexpectedly succeeded" >&2
    exit 1
fi
test ! -e "$TEMP_DIR/blocked-asset-curl"
grep -Fq 'PK_MAINTENANCE.md' "$TEMP_DIR/blocked-asset-output"

echo "private install entrypoint and GitHub token checks passed"
