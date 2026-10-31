### fix(freebuff): send x-freebuff-sse-gzip-recovery header on chat completions

Freebuff upstream (`codebuff.com`) now requires the `x-freebuff-sse-gzip-recovery: 1` header on chat completion requests. Without this header, upstream rejects calls with HTTP 410 `freebuff_update_required` ("This version of Freebuff is out of date. Update to the latest version to keep coding...").

- Add `x-freebuff-sse-gzip-recovery: 1` to `doChat` headers in `ForwardFreebuff`.
- Add test assertion in `TestForwardFreebuff_FullCycle`.
