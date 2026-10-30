### fix(executor): CommandCode non-stream requests now fold SSE to JSON

`ForwardCommandcode` previously ignored `req.IsStream` and always returned
SSE (`Content-Type: text/event-stream`). A non-stream client — including
the dashboard's **Test** button — received SSE whose JSON parser found no
`choices` array, and the probe reported:

    Provider returned no completion choices for this model

CommandCode's `/alpha/generate` always returns NDJSON regardless of the
client's `stream` field (the build hardcodes `params.stream = true` to match
upstream's `transformRequest` that sets `body.stream = true`). Upstream's
non-stream downstream handler folds the forced SSE back into one
`chat.completion` JSON using `wrapNdjsonAsOpenAISse`. Kiro already has the
same pattern (`handleKiroNonStream`).

`handleCommandcodeNonStream` now mirrors it: the NDJSON is consumed into an
`sseCollectWriter`, the collected `data: ...\n\n` frames are folded with
`sseToOpenAIJSON`, and the folded JSON is served as `application/json`.
An empty or content-less stream (no delta) correctly returns a 502
(`proxy.NoCompletionInStream`), matching `handleKiroNonStream`'s contract.

**Verification**

- `go test ./internal/handlers/chat/ -run TestForwardCommandcodeRequest_NonStream`
  — asserts `Content-Type: application/json`, parsed `chat.completion` shape
  with the correct `content`, and 502 on a finish-only (empty) stream.
- `go test ./internal/handlers/chat/ -run TestBuildCommandcodeBody_DefaultsMaxTokens`
  — fixed: now emits a `text-delta` so the fold produces non-empty content.
  The test's purpose is asserting `max_tokens` in the upstream body; its fake
  now produces a probeable stream like every other executor test.
- `go test ./internal/proxy/executor/ ./internal/handlers/chat/ ./internal/handlers/media/`
  — 2377 + 481 = 2958 passed.
