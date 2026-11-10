### feat(resilience): a version-drift halt switches the provider off in the dashboard, and the toggle cannot switch it back on

The drift watch stopped requests but left the provider looking enabled. The
provider list's toggle reads `isActive`, so a latch that only lived in the
routing layer showed Antigravity as ON while every request was refused with
403 `provider_suspended` — an operator had to discover the halt by trying a
request, and the only visible recovery was toggling all 45 accounts off by
hand.

The watch now performs the same bulk `DeactivateProviderConnections` write the
chat-path halt performs (`suspension_halt.go`): one UPDATE, idempotent, count
reported in the log line. A drift halt is thereby visible where the operator
already looks.

Enabling an account into a drift halt is refused with 409
`provider_halted_on_version_drift` carrying the halt reason (released version,
pinned version, what to patch). A drift halt's exit is patching the pinned
identity, not appealing an account, so no toggle may release it:
`releaseProviderHaltOnEnable` skips drift halts entirely, keeping the release
path that a ban halt needs (re-enable after appeal) intact for the halt type it
belongs to. Both provider-list and provider-detail toggles surface the refusal
message instead of silently refreshing.

Verified: `TestHaltAntigravityOnDriftDeactivatesConnections` (watch write
reaches `isActive`), `TestHaltAntigravityOnDriftNilRepo` (latch alone still
halts without a database), `TestEnablingIntoADriftHaltIsRefused` (409, latch
survives, row stays off), plus the existing release tests re-run green.
