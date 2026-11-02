"""Fixed current provenance and read-only compatibility for sealed old packages.

Legacy identity is accepted only when a verifier explicitly requests it. New
builds, source evidence publication and Release/GHCR publication use REPOSITORY.
Never rewrite a sealed manifest or its proof to migrate repository ownership.
"""

REPOSITORY = "yb1320057104/sub2api"
LEGACY_REPOSITORY = "pengkang0322/sub2api"


def package_repository(data: dict, *, allow_legacy: bool = False) -> str:
    repository = data.get("repository")
    allowed = (REPOSITORY, LEGACY_REPOSITORY) if allow_legacy else (REPOSITORY,)
    if repository not in allowed:
        raise RuntimeError("Unexpected package provenance")
    return repository


def image(repository: str) -> str:
    # Keep this helper closed even if another caller passes untrusted metadata.
    if repository not in (REPOSITORY, LEGACY_REPOSITORY):
        raise RuntimeError("Unexpected image provenance")
    return "ghcr.io/" + repository
