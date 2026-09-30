#!/bin/sh
set -e

# Fix data directory permissions when running as root.
# Docker named volumes / host bind-mounts may be owned by root,
# preventing the non-root sub2api user from writing files.
if [ "$(id -u)" = "0" ]; then
    mkdir -p /app/data
    # Use || true to avoid failure on read-only mounted files (e.g. config.yaml:ro)
    chown -R sub2api:sub2api /app/data 2>/dev/null || true
    # Re-invoke this script as sub2api so the flag-detection below
    # also runs under the correct user.
    exec su-exec sub2api "$0" "$@"
fi

# Compatibility: if the first arg looks like a flag (e.g. --help),
# prepend the default binary so it behaves the same as the old
# ENTRYPOINT ["/app/sub2api"] style.
if [ "$#" -eq 0 ]; then
    set -- /app/sub2api
elif [ "${1#-}" != "$1" ]; then
    set -- /app/sub2api "$@"
fi

# This is deliberately opt-in. The normal image and arbitrary custom commands
# retain their existing startup behavior. All installation work happens after
# dropping root, and both the running binary and updater temp/backup files live
# on the same persistent filesystem so the updater can rename them atomically.
if [ "${SUB2API_DOCKER_SELF_UPDATE:-false}" = "true" ] && [ "$1" = "/app/sub2api" ]; then
    image_binary=/app/sub2api
    runtime_dir=/app/data/bin
    runtime_binary=$runtime_dir/sub2api
    seed_marker=$runtime_dir/.image-seed.sha256

    if [ -L "$runtime_dir" ] || [ -L "$runtime_binary" ] || [ -L "$seed_marker" ]; then
        echo "sub2api: self-update paths must not be symbolic links" >&2
        exit 1
    fi
    mkdir -p "$runtime_dir"
    chmod 0755 "$runtime_dir"
    if [ ! -w "$runtime_dir" ]; then
        echo "sub2api: /app/data/bin must be writable by the application user" >&2
        exit 1
    fi
    if [ -e "$runtime_binary" ] && [ ! -f "$runtime_binary" ]; then
        echo "sub2api: persistent executable must be a regular file" >&2
        exit 1
    fi
    if [ -e "$seed_marker" ] && [ ! -f "$seed_marker" ]; then
        echo "sub2api: image seed marker must be a regular file" >&2
        exit 1
    fi

    image_hash=$(sha256sum "$image_binary")
    image_hash=${image_hash%% *}
    installed_seed_hash=
    if [ -f "$seed_marker" ]; then
        installed_seed_hash=$(cat "$seed_marker")
    fi

    # Recover the last running version if the updater was interrupted between
    # renaming the current executable to its backup and installing the new one.
    # A different image seed always wins, including an explicit image rollback.
    if [ "$image_hash" = "$installed_seed_hash" ] && [ ! -e "$runtime_binary" ] &&
        [ -f "$runtime_binary.backup" ] && [ -x "$runtime_binary.backup" ] &&
        [ ! -L "$runtime_binary.backup" ]; then
        mv "$runtime_binary.backup" "$runtime_binary"
    fi

    if [ "$image_hash" != "$installed_seed_hash" ] || [ ! -x "$runtime_binary" ]; then
        seed_stage=$(mktemp -d "$runtime_dir/.image-install.XXXXXX")
        trap 'rm -f "$seed_stage/sub2api" "$seed_stage/image-seed.sha256"; rmdir "$seed_stage" 2>/dev/null || true' 0
        trap 'exit 1' HUP INT TERM
        cp "$image_binary" "$seed_stage/sub2api"
        chmod 0755 "$seed_stage/sub2api"
        printf '%s\n' "$image_hash" > "$seed_stage/image-seed.sha256"
        chmod 0600 "$seed_stage/image-seed.sha256"

        # Publish the executable before its seed marker. If interrupted, the
        # next start repeats installation rather than trusting an incomplete one.
        mv -f "$seed_stage/sub2api" "$runtime_binary"
        rm -f "$runtime_binary.backup"
        mv -f "$seed_stage/image-seed.sha256" "$seed_marker"
        rmdir "$seed_stage"
        trap - 0 HUP INT TERM
        echo "sub2api: installed image binary into persistent self-update directory"
    fi

    shift
    set -- "$runtime_binary" "$@"
fi

exec "$@"
