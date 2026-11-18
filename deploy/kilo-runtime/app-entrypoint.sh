#!/bin/sh
set -eu

fail() {
    echo "sub2api kilo: $*" >&2
    exit 1
}

# This wrapper belongs only to the explicit Kilo Compose override. The image's
# entrypoint still owns data permissions, browser bootstrap and self-updates.
[ "$(id -u)" = "0" ] || fail "the opt-in wrapper must start as root"
app_uid=$(id -u sub2api) || fail "the image must provide the sub2api user"
[ "$app_uid" != "0" ] || fail "the sub2api user must be non-root"

gid=${KILO_TOKEN_GID:-}
case "$gid" in
    ''|*[!0-9]*|0*) fail "KILO_TOKEN_GID must be a canonical decimal GID" ;;
esac
[ "${#gid}" -le 10 ] || fail "KILO_TOKEN_GID is out of range"
[ "$gid" -ge 1024 ] && [ "$gid" -le 2147483647 ] || fail "KILO_TOKEN_GID must be between 1024 and 2147483647"
[ "$gid" != "10001" ] && [ "$gid" != "$(id -g sub2api)" ] || fail "KILO_TOKEN_GID must be a dedicated group, not an application primary group"

# Never reuse an unrelated image group: that would also grant the application
# access to files owned by that group. Container restarts may reuse our group.
group_gid=$(awk -F: '$1 == "kilo-token" { print $3 }' /etc/group)
group_names=$(awk -F: -v gid="$gid" '$3 == gid { print $1 }' /etc/group)
[ -z "$group_gid" ] || [ "$group_gid" = "$gid" ] || fail "the image kilo-token group has a different GID"
[ -z "$group_names" ] || [ "$group_names" = "kilo-token" ] || fail "KILO_TOKEN_GID is already used by another image group"
if [ -z "$group_gid" ]; then
    addgroup -g "$gid" kilo-token
fi
case " $(id -G sub2api) " in
    *" $gid "*) ;;
    *) addgroup sub2api kilo-token ;;
esac

token_file=/run/kilo/service.token
[ "${KILO_EXECUTOR_TOKEN_FILE:-}" = "$token_file" ] || fail "KILO_EXECUTOR_TOKEN_FILE must point to the shared token mount"
[ -f "$token_file" ] && [ ! -L "$token_file" ] || fail "the shared token must be a regular file"
token_mode=$(stat -c '%u:%g:%a' "$token_file") || fail "cannot inspect the shared token"
[ "$token_mode" = "0:$gid:640" ] || fail "the shared token must be owned by root:KILO_TOKEN_GID with mode 0640"

# su-exec rebuilds supplementary groups from /etc/group. Verify the same
# privilege-drop boundary used by the image, including a real file open.
# Never print or export the bearer value.
su-exec sub2api /bin/sh -ec '
    case " $(id -G) " in
        *" $KILO_TOKEN_GID "*) ;;
        *) exit 1 ;;
    esac
    test -s "$KILO_EXECUTOR_TOKEN_FILE"
    : < "$KILO_EXECUTOR_TOKEN_FILE"
' || fail "sub2api cannot open the shared token after privilege drop"

exec /app/docker-entrypoint.sh "$@"
