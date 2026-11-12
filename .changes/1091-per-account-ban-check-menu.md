### feat(dashboard): per-account "Check ban" menu item

- The ban check row already offered only "Check all accounts" — a sweep
  over every Antigravity connection. An operator who appealed one
  account had to spend a real request on every other account to learn
  the one verdict that changed.
- Each connection row's Actions menu (antigravity only) now carries a
  "Check ban" item beside the existing "Refresh", calling the same
  per-account `POST /api/connections/{id}/ban-check` endpoint the sweep
  uses, with the model selected in the ban-check bar.
- The verdict lands in the same `banCheckResults` map the row badge
  renders, a "banned" verdict still auto-disables the connection, and a
  "refused" (identity drift) still stops without recording anything.
- Verified with `make vet-svelte` (ratchet unchanged at 73), oxlint on
  the component, and `make web-build` + `make build`.
