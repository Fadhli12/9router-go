### 🛡️ feat(security): SSRF protection via safe network dialer

- **Background**: Custom provider nodes and custom proxies previously permitted dialing
  any IP address without restriction, posing a risk of internal network scanning or
  cloud metadata exposure (169.254.169.254).
- **Hardening**: Implemented `NewSafeDialer()` and `NewSafeTransport()` in `internal/proxy/ssrf.go`,
  enforcing destination IP inspection at the socket control level. Dials to loopback, RFC 1918
  private LANs, link-local, and cloud metadata addresses are blocked by default with `ErrSSRFBlocked`.
- **Operational Override**: Operators running local inference engines (such as Ollama on localhost)
  can explicitly permit private networking by setting `ALLOW_PRIVATE_NETWORKING=true`.
