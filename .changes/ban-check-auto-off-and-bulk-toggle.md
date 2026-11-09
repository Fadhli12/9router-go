### feat(dashboard): switch off accounts the ban check finds banned, and toggle a selection in bulk

Two follow-ups to the per-account ban check.

**A banned verdict switches the account off.** Leaving it enabled means the
rotation reaches that credential again and spends a request on one already known
to be refused. The write is narrow on purpose: only an upstream suspension
refusal reads as `banned`, so a rate limit or a transport fault lands in `error`
and the account is left exactly as the operator had it. A failed toggle write
does not discard the verdict — the row keeps its banned badge so the account can
be switched off by hand. The sweep summary reports how many it switched off, so
the check is visibly an action rather than only a report.

**Enable / Disable act on the checkbox selection.** `Select All` and the
per-row checkboxes already drove `Delete Selected`; the same selection now also
drives a bulk active toggle, sitting beside it. This is the operation an
operator actually needs after a sweep: with 40 accounts where three work,
enabling the whole provider to reach those three is the opposite of useful, and
toggling 37 rows by hand is what the ban check was built to end.

Both reuse what was already there rather than adding a parallel path:
`handleToggleSelected` is `handleToggleProvider`'s write scoped to
`selectedConnIds`, and the numeric `isActive` is normalised to a JSON bool by
`api.updateConnection` before it reaches the handler's `rawBody["isActive"].(bool)`
assertion — the same path the per-row toggle uses.

Verified: `make vet-svelte` 0 unresolved identifiers at baseline 73, `bun test`
807 pass, `bun run build` and the Go build clean.
