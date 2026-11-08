# Build the cluster application

This package prepares application source with the HA r4 features and builds a
new immutable Docker image for a new site. It does not publish an application
release, change the source checkout, reuse an existing site's data, or run CI.
Both single-server and three-node deployments use the same application image.

Requirements: Python 3.8+, Git, and Docker with BuildKit. The source preparation
works on Windows and Linux. Build the image on the new primary server; an amd64
build should have at least 4 GiB free RAM, with 6 GiB recommended for tests.
No source repository credentials are copied into the Docker build context.

From this package's root:

```sh
python3 build/prepare_source.py --repo /path/to/sub2api-checkout --output /path/to/app-source
docker build --pull -t sub2api-cluster:0.1.0 /path/to/app-source
docker image inspect sub2api-cluster:0.1.0 --format '{{json .Config.Labels}}'
```

The checkout must contain commit `fd80b8314fc2ddb9ec6470755abed0f741b27680` from
the configured private source repository. Uncommitted files are ignored:
`git archive` reads the fixed commit without modifying the worktree or index.
Alternatively, use an archive exported from that exact commit:

```sh
git -C /path/to/sub2api-checkout archive --format=tar \
  --output=/path/to/base.tar fd80b8314fc2ddb9ec6470755abed0f741b27680
python3 build/prepare_source.py --archive /path/to/base.tar --output /path/to/app-source
```

A GitHub source tarball with one top-level directory is also supported. The
extractor verifies every selected base file against its SHA-256 pin, refuses
links/traversal/duplicates, applies the consolidated patch, and verifies all
resulting files. It exports only pinned application source, tests, lockfiles,
legal notices and generic deployment examples. Runtime directories, Git
configuration, production endpoints, credentials and databases are never copied.
Existing output is accepted only when its complete file inventory and hashes
still match; unknown or modified output is left untouched and rejected.

`source-build.json` records the exact base, patch and per-file source hashes. Its
copy is embedded in the image under `/usr/share/sub2api/source/`. The Docker
build verifies source before downloading dependencies, builds the frontend with
a frozen pnpm lockfile, runs targeted HA regressions, then embeds the frontend
into a CGO-free Go binary. Go modules use the existing `go.sum`. BuildKit caches
contain dependencies only and are not included in the runtime image.

The default application version is `cluster.0.1.0`, not an official upstream
version. The image labels `io.sub2api.cluster.capabilities=ha-r4` and
`io.sub2api.cluster.version=0.1.0` allow the deployment tool to verify an optional
prebuilt image. Labels alone do not establish publisher trust: prebuilt images
must also use a user-trusted registry and immutable digest.

The default toolchain matches this source: Go 1.27.3, Node 24, pnpm 9.15.9,
Alpine 3.21, PostgreSQL client 18. For a release build, record immutable registry
digests using `--build-arg GO_IMAGE=...@sha256:...` and equivalent `NODE_IMAGE`,
`RUNTIME_IMAGE`, `POSTGRES_IMAGE`. Image tags and OS packages can change; this
package verifies repeatable source inputs but does not claim byte-identical
binaries across different toolchain images. Build date defaults to the fixed
base commit timestamp; `SOURCE_DATE_EPOCH` can override it.

The entrypoint keeps the original external browser bootstrap for account
authorization. This installs missing browser packages on first application
start when enabled and network access is available. It does not run account
authorization automatically. Application roles set
`SUB2API_DOCKER_SELF_UPDATE=false`: cluster updates replace the image rather than
letting a browser update replace the HA executable with a different variant.

The image contains the LGPL-3.0 license text. Keep the corresponding prepared
source, patch manifest, build instructions and license available to recipients
of the built image under the applicable license. Do not advertise a private
repository URL as publicly accessible source. This deployment-tool package does
not change the source repository's release/Jenkins policies or create Actions.

Preparation verifies the complete source inventory and rejects modified output.
The build repeats the source check and runs the recorded application regressions.
