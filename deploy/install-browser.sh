#!/bin/sh
# Install the authorization browser separately from application release assets.
set -eu

if [ "$#" -ne 1 ]; then
    echo 'Usage: sh deploy/install-browser.sh APPLICATION_CONTAINER' >&2
    exit 2
fi
container=$1
case "$container" in
    ''|-*|*[!a-zA-Z0-9_.-]*) echo 'Invalid container identifier' >&2; exit 2 ;;
esac

# Run inside the application container: host-only installation is not visible
# to its local Chromium/CDP launcher. No application restart is performed.
docker exec --user 0 --interactive "$container" sh -s <<'INSTALL'
set -eu
test -f /etc/alpine-release || { echo 'This installer requires an Alpine application container' >&2; exit 1; }
wrapper=/usr/local/bin/chromium
if [ -L "$wrapper" ] || { [ -e "$wrapper" ] && ! grep -Fq '# PK external Chromium launcher' "$wrapper"; }; then
    echo 'Refusing to overwrite an existing custom Chromium launcher' >&2
    exit 1
fi
apk add --no-cache chromium
test -x /usr/bin/chromium
umask 022
staged=$(mktemp /usr/local/bin/.pk-chromium.XXXXXX)
trap 'rm -f -- "$staged"' EXIT HUP INT TERM
cat > "$staged" <<'LAUNCHER'
#!/bin/sh
# PK external Chromium launcher
# Container deployments cannot use the ordinary host Chromium sandbox.
case "${PK_AUTH_BROWSER_NO_SANDBOX:-true}" in
    1|true|TRUE) exec /usr/bin/chromium --no-sandbox "$@" ;;
    *) exec /usr/bin/chromium "$@" ;;
esac
LAUNCHER
chmod 0755 "$staged"
mv -f -- "$staged" "$wrapper"
INSTALL

docker exec --user sub2api "$container" /usr/local/bin/chromium --version
echo 'External Chromium installed. Application restarts and web updates preserve it.'
echo 'After recreating the container, run this installer again. Existing data and encryption keys are unchanged.'
