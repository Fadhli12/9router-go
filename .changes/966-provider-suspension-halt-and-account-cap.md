### feat(resilience): halt a provider when an upstream suspends an account, and bound how many accounts are ever exposed

An operator lost all 40 Antigravity accounts in one cascade: the first account
was suspended, the failover loop cooled it down and rotated to the next, and
every remaining account was spent on the same verdict — each one then needing
its own appeal. Nothing in the gateway treated "this account was punished" as
different from "this account is busy".

Two mechanisms, both off by default except the halt:

- **Suspension halt.** `providers.LooksLikeAccountSuspension` recognises an
  upstream verdict on the account itself (`account suspended`, `account
  disabled/terminated/banned`, the snake_case error codes, and the negations
  that only mention a suspension to rule it out). On a match the rotation stops
  instead of advancing, the provider is latched in `internal/resilience`, and
  the latch has **no timer** — the breaker's own recovery would resume the
  cascade, so only an operator releases it. The latch is persisted in the
  settings row, so a restart cannot resume where it left off.
  `suspensionThreshold` (default 1) is how many distinct accounts must be
  refused first.
- **Account exposure cap.** `maxActiveAccounts` per provider
  (`providerStrategies.<provider>.maxActiveAccounts`, 0 = unlimited) bounds how
  many accounts may be dialled at all. The rest stay standby and are promoted
  only as active slots become unusable, so a 40-account pool behind a cap of 10
  presents 10 identities rather than 40. Promotion reads persistent state
  (cooldowns, model locks) rather than the request's own exclude list: promoting
  on the latter would let one request walk the whole pool, which is the exposure
  the cap exists to bound. A halt beats promotion — the standby accounts are
  what it is protecting.

The halt is enforced at `getBestConnectionWithContext`, the one resolver all 21
chat and media call sites share. A gate in the chat failover alone left the
media handlers (image, search, STT) rotating accounts through their own
`excludeIDs` loops.

Visible where an operator already looks: a halt switches the provider's
connections to `isActive=0`, so the dashboard's per-provider toggle reads off,
and re-enabling from that same toggle is the manual release. A latch that only
lived in memory left the card showing the provider as enabled while every
request was refused. Disabling an account does **not** release the halt, and
`POST /api/resilience/reset` does not either — that button closes breakers an
operator has already fixed, and folding the halt into it would let a routine
click resume the cascade. `GET /api/resilience` lists the halts with the refused
accounts and the upstream reason; `POST /api/resilience/suspensions/clear`
releases one by name.

Clients get `403` with `type: "provider_suspended"` rather than a `503`: nothing
clears the halt on a timer, so a client told to retry would retry forever.

Deliberately excluded from the detection, because a false positive costs every
healthy account in the pool: rate limits and quota (the account is fine,
waiting fixes it), token and grant failures (one credential, repaired by
re-authenticating that account), `not found`, any 429 however it is worded, and
a 5xx that echoes "banned" from an error template.

Also in this change:

- `db.ConnectionModelLockedFrom` reads the connection row's own JSON instead of
  issuing a `SELECT` per account, so judging a 40-account pool costs 0 queries
  rather than 40–80 (§4.A, §4.G).
- The Round Robin toggle no longer deletes the whole per-provider strategy
  entry, which silently discarded the account cap and the proxy-pool binding
  stored beside it.
- The remembered account ids per halted provider are bounded (§4.G).
- The previous merge had dropped the SSRF guard from `connection_probe.go`
  (`NewSafeTransport` missing from all three transport sites,
  `WithAllowLoopback` from the probe context) while `internal/proxy/ssrf.go`
  still defined them; restored, and the `isLoopbackBaseURL` auto-allow that
  granted private-network dials without the operator asking is gone — upstream
  #840 made that an explicit per-connection opt-in.
