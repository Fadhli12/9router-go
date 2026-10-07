# internal/handlers — HTTP Routing & Handlers

> Parent context: read root `AGENTS.md` (provider isolation §3, Go engineering §4, testing §5) before working here. This file adds only handlers-specific guidance.

## Role

Every external HTTP request enters through this package. It owns:

| Subpackage | Lines | Responsibility |
| :--- | ---: | :--- |
| `chat/` | ~31,700 | `/v1/*` LLM routing: combo resolution, fallback/fusion, quota/strike handling, per-provider handlers (`codex`, `gemini`, `mimofree`, `kiro`, `bypass`, `claude` cloaking). |
| `dashboard/` | ~21,000 | `/api/*` admin REST/SSE: connections, combos, proxypools, provider nodes, settings, usage, auth, codex credit reset. |
| `oauth/` | ~10,000 | OAuth token exchange, provider account authorization flows. |
| `media/` | ~9,300 | Audio (STT/TTS), image, embeddings, and multimodal routing (`/v1/*` media endpoints). |
| `shared/`, `sso/` | ~1,100 | Shared handler helpers and single-sign-on routes. |

## Entry Points

- `NewRouter` / `RegisterDashboardRoutes` mount the full route table and middleware stack.
- `chat.ChatHandler` is the central chat routing engine — combo resolution → account selection → forward.
- Handlers coordinate `internal/db`, `internal/proxy`, `internal/providers`, and `internal/translator`; they must **not** own business logic themselves.

## Hard Rules (enforced by existing code & tests)

1. **Never expose debug/metrics publicly.** Dashboard debug surfaces and usage/log streams are auth-gated (`router.go`); do not add a route without matching auth middleware.
2. **SSE streams must not block request dispatch or shutdown.** Slow subscribers are dropped, never queued unbounded (`internal/handlers/consolelog.go`, `internal/handlers/usage_stream.go`). Follow the same pattern for any new SSE emitter.
3. **Transient upstream errors (429/5xx) are not "no project".** Do not cache or persist transient failures as terminal state (`chat/antigravity_project.go`).
4. **Provider isolation is absolute** (root §3). A handler under `chat/` for provider X must never inspect model strings to switch into provider Y. Routing is driven by catalog registration, DB aliases, or `provider/model` wire syntax only.
5. **No `panic()`** in any handler; recover and emit the proper HTTP/SSE error payload (root §4.E).

## Conventions

- **Wrap errors** with `fmt.Errorf("handler.Method: %w", err)`; inspect with `errors.Is`/`errors.As` (root §4.E).
- **Table-driven tests** with `testing.T` + `httptest` (root §5). One file per provider feature, with a matching `*_test.go`.
- **Cognitive complexity ≤ 15**, guard clauses, happy path left-aligned (root §4.D). The chat/dashboard handlers are the most prone to nested `if/else` ladders — decompose into private helpers.
- **Live/network-dependent tests** are tagged (`live_e2e_test.go`) or env-guarded so `go test ./...` stays offline (root §5.C).

## Quick Checks

```bash
rtk go test ./internal/handlers/...
make test-integration   # real router + real DB + fake upstreams (root §5.D)
```
