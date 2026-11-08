# Application source patch

`cluster-ha-r4.patch` consolidates the verified application changes from the
fixed pk.58 base through HA r4. `manifest.json` pins all 4,661 selected base
source files and the before/after SHA-256 hashes of 94 changed files. It also
records the original patch-chain hashes and targeted regression selections.

Features retained in the resulting source:

- PostgreSQL writer / Redis master readiness; Redis Sentinel and TLS.
- API-only and worker-only roles, periodic-background activation control.
- Graceful HTTP and asynchronous billing shutdown with a private drain probe.
- Shared-state admission and billing/concurrency behavior for multiple APIs.
- Multiple allowed Turnstile hostnames without weakening hostname validation.
- Startup migration batching and scheduler shutdown fixes.
- Manual account candy/radar execution on API nodes with periodic tasks disabled;
  the worker retains scheduled execution, with shared database claims and leases.

The existing application frontend, 324 SQL migration files and dependency lockfiles
are retained; no production database is exported. Historical deployment reports,
SSH connection code, IP/domain assignments, runtime receipts and credentials
are intentionally outside this patch.
The legacy `deploy/config.example.yaml` is excluded because it contains a
site-specific hostname example. This package generates deployment configuration
from its own generic templates. Application runtime source is unchanged from
the verified HA r4 chain.

This is source corresponding to the HA r4 behavior, not a claim that rebuilding
produces the old production binary byte for byte. Use `build/prepare_source.py`
and `build/Dockerfile` to make a new site image.

The application is LGPL-3.0; preserve `LICENSE` and the source availability
requirements when distributing modified application images. Original upstream
copyright notices and the unchanged Go module path are retained.
