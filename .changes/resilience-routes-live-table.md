### fix(dashboard): register the resilience routes on the production router

The provider halt was one-way on a real server. `GET /api/resilience`,
`POST /api/resilience/reset` and `POST /api/resilience/suspensions/clear` were
registered **only** in `dashboard.RegisterRoutes`, which `router.go:250` already
documents as mounted by tests only: *"a route added there alone is reachable in a
unit test and 404s on the real server."*

So the latch could fire but never be released. Its own refusal message tells the
operator to "clear the halt after appealing", and that path answered
`404 page not found`. The latch is persisted through `SetSuspensionStore`, so a
restart did not release it either — an operator who followed the instruction had
no way out short of editing SQLite.

Found by trying to clear a real halt, not by reading the code.

All three are now registered in `SetupServerRouter` as well, and
`TestSetupServerRouter_ResilienceRoutesAreLive` pins them there — the same
production-side guard shape the jailbreak and codex reset-credit routes already
carry for this exact defect class.

The test distinguishes chi's not-found from a handler's own 404: the clear
endpoint legitimately answers `404 provider is not halted` when nothing is
latched, which is the state of a fresh test database, so asserting on the status
alone would have passed against a missing route. Verified by mutation —
removing the registration fails the test with `404 page not found`.
