### feat(antigravity): halt the provider when Google ships a client release this build does not match

Every pooled Antigravity account presents the fingerprint pinned in
`client_identity.go` — the same `os_type`, `arch` and `cl` on purpose, so the
pool looks like one client. When Google ships a new Antigravity release and this
gateway has not been patched yet, that fingerprint goes stale on every account
at once, and the accounts are what pay for it: an operator's pool was spent on
exactly that event, with appeals filed across all of them simultaneously.

The exposure is specific to this gateway's users. The real Antigravity CLI
carries an auto-updater (`AutoUpdate`, `third_party/jetski/cli/updater`), so a
genuine install never runs a stale version for long. A 9router pool is the only
population that can be stale *and* identical across accounts at the same time.

The gateway now polls the CLI's own auto-updater service — the endpoint found
embedded in the `agy` binary — and halts the Antigravity provider when the
released version is ahead of the pinned one:

```
GET https://antigravity-cli-auto-updater-974169037036.us-central1.run.app
→ Antigravity CLI auto updater is running! Stable Version: 1.5.3. Rolled out to 100%
```

That source was chosen because it is the only one that works on a hosted
gateway. The alternatives all need the real client on the same host: a local
`agy --version` probe, the MITM handler's observed User-Agent, and the
User-Agent on inbound requests are desktop-only — a cloud host has no
Antigravity install, and its accounts are driven headlessly by the gateway
itself.

Behaviour:

- **Newer-only.** A released version strictly ahead of the pin is drift. An
  older one is an operator who has not updated, and halting for that would take
  the provider down for every other account.
- **Fails safe.** The response is prose, not JSON, so the wording can change
  without notice. An unreadable body yields no version, which means no drift and
  no halt — a copy edit on Google's page must not stop the provider. A failed
  probe is likewise not drift.
- **Reuses the suspension latch** rather than adding a second halt: requests get
  `403 provider_suspended`, the latch survives restart, and it clears only
  through `POST /api/resilience/suspensions/clear`.
- **Does not overwrite an existing halt.** A real ban verdict an operator is
  mid-appeal on keeps its reason and its account list; a version note must not
  replace it.
- **Names no account.** A drift refused no credential, so `Accounts` carries a
  `(no account: client version drift)` sentinel instead of a connection id — the
  dashboard shows that list as "appeal these".
- Polled hourly. A release is a human-scale event, and the cost of noticing it
  an hour late is an hour of exposure, against one request an hour forever on the
  deployment target (§4.G).

Version comparison is field-wise numeric, so `1.10.0` is ahead of `1.9.0` rather
than behind it as a string sort would have it, and an unparseable field reports
equal — the safe direction, since an unreadable version must never read as an
upgrade.

Verified with `go build ./...`, `go vet ./...`, the full `internal/providers`
suite including the 1829-pair upstream parity fixture, and `internal/app`. The
fetch path is driven against an `httptest` server, so no test reaches the
network; the parser pins the live response verbatim alongside the reworded,
empty and error-page cases.

Known limits, stated rather than implied: this is a heuristic. It sees a release
only after Google publishes it, so a ban can still land inside the poll window,
and it has not been proven that a stale fingerprint *causes* a ban — the
evidence is the cohort pattern, not an experiment. What it buys is that the
rotation stops within the hour instead of after the fortieth account.

Note for operators: halting is not restoring. Clearing the halt without
patching the pinned identity resumes the stale fingerprint. The pin cannot be
bumped by editing the version alone — `cl` comes from the client's runtime
config, so a bump needs a fresh capture of the real client's headers.
