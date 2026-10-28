### fix(providers): declare Kiro's 1M context window for Claude Opus, Sonnet and Haiku

Kiro served every Claude name with a 200000-token context window and an 8192-token
max output, which is what the `*claude-opus*`/`*claude-sonnet*`/`*claude-haiku*`
substring row in `lookupModelTokenLimits` answers. That row predates 1M-context
general availability: Anthropic serves Opus 4.6 and later, Sonnet 4.6 and later,
and Haiku 5.5 with a 1M window as the default — no beta header, standard pricing —
and 128k max output. A client reading `/v1/models` therefore budgeted a fifth of
the context the gateway would accept, and auto-compacting clients compacted at
200k on a model that had 1M to give.

The figures are declared in `providerTokenLimits` under `kiro` rather than by
editing the substring row, because that row answers for every gateway serving a
Claude name — `antigravity` and `github-copilot` among them — and their limits are
not Kiro's to change. `providerTokenLimits` is also limits-only, so the thinking
format and the capability flags still resolve through the normal chain; a
`providerCapabilities` row would have dragged its thinking shape along.

26 names are listed: Opus 4.6/5/5.5, Sonnet 4.6/5/5.5 and Haiku 5.5, each with its
`-thinking`, `-agentic` and `-thinking-agentic` variants, since the lookup matches
the model name exactly and not by prefix. `claude-sonnet-4` and `claude-haiku-4.5`
are 200k models upstream and are deliberately left on the substring row, which is
already right for them. Haiku 5.5 carries an upstream price premium above 100k
tokens; the window is still the full 1M, and billing is not this table's concern.

Verified: `gofmt` clean, `go build ./...`, `go vet ./internal/providers`, and
`go test ./internal/providers ./internal/handlers/chat` green. Resolution checked
through `GetCapabilitiesDetailForModel` for every added name, and end-to-end against
a restarted gateway — `GET /v1/models` reports `kr/claude-opus-5`,
`kr/claude-sonnet-4.6` and `kr/claude-haiku-5.5` at `1000000`/`128000`, while
`kr/claude-sonnet-4` and `kr/claude-haiku-4.5` stay at `200000`/`8192`.
