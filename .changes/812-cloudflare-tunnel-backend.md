### feat(tunnel): implement Cloudflare Tunnel, and expose repair-origin

- `HandleTunnelEnable` answered `400 "Cloudflare Tunnel service is not yet
  supported in 9router-go"`, so the dashboard offered a Start Tunnel button that
  could only ever fail. This ports the tunnel package and the handlers that
  drive it.
- `internal/tunnel` gains cloudflared process management (quick, named and
  auto-provisioned modes), the Cloudflare API client for named tunnels, the state
  file, the PID probe, the health check, and the watchdog that rebuilds a dropped
  tunnel.
- `HandleTunnelEnable` now takes the form payload — mode, shortId, customDomain,
  token, cfApiToken, workerUrl — and starts the connector.
  `HandleTunnelStatus` reports the live process state rather than trusting the
  stored URL: a settings flag with no connector is a dead tunnel, and the
  dashboard must say so.
- `POST /api/tunnel/repair-origin` rewrites the ingress rule to point at this
  gateway's own origin. Separate from enable, because an operator who moved the
  gateway to a new port needs the rule fixed without tearing the tunnel down.
- `Menu` gains an `iconOnly` prop: the endpoint rows put the overflow menu beside
  a copy button, and a trigger that differs from it reads as a different kind of
  control.

**Verified.** `go build ./...` and `go test ./internal/tunnel/
./internal/handlers/dashboard/` pass. `TestHandleTunnelEndpoints` previously
asserted that a settings flag plus a stored URL meant *running* — that held only
against the stub, which never started anything. The assertion now checks
`settingsEnabled` for the flag and requires `enabled`/`running` to be false with
no process.

Three unrelated failures exist on the base commit as well and are untouched here:
`browserlogin` browser names, `clientconfig` large-file timing budget, and
`handlers` SPA routes against an unstubbed `web/dist`.
