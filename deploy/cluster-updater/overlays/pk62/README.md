# pk.62 HA adaptation

This profile carries the reviewed pk.61 HA lifecycle into the pk.62 integration
of upstream commit `8545bf9ffac5a870ade18fbe7c4b61a48755c8db`. It leaves the
published r4 overlay and the committed pk.61 profile unchanged. All 96 original
overlay paths have exact input and output hashes; SQL migrations, production
identifiers, credentials and runtime configuration are excluded.

Generate the candidate overlay only after the reviewed source is committed:

```sh
python3 deploy/cluster-updater/prepare_candidate_overlay.py \
  --repo /path/to/private/repository \
  --commit FULL_REVIEWED_COMMIT \
  --previous /path/to/verified/r4-overlay \
  --profile deploy/cluster-updater/overlays/pk62 \
  --output /new/private/pk62-overlay
```

The generated overlay binds that exact commit. Any changed source or patch
requires a new review. The original migration baseline and independent fresh,
upgrade and rollback evidence remain mandatory.

API replicas keep local request consumers. The sole activated worker performs
shared account operations, including API Key fallback evaluation; the provider
wakes its existing joined loop after activation. Admin requests read the worker's
revision-bound status through shared settings and report unavailable or stale
observations. This does not start fallback evaluation on API nodes.

The new fallback and shared-status source files come from the candidate release;
they need no duplicate overlay copies. Provider signatures and the generated
Wire graph remain unchanged. Building this profile alone does not authorize
runtime changes or prove production deployment.
