# syntax=docker/dockerfile:1
# Independent executor image. The application image and its release-bound worker
# are intentionally built and supervised by their existing owners.
FROM rust:1.98.0-bookworm AS cfsolve
ARG CFSOLVE_COMMIT=19efa31656328528457376ae0895a61164b22da0
RUN test "$CFSOLVE_COMMIT" = 19efa31656328528457376ae0895a61164b22da0 \
    && apt-get update && apt-get install -y --no-install-recommends cmake clang libclang-dev git ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && git init /build/cfsolve \
    && git -C /build/cfsolve fetch --depth=1 https://github.com/chris124567/cfsolve.git "$CFSOLVE_COMMIT" \
    && git -C /build/cfsolve checkout --detach FETCH_HEAD \
    && test "$(git -C /build/cfsolve rev-parse HEAD)" = "$CFSOLVE_COMMIT"
COPY tools/kilo-runtime/cfsolve/bridge.rs /build/cfsolve/src/bin/kilo-cfsolve.rs
WORKDIR /build/cfsolve
RUN cargo build --locked --release --bin kilo-cfsolve

FROM node:22.22.2-bookworm-slim AS node
FROM python:3.12.12-slim-bookworm AS executor
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libstdc++6 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm
WORKDIR /opt/kilo/tosub2
COPY tools/reauth-runtime/package.json tools/reauth-runtime/package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund
COPY tools/kilo-runtime/vendor/tosub2/ ./
COPY tools/kilo-runtime/requirements.txt /opt/kilo/requirements.txt
RUN python -m pip install --only-binary=:all: --no-cache-dir -r /opt/kilo/requirements.txt
COPY tools/kilo-runtime/ /opt/kilo/kilo-runtime/
COPY --from=cfsolve /build/cfsolve/target/release/kilo-cfsolve /opt/kilo/kilo-runtime/bin/kilo-cfsolve
COPY --from=cfsolve /build/cfsolve/LICENSE /opt/kilo/kilo-runtime/licenses/cfsolve.LICENSE
# The named context contains exactly the licensed/authorized three pinned JS
# files. It is an explicit external input, never a copied staging directory.
COPY --from=sentinel_assets /sentinel_bridge.js /sentinel_bootstrap.js /sentinel_sdk.js /opt/kilo/kilo-runtime/sentinel_assets/
COPY tools/kilo-runtime/sentinel_curl_fetch.py /opt/kilo/kilo-runtime/sentinel_assets/sentinel_curl_fetch.py
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 \
    TOSUB2_ROOT=/opt/kilo/tosub2 NODE_EXECUTABLE=/usr/local/bin/node TOSUB2_PYTHON=/usr/local/bin/python \
    KILO_BIND=127.0.0.1 KILO_PORT=8099 \
    KILO_SENTINEL_ASSETS=/opt/kilo/kilo-runtime/sentinel_assets \
    KILO_CFSOLVE_PROFILE=/opt/kilo/kilo-runtime/profiles/safari-26-ios.json \
    KILO_CFSOLVE_BIN=/opt/kilo/kilo-runtime/bin/kilo-cfsolve \
    KILO_SERVICE_TOKEN_FILE=/run/kilo/service.token
RUN python -B /opt/kilo/kilo-runtime/runtime_service.py --check \
    && python -B /opt/kilo/kilo-runtime/test_runtime.py -q \
    && node /opt/kilo/kilo-runtime/test_protocol.mjs \
    && groupadd --gid 10001 kilosvc && useradd --system --uid 10001 --gid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin kilosvc \
    && chmod -R a+rX /opt/kilo
USER 10001:10001
WORKDIR /opt/kilo/kilo-runtime
ENTRYPOINT ["/usr/local/bin/python", "-B", "/opt/kilo/kilo-runtime/runtime_service.py"]
