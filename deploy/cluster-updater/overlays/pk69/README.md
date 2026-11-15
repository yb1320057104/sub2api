# pk.69 HA independent free Ultrafast policy

This profile preserves the reviewed pk.68 HA patch byte for byte and gives the
pk.69 candidate its own profile identity and source binding. All 96 overlay
input paths still match the pk.68 inputs. The parent profile is pinned by the
SHA256 of its committed manifest; historical profiles and accepted artifacts
remain unchanged.

The independent `free_openai_ultrafast` group policy, its Ent field and migration
343, administrator inputs and DTOs, repository projection, version-36 auth
snapshot, and user-price selection come from the candidate source. None is
replaced by this overlay. The existing HA writer balance recovery, shared
reservation, durable settlement, single active worker, and graceful drain
behavior remain present.

Use `prepare_candidate_overlay.py` with this profile and the immutable candidate
commit, then apply the result to that same commit. Verify that every candidate
SQL migration and every pk.69 policy implementation/test file is preserved byte
for byte, and that the output includes both free-Ultrafast user billing and the
HA settlement/reservation adaptations. A source match is not runtime acceptance.

Migration 343 adds one non-null boolean column with a false default. Existing
SQL migrations are unchanged. The old program ignores the added column;
rollback keeps it and its values. Test fresh install, pk.68 upgrade, repeat
startup, and old-program restart against the upgraded schema in disposable
PostgreSQL. Keep the new policy disabled during mixed-version deployment because
old processes do not implement its discount and can publish version-35 auth
snapshots. Enable it only after both serving API instances run pk.69 and
version-36 snapshots have been observed.

Before deployment, rerun pk.69 metadata, independent-tier billing, and existing HA
lifecycle/reservation regressions against the derived source. Exercise mixed
old/new program settlement against the same disposable DB/Redis and verify one
usage row and one debit per request. The new discount must affect only user
pricing, while actual upstream tier, token counts, and account cost remain
unchanged. None of these checks authorizes rewriting a prior accepted result.
