# pk.62 HA revision 2

This independent profile keeps the immutable pk.62 application commit and the
original pk62/r4 profiles unchanged. It removes the duplicate backup activation
gate in the provider while retaining the runtime role check. BackupService.Start
continues to own its internal activation gate, disabled API schedule editing,
late-activation refusal, and orderly drain behavior.

The only production source change from the previous pk62 HA output is service
wire.go. Six provider regression cases extend the existing backup_service_test.go.
No migration, frontend, dependency or ordinary release artifact is changed.

Prepare with prepare_candidate_overlay.py using the fixed pk.62 commit,
the verified original r4 overlay, this profile and a new output directory.
The generated source and binary require independent checks and a new HA build
suffix. This profile alone is not a deployment receipt.
