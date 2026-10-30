#!/bin/sh
set -e

# Normalize the command before deciding whether runtime browser setup is needed.
if [ "$#" -eq 0 ]; then
    set -- /app/sub2api
elif [ "${1#-}" != "$1" ]; then
    set -- /app/sub2api "$@"
fi
browser_runtime=false
if [ "$1" = "/app/sub2api" ]; then
    browser_runtime=true
    for argument in "$@"; do
        case "$argument" in
            --version|-version|-v|version|--help|-help|-h|help) browser_runtime=false; break ;;
        esac
    done
fi

# Fix data directory permissions when running as root.
# Docker named volumes / host bind-mounts may be owned by root,
# preventing the non-root sub2api user from writing files.
if [ "$(id -u)" = "0" ]; then
    mkdir -p /app/data
    # Use || true to avoid failure on read-only mounted files (e.g. config.yaml:ro)
    chown -R sub2api:sub2api /app/data 2>/dev/null || true
    # Web updates replace the executable and restart this same container. Only
    # container recreation loses system packages. Restore missing dependencies
    # at runtime, keeping browsers out of release archives and image layers.
    # Help/version/custom commands and explicitly disabled bootstraps stay offline.
    if [ "$browser_runtime" = "true" ] && [ "${PK_AUTH_BROWSER_BOOTSTRAP:-true}" = "true" ]; then
        if [ -f /etc/alpine-release ]; then
            if [ ! -x /usr/bin/chromium ] || [ ! -x /usr/bin/Xvfb ]; then
                echo "sub2api: installing missing runtime browser dependencies (up to 240 seconds)" >&2
                browser_install_status=0
                timeout 240 apk add --no-cache chromium xvfb >/dev/null 2>&1 || browser_install_status=$?
                if [ "$browser_install_status" -eq 0 ] && [ -x /usr/bin/chromium ] && [ -x /usr/bin/Xvfb ]; then
                    echo "sub2api: runtime browser dependencies are ready" >&2
                else
                    echo "sub2api: browser setup incomplete (exit $browser_install_status); gateway will start, but browser authorization may be unavailable. Check Alpine repository access and run deploy/install-browser.sh." >&2
                fi
            fi
        else
            echo "sub2api: automatic browser setup requires Alpine; provide external browser dependencies for this runtime" >&2
        fi
    fi
    # Containers cannot use the ordinary Chromium sandbox.
    : "${PK_AUTH_BROWSER_NO_SANDBOX:=true}"
    export PK_AUTH_BROWSER_NO_SANDBOX
    # Re-invoke this script as sub2api so the flag-detection below
    # also runs under the correct user.
    exec su-exec sub2api "$0" "$@"
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
