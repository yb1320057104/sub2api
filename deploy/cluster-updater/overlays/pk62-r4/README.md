# pk.62 HA revision 4

This independent profile preserves the immutable formal pk.62 source and the
r2/r3 profiles. It retains both corrected provider startup gates from r3.
The additional production change makes idle ContentModeration consumers observe
an empty queue after Drain closes admission. Queued and running audit tasks keep
their existing execution contexts and timeouts; Drain still joins all consumers.

Regression cases extend the existing HA cleanup lifecycle test file with the real
32-consumer constructor, concurrent Drain callers, retained running/queued records,
and settings-failure retry without discarding accepted work.

No frontend, migration, dependency or formal release artifact changes.
This profile is not a deployment receipt. Same-binary acceptance must enable
SUB2API_GRACEFUL_DRAIN=true and verify the complete HA shutdown path.

The isolated Linux build produced version `0.2.19-pk.62-ha.20261110.1794354599`,
SHA256 `690df004750727fc781a966dd25c1bab7d232219933fc08501d4e14a4a40f56c`
(167297184 bytes). The targeted selection passed 190 top-level tests / 477 pass
events with no failed or skipped cases. The four new Drain cases also passed
three repetitions each; these repeats are not added to the unique test count.
All twelve existing Backup/AutoReset provider regressions passed separately.
Replacing only the moderation implementation with r3 reproduced the empty-queue
Drain deadline failure. An initial incorrect 64-consumer assertion failed before
that reproduction and is retained as a failed attempt, not counted as passing.

All 6088 source inputs are bound to formal commit
`fdec871f36b965dfc272b46bd12eca76ba448300`; compared with r3, only
`content_moderation.go` and the existing `ops_cleanup_overlay_test.go` differ.
The 469 migration files / 442 SQL files and preserved 324-file SQL baseline are
unchanged. Schema evidence was rebound by exact byte equivalence; historical
migration tests were not rerun. The same binary subsequently passed nine isolated
initialization and HA shutdown checks. API, active worker, never-activated worker
and activated delayed worker each exited 0 after TERM with no OOM or restart;
all six ordered drain phases completed. The fixture explicitly enables graceful
drain with a 30-second warning budget and a private Unix probe. Its outer network
is disabled and cleanup passed. Receipt SHA256:
`6bf4390f0c2aa73e1b73fd3a2915a95746b77e6dbb296466277c9005b940b60c`.
This final run also preserves the four actual Id/Name/RestartCount/State
inspection records. The earlier successful run and an intervening isolated
Docker readiness failure remain separate evidence, without double-counting tests.
Production recovery and business acceptance remain separate requirements.
