# internal/integration — Feature Integration Test Suite

> Parent context: read root `AGENTS.md` §5.D (Feature Integration Tests) — it is the authoritative contract for this package.

## Role

Boots the **real** router (`app.ProvideRouter`), real middleware, and a real (temporary) SQLite DB on a real HTTP listener — with every provider call intercepted by a fake `httptest` upstream seeded through `providerConnections.data.baseUrl`. Closes the gap between unit tests (hand-built routers) and production wiring.

- `internal/integration/` — test harness + cases.
- `internal/integration/bootfx/` — boots the full fx graph (`DatabaseModule` + `ServerModule`) **once**, in its own test binary.

## Build Tag

```bash
make test-integration      # go test -tags=integration ./internal/integration/...
```

The `integration` tag keeps `go test ./...` fast; CI runs this suite on every PR.

## Harness Contract (root §5.D — follow exactly)

1. **Use the harness helpers, never raw setup.**
   - `newProviderEnv(t)` — `Env` with one DeepSeek connection aimed at a fake upstream.
   - `newEnv(t)` — bare gateway, token savers pinned off.
   - `env.AddConnection(t, ...)`, `env.AddCombo(t, ...)`.
2. **Every helper takes `*testing.T` as its first argument.** `Env` holds no `*testing.T`.
3. **Never let a test reach the network.** Providers must be `env.NewUpstream(t, ...)` fakes that assert what the gateway sent (`upstream.Last(t).Model(t)`, `.Header`).
4. **Do not boot `app.DatabaseModule` outside `bootfx/`, and only once there.** `db.InitGlobalDatabase` is a process-wide `sync.Once`; a second boot reuses a closed DB.
5. **Assert observable behaviour, not wiring.** Pin a client-visible contract (status code, model received upstream, row written to `usageHistory`). Select rows by id and fail on absence — never rely on a loop that only errors on a match.

## Quick Checks

```bash
make test-integration
make vet-integration
```
