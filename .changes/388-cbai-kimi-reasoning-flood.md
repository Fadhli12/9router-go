### fix(cbai): stop CodeBuddy/kimi reasoning from flooding the client stream

Using the `cbai` provider (`codebuddy-intl`) with a Kimi model (`kimi-k2.7`, `kimi-k2.6`, `kimi-k2.5`) can stream `delta.reasoning_content` for tens of seconds. Clients that print every chunk flood the terminal and appear to hang.

`ForwardCodebuddyCN` now pipes its SSE stream through `stripReasoningContentStream` before `execSSEStream`, which:

- drops `delta.reasoning_content` from each chunk **plus the vendor alt shapes** `delta.reasoning` and `delta.reasoning_details` (all three exist in-repo as `translator.OpenAIDelta` fields; without this, an alt shape smuggles the flood past the filter);
- **drops the whole frame** when nothing renderable is left (no `content`, `tool_calls`, `function_call`, `role` or `finish_reason`). Stripping the field alone would re-marshal those frames as `"delta":{}` and still push thousands of empty chunks at the client — the actual hang;
- keeps `tool_calls` and `finish_reason` frames intact, so tool-driven turns still work;
- tolerates a whitespace-padded `[DONE]` terminator;
- closes the upstream reader when the client disconnects, instead of draining a reasoning flood nobody will see.

The non-streaming path is unchanged: `sseToOpenAIJSON` still folds `reasoning_content` into the single aggregated message.

**Verified:**
- `go test ./internal/proxy/...` passes (new cases cover empty-frame dropping, alt `reasoning`/`reasoning_details` shapes, tool-call/finish retention, and padded `[DONE]`).
- Live probe through the gateway (`cbcn/hy3`, `cbcn/hy4-preview`, `reasoning_effort=high`): upstream emits `delta.reasoning_content` in 133/137 (`hy3`) and 152/154 (`hy4-preview`) frames — 97%+ of the stream is thought output the client would print. No `reasoning_details`/`thinking` keys observed. The strip covers both confirmed shapes.
- `golangci-lint run --new-from-rev=HEAD` reports 0 issues; `gofmt` clean.

Note: `cb` is the `cerebras` alias, not a CodeBuddy shortcut — `cb/...` traffic routes to plain `ForwardOpenAI` with no reasoning protection. Cerebras models (`gpt-oss-120b`, `qwen-3-*`) can emit reasoning deltas, but no Cerebras connection exists in this install to probe live.
