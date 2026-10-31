### feat(dashboard): group both tunnels' behaviour under one Global tab

- Tunnel Settings gains a **Global** tab holding what the two expose paths share:
  Cloudflare behaviour, Tailscale behaviour, dashboard access, and a new
  visibility switch. Each tunnel's own tab keeps only what is specific to it, so
  the same question is no longer asked twice with the same answer.
- **Allow dashboard access via tunnel** moves off the endpoint card into that tab.
  It governed whichever tunnel was running, so a card owned by one row was the
  wrong place to ask it.
- **Hide unused tunnel rows** is new: while neither tunnel is running, the Tunnel
  and Tailscale rows disappear and return the moment one is enabled. It tidies
  the card without removing the controls. A row counts as in use while it is
  *running*, not merely switched on in settings — a Cloudflare tunnel that is
  enabled but has no connector is not serving anything.
- The three endpoint rows now render through one `endpointBox` snippet with one
  overflow menu each. Local was the odd one out: a bare copy button, and the only
  row whose URL could not be masked at all. Its menu carries Show/Hide URL, Copy
  URL, and Settings — which opens the Global tab, since Local is the row an
  operator always sees and the Global tab is where the shared settings live.
- Clicking the backdrop closes the modal, and Escape closes whichever overlay is
  open. The backdrop handler compares the event target against itself, so a click
  that starts inside the panel does not close the modal on its way out.

**Verified.** `bun run ratchet:svelte` reports 0 unresolved identifiers and 76
errors against a baseline of 77 — one fewer than before, and no new ones.
`bun run build` and `go build ./cmd/9router-go/` pass. Measured in a browser: the
three tabs each render only their own content, the Local menu toggles the mask
round-trip, and Settings from Local lands on Global while Settings from Tunnel
still lands on Cloudflare.
