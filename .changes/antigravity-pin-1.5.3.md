### fix(antigravity): bump the pinned client identity to the captured 1.5.3 fingerprint

Google released Antigravity CLI 1.5.3 while this gateway still presented 1.5.1
with `cl=1014269460`. Every pooled account sent that stale fingerprint, which is
the condition the drift watch halts on — so Antigravity was deliberately
unavailable until the identity could be corrected.

The new values come from a live capture, because they cannot be read any other
way: `cl` is assembled from the client's runtime config, so `strings` on the
installed `agy` executable finds no `cl=` token at all, in 1.4.2 or 1.5.3.

Captured by updating the local client with `agy update` (1.4.2 → 1.5.3) and
proxying `agy models` through mitmproxy, with the proxy's CA trusted by that one
process via `SSL_CERT_FILE` rather than added to a keychain — `agy` is a Go
binary and honours `HTTPS_PROXY` and `SSL_CERT_FILE`, so no system-wide trust
change was needed. `agy models` was chosen as the target because it makes real
requests to `daily-cloudcode-pa.googleapis.com` with the full client identity
while spending no inference quota, so the capture risked no account. Eight
requests reported the same fingerprint:

```
antigravity/cli/1.5.3 (aidev_client; os_type=darwin; arch=arm64; cl=1016078699; auth_method=consumer)
```

Only the version and `cl` are taken from it. `os_type/arch` stay pinned to
`windows/amd64` for two reasons: every pooled account then presents one
identical fingerprint instead of leaking the gateway's own platform, and
`antigravity_project.go` sends `"platform": 5` (WINDOWS_AMD64), which has to
agree with what this header claims.

Verified against the live updater endpoint after the change: released 1.5.3,
pinned 1.5.3, drift false. `go build ./...` and `go vet ./...` clean, provider
suite green.

The halt does not clear itself. It is latched and released only by an operator
through `POST /api/resilience/suspensions/clear`, which is correct: clearing is
the moment accounts start being spent again, so it stays a deliberate act taken
after the identity is known good.
