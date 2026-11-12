### fix(antigravity): stop sending X-Goog-User-Project identity headers that 403 consumer accounts

- Reverts the header injection from upstream #1089 (`X-Goog-User-Project`,
  `X-Machine-Session-Id`, `X-Client-Name`, `X-Client-Version`) on the
  Antigravity ForwardGemini lane.
- Every consumer-pool connection stores `projectId: aicode-consumers` (the
  default from the OAuth handler). Sending that value as the
  `X-Goog-User-Project` HTTP header asks Google to authorize the caller's
  OAuth token against that internal project via IAM
  (`serviceusage.services.use`), which consumer accounts do not have —
  every request returned `403 PERMISSION_DENIED` on both local and cloud
  immediately after the upstream sync.
- The project ID continues to travel inside the request envelope
  (`WrapForAntigravity`), which is the pre-#1089 behavior that worked.
- `gemini_headers_test.go` now asserts the identity headers are absent on
  the antigravity lane; the gemini-cli non-leakage case is unchanged.
- Verified with `go vet` and `go test ./internal/proxy/...`.
