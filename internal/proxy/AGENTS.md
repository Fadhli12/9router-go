# internal/proxy — Upstream Forwarding & SSE Streaming

> Parent context: read root `AGENTS.md` (provider isolation §3, Go engineering §4) before working here.

## Role

The core reverse-proxy engine: upstream HTTP I/O, SSE stream adaptation, retry/fallback transport, tool-call repair, and provider executors.

| Area | Lines | Responsibility |
| :--- | ---: | :--- |
| `proxy/` (top level) | ~7,000 | Generic forwarding (`ForwardOpenAI`, `ForwardAnthropic`, `ForwardGemini`), `NewFallbackTransport`, SSE copy/scan, retry, stall detection, error classification. |
| `executor/` | ~15,100 | Per-provider executors: `opencode_zen.go`, `freebuff.go`, `claude_*.go`, `codex_*.go`, `gemini.go`, `kiro.go`, `qoder.go`, `trae.go`, `windsurf.go`, `muse.go`, `codebuddy.go`, plus `responses_bridge.go` (Responses API transform) and `stream.go`. |
| `oauth/` | ~1,600 | Provider OAuth token acquisition/refresh flows. |

## Entry Points

- `proxy.NewFallbackTransport(...)` — HTTP round-tripper that falls back to direct dial when a proxy refuses.
- `proxy.ForwardOpenAI/ForwardAnthropic/ForwardGemini` — reverse-proxy forwarders.
- `proxy.StreamWriter` / `AdaptSSEStream` — zero-allocation SSE pipes.
- `executor.Executor` interface + per-provider implementations — the seam where provider-specific protocol lives.

## Hard Rules (enforced by existing code & tests)

1. **Stream, don't buffer** (root §4.A). Only buffer a streaming body when modification/repair is strictly required. Use `bufio.Reader`, `sync.Pool` buffers, `json.RawMessage` for passthrough segments.
2. **Provider isolation is absolute** (root §3). Provider-specific logic (`opencode_zen.go`, `freebuff.go`, etc.) stays inside that executor's execution path. It must never leak into generic routing, sibling executors, or middleware.
3. **Never instantiate an unconfigured `&http.Client{}`** per request. Reuse pooled `http.Transport` with `MaxIdleConnsPerHost` + keep-alives (root §4.A).
4. **Inband SSE error scanning must not false-trigger** on prompt text merely containing `"error"` (`sse_inband.go`). Preserve this guard when touching stream abort logic.
5. **Respect client disconnection** — propagate `r.Context()` to abort in-flight upstream calls (root §4.B).

## Conventions

- **Generic abstractions** for cross-provider mechanisms (`NewFallbackTransport`, `ForwardOpenAI`, `RepairToolCalls`); **dedicated files** for provider quirks (root §3.C).
- Use `singleflight` to collapse duplicate concurrent external fetches (e.g. OAuth refresh).
- Table-driven `testing.T` tests with `httptest.NewServer` fakes — no live network in unit tests (root §5.C).

## Quick Checks

```bash
rtk go test ./internal/proxy/...
```
