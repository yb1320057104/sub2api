# pk.62 HA revision 3

This independent profile retains the immutable pk.62 formal application and
earlier HA profiles. Backup and OpenAI quota automatic-reset providers now call
their internally gated Start methods directly, preserving the runtime role check.
Disabled HA API replicas keep backup schedule CRUD and request-triggered quota
queues; only the active worker runs their periodic jobs. Delayed activation and
orderly shutdown remain governed by each existing lifecycle.

Compared with the original pk62 HA source, production changes affect wire.go
only. Regression cases extend the existing backup and quota-reset test files.
No frontend, migration, dependency or formal release artifact changes.

Prepare from the fixed commit and verified original r4 overlay with this profile.
This profile is not a deployment receipt. The exact binary must complete full
worker initialization, periodic work and normal shutdown in isolated validation.
