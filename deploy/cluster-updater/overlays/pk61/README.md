# pk.61 HA adaptation

This profile adapts the separately pinned r4 HA overlay to the pk.61 application
interfaces. It leaves the published r4 archive, patch and manifests unchanged.
The profile records the exact candidate input and reviewed result of all 96
original overlay paths. It includes no SQL migrations, production identifiers,
credentials, runtime configuration or generated frontend assets.

Generate an overlay from an immutable source commit only:

```sh
python3 deploy/cluster-updater/prepare_candidate_overlay.py \
  --repo /path/to/private/repository \
  --commit FULL_REVIEWED_COMMIT \
  --previous /path/to/verified/r4-overlay \
  --profile deploy/cluster-updater/overlays/pk61 \
  --output /new/private/pk61-overlay
```

Preparation verifies the old overlay identity, candidate file hashes, patch
hash, allowed paths and every resulting file. Any changed candidate file requires
a new review; there is no fuzzy acceptance or conflict fallback. The generated
overlay binds its exact candidate commit, and the builder rejects it for a
different release. This is independent of the migration compatibility receipt:
the original migration pins remain and fresh, upgrade and rollback evidence is
still required for a changed migration set.

The adaptation preserves local request consumers on API replicas, global job
activation on the sole worker, cross-process shared state, orderly producer and
billing queue shutdown, and cleanup of the new pk.61 services before Redis and
PostgreSQL close. Wire output must be regenerated from the complete adapted
source. Compiling this profile does not by itself authorize or prove deployment.
