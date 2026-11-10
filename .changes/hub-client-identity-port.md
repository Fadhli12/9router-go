### fix(antigravity): port the hub client identity from upstream #4816, ending the CLI fingerprint drift

The gateway presented `antigravity/cli/1.5.3` with a runtime `cl=` token that
only a live mitm capture can observe and that moves on every `agy` CLI release.
Google shipped 1.5.4; the drift watch halted the provider, the ban check was
refused, and the only exit was a fresh capture per release.

Upstream decolua/9router solved this in #4816 by moving to the Hub desktop
fingerprint: `antigravity/hub/2.24.0`, no `cl=` token, serving the Argon lanes
that the legacy 'ide' flavor 404'd on. This ports that identity. The platform
stays pinned to windows/amd64 as before, so every pooled account presents one
identical fingerprint that agrees with the `platform: 5` metadata.

The CLI release watch is not started on the hub lane: its only signal is the
`agy` auto-updater service reporting 1.x CLI releases, which no longer say
anything about the 2.x Hub pin. The watch and halt stay compiled and tested
for the day a Hub release feed exists.

Verified live: `ag/gemini-3.8-flash` answers through the new fingerprint on
both the local gateway and the cloud host; the stale drift halt was cleared and
all connections re-enabled. Full unit suite green. Future identity bumps are a
one-constant change (`AntigravityHubVersion`) — no capture required.
