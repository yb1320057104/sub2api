# syntax=docker/dockerfile:1.7
# Build context MUST be produced by prepare_source.py, not a live checkout.
ARG NODE_IMAGE=node:24-alpine
ARG GO_IMAGE=golang:1.27.3-alpine
ARG RUNTIME_IMAGE=alpine:3.21
ARG POSTGRES_IMAGE=postgres:18-alpine

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS frontend
WORKDIR /src
RUN apk add --no-cache python3
COPY . .
RUN python3 verify-source.py
WORKDIR /src/frontend
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate
RUN --mount=type=cache,id=cluster-pnpm,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
RUN pnpm run build

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS backend
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=cluster.0.1.0
ARG COMMIT=fd80b8314fc2ddb9ec6470755abed0f741b27680
ARG SOURCE_DATE_EPOCH=0
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=off
RUN apk add --no-cache git ca-certificates python3
WORKDIR /src
COPY . .
RUN python3 verify-source.py
COPY --from=frontend /src/backend/internal/web/dist /src/backend/internal/web/dist
WORKDIR /src/backend
RUN --mount=type=cache,id=cluster-gomod,target=/go/pkg/mod go mod download && go mod verify
# Run the recorded HA regression selections before compiling. These tests use
# mocks/local in-process dependencies; no production service is contacted.
RUN --mount=type=cache,id=cluster-gomod,target=/go/pkg/mod \
    --mount=type=cache,id=cluster-gobuild,target=/root/.cache/go-build \
    python3 -c 'import json,subprocess; m=json.load(open("/src/source-build.json")); [subprocess.run(["go","test","-mod=readonly","-p","1","-tags","embed,unit","-count=1","-timeout=180s","-run",t["selection"]]+t["packages"],check=True) for t in m["targeted_tests"]]'
RUN --mount=type=cache,id=cluster-gomod,target=/go/pkg/mod \
    --mount=type=cache,id=cluster-gobuild,target=/root/.cache/go-build \
    BUILD_DATE="$(python3 -c 'import datetime,json,os; e=int(os.environ.get("SOURCE_DATE_EPOCH","0")); m=json.load(open("/src/source-build.json")); print(datetime.datetime.fromtimestamp(e or m["source_date_epoch"],datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))')" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -mod=readonly -tags embed \
      -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}+cluster-r4 -X main.Date=${BUILD_DATE} -X main.BuildType=cluster" \
      -o /out/sub2api ./cmd/server

FROM ${POSTGRES_IMAGE} AS pgclient

FROM ${RUNTIME_IMAGE} AS runtime
ARG VERSION=cluster.0.1.0
LABEL org.opencontainers.image.title="Sub2API Cluster" \
      org.opencontainers.image.version=${VERSION} \
      org.opencontainers.image.source="https://github.com/yb1320057104/sub2api" \
      org.opencontainers.image.licenses="LGPL-3.0" \
      io.sub2api.cluster.capabilities="ha-r4" \
      io.sub2api.cluster.version="0.1.0"
RUN apk add --no-cache ca-certificates tzdata su-exec libpq zstd-libs lz4-libs krb5-libs libldap libedit \
    && addgroup -g 1000 sub2api \
    && adduser -u 1000 -G sub2api -s /bin/sh -D sub2api \
    && mkdir -p /app/data /app/resources /usr/share/sub2api/source \
    && chown -R sub2api:sub2api /app/data /app/resources
COPY --from=pgclient /usr/local/bin/pg_dump /usr/local/bin/psql /usr/local/bin/
COPY --from=pgclient /usr/local/lib/libpq.so.5* /usr/local/lib/
COPY --from=backend --chmod=0555 /out/sub2api /app/sub2api
COPY --from=backend --chown=sub2api:sub2api /src/backend/resources /app/resources
COPY --from=backend --chmod=0555 /src/deploy/docker-entrypoint.sh /app/docker-entrypoint.sh
COPY --from=backend /src/LICENSE /usr/share/sub2api/LICENSE
COPY --from=backend /src/source-build.json /usr/share/sub2api/source/source-build.json
# HA revisions are updated as immutable images. A browser-based updater could
# replace this binary with an upstream build lacking the HA behavior.
ENV SUB2API_DOCKER_SELF_UPDATE=false \
    PK_AUTH_BROWSER_BOOTSTRAP=true \
    PK_AUTH_BROWSER_NO_SANDBOX=true
WORKDIR /app
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/sub2api"]
