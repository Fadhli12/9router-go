### Fix: macOS daemon could never recognise its own process, and the OAuth package was untestable

Three test-suite failures on darwin turned out to be two real defects and one
mis-scoped test guard. `go test ./...` now passes with zero failures on
darwin/arm64, and `go vet ./...` is clean.

**`internal/proc` — macOS has no `kern.proc.pathname` sysctl.**
`executable_bsd.go` built for `darwin || freebsd || netbsd || openbsd ||
dragonfly` and read the image path from the `kern.proc.pathname.<pid>` sysctl.
That OID is a FreeBSD/NetBSD interface and simply does not exist on macOS —
`sysctl kern.proc.pathname.<pid>` answers `unknown oid` for every pid, so
`proc.Executable` failed unconditionally there.

This was not only a test failure. `daemon.claim.owner()` verifies a pid-file
claim by comparing the recorded image against the live process's, and a lookup
error makes it fail closed. With `proc.Executable` always erroring, `owner()`
always returned false and `RunningPID()` always returned 0 — so on macOS
`9router-go status`, `stop`, and `restart` could not find a running daemon at
all.

- `darwin` leaves the BSD build tag and gets `executable_darwin.go`, which
  calls libproc's `proc_pidpath` through the `proc_info` syscall directly
  (`PROC_INFO_CALL_PIDINFO` / `PROC_PIDPATHINFO`). No cgo, so
  `CGO_ENABLED=0` still holds for every target in `CROSS_TARGETS`, and no new
  dependency — `golang.org/x/sys` was already a direct require.
- The return value of that syscall is **not** a byte count for this flavor:
  it is 0 on success and the path arrives NUL-terminated in the buffer.
  Treating it as a length reads every success as a failure.
- `trimNUL` moves to the shared `executable.go`, since the darwin and BSD
  paths both receive fixed-size NUL-terminated char arrays.

Verified by running the built binary: `start` → `status` → `restart` →
`logs` → `stop` against an isolated `DATA_DIR`, with `status` correctly
reporting the live pid and the port released after `stop`.

**`internal/proxy/oauth` — the package had not compiled its tests since 2026-10-24.**
`refresher_test.go` is fork-local; upstream has no such file. The merge
`1c4d4a07` took upstream's `refresher.go` — which has no `Default*` symbols and
no `Params.ConnectionID` — while keeping the fork's test, leaving it calling
five identifiers that no longer existed. `go test ./...` reported
`[build failed]` for the package and moved on, so **nothing in `internal/proxy/oauth`
was being tested for over a month.**

- Restores `DefaultOAuthTransport` (pooled, keep-alive, HTTP/2) and
  `DefaultOAuthClient`, plus the `DefaultTransport` / `DefaultClient` aliases.
  Without a shared transport each refresh redoes the TLS handshake against the
  same few OAuth endpoints, for every account, on every expiry.
- Restores `Params.ConnectionID`. `Refresh` keys deduplication on it when set,
  because one stored account can carry several refresh tokens within a turn and
  keying on the token would let the same account issue a second upstream OAuth
  call while the first is still in flight.
- `Refresh` injects the default client into a copy of the caller's `Params`,
  rejects nil params, and checks `ctx.Err()` before the registry lookup so a
  cancelled caller is told that rather than "unknown provider".
- Upstream's `DedupRefresh` keeps owning the collapse and its 10s result
  cache; the connection-keyed path is a thin `dedupRefreshKeyed` alongside it,
  so there is no second deduplication mechanism.

**`internal/browserlogin` — the test guard was narrower than the code.**
`resolveDisplayEnv` returns the environment untouched unless `GOOS == "linux"`,
but the two socket-discovery tests skipped only on Windows, so they asserted
linux-only behaviour on macOS. These fail on `upstream/main` itself; upstream CI
runs `ubuntu-latest` and `windows-latest` only, so it never saw them. The guards
are now `!= "linux"`. `TestResolveDisplayEnvAdvertisesNoMissingSocket` keeps its
Windows-only guard, since asserting that no socket is advertised holds on every
platform.

### Fix: dropped errors on the OAuth save, analytics breakdowns, and the Grok model probe

Five defects found by running `golangci-lint` over the whole tree instead of the
`--new-from-rev` diff. Only findings that change behaviour were taken; the
mechanical categories were left alone deliberately, because rewriting them would
conflict with every open PR — which is the reason the lint job is a ratchet in
the first place.

**A failed OAuth connection save reported success** (`internal/handlers/oauth/pkce.go`).
`persistConnection` opened its upsert with `if err := ...QueryRow(...)`, and that
`err :=` shadowed the outer `err` across *both* branches. The `UPDATE` and the
`INSERT` each assigned the shadowed copy, while `return err` read the outer one —
already nil from the `json.Marshal` check above. A write that failed returned
`nil`, so the login reported a connection it had never stored. The lookup error
is now named `lookupErr`, leaving `err` to carry the write result it was always
meant to carry.

The same function also moved to `QueryRowContext`/`ExecContext`: its single
caller already had the request context, so threading it through closes all four
`noctx` findings there rather than suppressing one, and the OAuth save is now
cancellable with the request.

**Analytics breakdowns could publish partial results as complete**
(`internal/db/compression_analytics.go`). Eight `sql.Rows` loops never checked
`rows.Err()`. These queries are best-effort by design — a query that fails
leaves its breakdown absent while the totals still render — but a loop that
stops part-way has already filled some of the result, and a half-filled
breakdown is the one outcome that reads as complete. Two helpers
(`dropPartialMap`, `dropPartialSlice`) now drop a partial result so it is absent
instead, matching what a failed query already does. The per-model USD estimate
needed its own handling: a partial sum stays positive, so it would be published
as a figure that is simply too low; zeroing it takes the flat-rate fallback that
already exists three lines below.

**A Grok model-probe failure surfaced as `failed to fetch models: 0`**
(`internal/handlers/dashboard/connections.go`). The post-refresh retry assigned
`err` and never read it (`SA4006`), so a transport failure left `status` at 0 and
fell through to the status check instead of reporting the cause.

**A connection locked after an empty upstream body stored an empty message**
(`internal/handlers/chat/fallback.go`). `errMsg` was computed as the fallback for
exactly that case, then never used — both consumers took `errorText` directly.
It is now passed to the two calls that *persist* the message
(`RecordConnectionScopedError`, `LockConnectionRateLimit`), while classification
keeps reading `errorText`: it pattern-matches the real body, and a synthesized
string would match nothing.

**`PersistentLRUStore.Hydrate` swallowed a mid-hydration failure**
(`internal/semanticcache/persistent_store.go`). It returned bare `nil` after the
scan loop; it now returns `rows.Err()` wrapped like the error above it.

Deliberately not fixed: `bodyclose` (3) are false positives — the body is closed
through a `var bodyCloser io.Closer` the linter cannot follow, and one is a
websocket. Three `sqlclosecheck` hits in `internal/db` close `rows` on every
path and only skip `defer`.

A note for whoever reads the lint baseline next: `golangci-lint` caps repeated
messages per linter by default, so a plain `golangci-lint run ./...` reports ~217
issues. The real tree-wide count is **1061** — run it with
`--max-same-issues=0 --max-issues-per-linter=0`. The `141` quoted in
`.golangci.yml` and `.github/workflows/ci.yml` measures the capped view, which is
why that file's three `rowserrcheck` hits were in fact eight. These fixes take
the full count to 1050, and the CI gate (`--new-from-rev`) is clean on the diff.
