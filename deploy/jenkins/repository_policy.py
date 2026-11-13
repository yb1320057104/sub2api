"""Fixed current provenance and read-only compatibility for sealed old packages.

Legacy identity is accepted only when a verifier explicitly requests it. New
builds, source evidence publication and Release/GHCR publication use REPOSITORY.
Never rewrite a sealed manifest or its proof to migrate repository ownership.
"""

REPOSITORY = "yb1320057104/sub2api"
LEGACY_REPOSITORY = "pengkang0322/sub2api"


def release_repository(metadata: dict) -> str:
    """Accept either visibility only for the active configured release source."""
    if not isinstance(metadata, dict) or metadata.get("full_name") != REPOSITORY:
        raise RuntimeError("Unexpected release repository identity")
    private = metadata.get("private")
    expected_visibility = "private" if private else "public"
    if type(private) is not bool or metadata.get("visibility", expected_visibility) != expected_visibility:
        raise RuntimeError("Unexpected release repository visibility")
    if metadata.get("archived", False) is not False or metadata.get("disabled", False) is not False:
        raise RuntimeError("Release repository is unavailable")
    return REPOSITORY


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
