# Private release tools and retained upstream helpers

This directory retains upstream release-matrix utilities for source compatibility and review. It is not the release entry point for this private fork. Do not trigger an upstream `release.yml` workflow, use an Actions dry run as private release evidence, or infer that GHCR images were published from the presence of these scripts.

## Current private workflow

The current pk.61 authorization permits local builds followed by isolated verification on the server and publication of the verified package to the private `yb1320057104/sub2api` repository. See [project constraints](../../PROJECT_CONSTRAINTS.md), [maintenance](../../PK_MAINTENANCE.md), and the [pk.61 review](../../docs/PK_UPSTREAM_INTEGRATION_PK61_REVIEW.md) for the effective scope and actual progress. Jenkins remains an available runner when authorized; historical Jenkins-only instructions do not prohibit the current authorized local workflow.

The package must retain its fixed source commit/tree, an unused formal `vX.Y.Z-pk.N` tag in private `main` history, the supported `linux/amd64` target, SHA256 inventory, and all eleven passing `pk-artifact-v2` runtime checks against the exact artifacts being published. The update archive and image must contain the same binary. Existing tags, Release assets, package profiles and evidence must not be rewritten or downgraded.

[`deploy/local-release.py`](../../deploy/local-release.py) publishes already validated packages. It does not build, run Docker, deploy services, or push a registry image. With the required private credential supplied outside tracked files, its entries are:

```bash
python deploy/local-release.py check --artifact-dir <sealed-package-directory>
python deploy/local-release.py publish --artifact-dir <sealed-package-directory>
```

`check` performs the artifact gates and read-only GitHub checks. `publish` stages a private copy, creates a new draft, uploads and verifies each asset, rechecks provenance and publishes. A failure may leave a draft for investigation; the tool does not overwrite or resume an existing Release. The Docker image archive is a Release asset for `docker load`, not evidence of a GHCR push. Check the selected Release's actual inventory before choosing a deployment source.

For the Jenkins runner, follow [its instructions](../../deploy/jenkins/README.md). A formal release uses `ACTION=release`, `SOURCE_REF=vX.Y.Z-pk.N` without `origin/`, and empty `COMMIT_SHA`/`PACKAGE_ID`. Production HA rollout is a separate controlled step under [cluster-updater review](../../docs/PK_CLUSTER_UPDATER_REVIEW.md); publishing a package does not establish that nodes are updated or healthy.

## Scope of the retained helpers

`release_matrix.py` reads GoReleaser target definitions and validates per-target archive metadata. `release-images.sh` contains the upstream image assembly/publication path. Their upstream assumptions, platform matrix, registry operations and workflow artifacts do not override this fork's private source, amd64, authorization or runtime gates.

The helper checks below validate those utilities only. They do not build a private release, execute the eleven runtime checks, certify the current source tree, or establish production capacity:

```bash
python -m pip install -r .github/release-tools/requirements-release.txt
python -m unittest discover -s .github/release-tools -p 'test_release_matrix.py'
bash -n .github/release-tools/release-images.sh
```

No helper tests or publication commands were executed as part of this documentation correction. The actual build, verification and release results are recorded separately when completed.
