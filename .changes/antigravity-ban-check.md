### feat(antigravity): per-account ban check, refused while the client identity is stale

An Antigravity account that Google suspended still holds a valid OAuth token and
refreshes it happily, so `probeOAuthConnection` — the "Test connection" path —
reports success on an account that cannot serve a single request. It only asks
whether a token exists. The same is true of refresh and test-all: none of them
send anything Google could answer with the suspension notice, which is why
learning whether an appeal had been granted meant switching one account on,
calling a model by hand, reading the error, and switching it back off, forty
times.

`POST /api/connections/{id}/ban-check` does that in one request:

- the connection is **pinned**, so the verdict belongs to that credential rather
  than to whichever account a strategy would have picked;
- it runs through the **real chat lane**, so credential resolution, OAuth
  refresh, proxy-pool binding and the provider executor are the ones live
  traffic uses;
- the body is `max_tokens: 1`, because a suspension refusal arrives before any
  tokens are generated — enough to learn the verdict without paying for a reply;
- the answer is classified from the upstream's own wording through
  `providers.LooksLikeAccountSuspension`, and reported as `banned`, `lifted` or
  `error`. Anything that is neither an explicit suspension nor an explicit
  success is an error, never `lifted`: reporting a rate limit as lifted would
  invite switching a still-banned account back on.

**The guard is the point of the feature, not a safety rail bolted to it.** A ban
check is itself a real request on the fingerprint this build sends. With that
fingerprint stale, the check can earn the very ban it was asking about — and an
account that has already spent its one Google appeal does not survive a second
verdict. So while the Antigravity release watch reports drift, the endpoint
answers `409` with `state: "refused"` and the two versions, and the dashboard
stops the sweep rather than repeating the refusal per account. A check that can
re-ban the account it clears is worse than no check.

Two mechanisms made this possible without widening anything:

- **A distinct ban-check context marker** (`handlerutil.WithBanCheckContext`)
  rather than a wider `IsProbeContext`. That marker is shared with the
  model-health scheduler, and a ban check has to reach a connection the operator
  switched off — a banned account is left off, so that state *is* the question.
  Granting it to every probe would have the scheduler quietly exercising
  accounts that were deliberately parked. The marker implies the probe marker,
  so the suspension halt is bypassed for it exactly as `connections.go` already
  documents: *"A probe is let through on purpose: it is the operator checking one
  named account to see whether an appeal worked."*
- **The existing pinned-connection path**, with one added exception: a ban check
  may resolve a row whose `IsActive != 1`. A generic probe still refuses that
  case rather than being redirected to another account.

The dashboard row sits below Custom headers and above Sensor Email. The model
picker offers only this provider's own chat models, from the same
`getModelsByProviderId(...).filter(isChatModel)` the view already uses — a check
on a model the account cannot serve fails for the wrong reason, and that failure
reads as a ban. The operator picks a cheap one, because the request is billable.

Verified with `go build ./...` and `go vet ./...` clean, `gofmt` clean, and the
`internal/handlerutil`, `internal/handlers/dashboard` and `internal/handlers/chat`
suites green. 14 dashboard subtests cover the guard in both directions (a newer
release refuses, the pinned version and an older release allow, the `ag` alias is
guarded too), the verdict mapping through the production function rather than a
copy of it, the minimal request body, and the refusal to answer without a wired
engine. Frontend: `bun test` 807 pass, `make vet-svelte` 0 unresolved
identifiers at baseline 73, `bun run build` clean.

Known limit, stated plainly: with the identity currently pinned at 1.5.1 against
a released 1.5.3, the correct behaviour of this button today is to refuse every
click. It becomes useful the moment the pin is patched — and the pin cannot be
bumped by editing the version string alone, because `cl` comes from the client's
runtime config and needs a fresh capture of the real client's headers.
