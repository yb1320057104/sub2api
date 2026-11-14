# pk.65 HA adaptation

This profile carries the reviewed pk.64 HA implementation into the pk.65
request-path fixes. It pins the input and output bytes of all 96 original
overlay paths. The candidate supplies the model-route and opaque-history
changes, including their existing test files; they are not duplicated in the
overlay when that file has no HA adaptation.

The three platform-quota cache fills now allow 250 ms for remote Redis writes.
Their quota decisions, unlimited sentinel semantics and write count are
unchanged. Shared-state inflight admission still rejects unavailable or
ambiguous reservations before forwarding. Its new diagnostic records only
bounded `stage`, `cause` and `elapsed_ms` fields, alongside the request logger's
existing context. It does not log the raw Redis error or change the public
error. A lost reservation reply is not retried; the original best-effort cleanup
remains, with no upstream replay or database balance overwrite.

The adaptation preserves the corrected Backup and OpenAI quota auto-reset
provider startup gates, the existing single-worker lifecycle, and all eight
drain support files. The token-guard V2 provider and generated Wire graph retain
the candidate's database/encryptor enrollment wiring. The merged OpenAI handler
retains both the existing HA admission hooks and the candidate's model-route
and history-recovery changes.

Generate a candidate overlay only after committing the reviewed source:

```sh
python3 deploy/cluster-updater/prepare_candidate_overlay.py \
  --repo /path/to/private/repository \
  --commit FULL_REVIEWED_COMMIT \
  --previous /path/to/verified/r4-overlay \
  --profile deploy/cluster-updater/overlays/pk65 \
  --output /new/private/pk65-overlay
```

The manifest intentionally has no candidate commit field. The preparation tool
checks every pinned input against the supplied commit and binds the generated
overlay to that commit. Any source or patch change requires another review.
The original migration baseline and independent fresh, upgrade and rollback
evidence remain required for a release.

The inherited generic r4 patch, pk.64 profile and sealed artifacts are
unchanged. Local preparation verifies patch application, all output hashes,
the four merged intersections, and preservation of provider/drain behavior.
This profile is a local release input; its existence does not establish a
production deployment or prove the cause of earlier billing 503 responses.

## Local verification

The final frozen HA source passed the targeted unit-tagged batches below.
Counts include subtests only in the total pass column.

| Package | Top-level tests | Total pass records |
| --- | ---: | ---: |
| `internal/service` | 81 | 263 |
| `internal/handler` | 47 | 223 |
| `cmd/server` | 9 | 16 |

The combined 137 top-level tests produced 502 pass records with no failures or
skips. Coverage includes unavailable/ambiguous billing reservations, release
and handoff behavior, bounded quota cache fills, model-route exhaustion,
opaque-history recovery, startup gates, readiness and ordered drain cleanup.
Native WebSocket tests cover read failure, missing terminal response and
upstream error events after sending, plus handshake 503 and ordinary-reasoning
controls. The `write_request` guard is reviewed at the actual `WriteJSON` error
boundary; these tests do not simulate a partially written socket frame.

Final batches ran sequentially with `-p 1`, `GOMAXPROCS=2`,
`GOMEMLIMIT=3GiB`, `GOGC=50`, and all Go cache/temp directories on D:. An earlier
preview failed compiling `ent`, and the first final attempt failed compiling
the service test package, both with Windows `VirtualAlloc` error 1455. Those
attempts and the first attempt's passing handler results remain retained as
separate evidence. They are not counted as passing final validation.

Local evidence is under `.runtime/pk65-call-optimization/billing-ha/` in the
workspace parent: `final-2` contains source hashes and patch verification;
`tests-ha-final-service-handler-2` and `tests-ha-final-server-2` contain complete
JSON test logs, exact command/environment records and package summaries.
Commit-bound `prepare_candidate_overlay` validation remains a separate step
after the reviewed source is committed.
