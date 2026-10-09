### 🖼️ `muse.png` 404 — Meta Muse tile rendered empty

`muse` is in the provider catalog (`web/src/lib/providers.ts`), and `getIconPath`
resolves a catalog id to `/providers/<id>.png`, so the tile asked for
`web/public/providers/muse.png` — an asset this repo never shipped when the
provider landed with the v0.5.95 sync. Every Muse tile (provider card,
connection row, quota tracker filter, top bar) rendered an empty box.

An audit of the catalog turned up two more dashboard-visible cases with the
same cause: `tinyfish` and `v1m` (`systemone`). All three now carry the artwork
upstream `decolua/9router` ships in `public/providers/` — 128×128 for muse and
v1m, 96×96 for tinyfish. The remaining catalog entries without artwork are not
provider cards: they are header/field names (`user-agent`, `x-requested-with`,
`anthropic-version`), region ids (`cn`, `sgp`, `ams`), or entries that already
have local artwork (`freebuff`, `opencode-zen`, `ollama-search`).

Verified with `bun run build` (the assets land in `web/dist/providers/`), a
`go build` of the single binary, and `GET /providers/{muse,tinyfish,v1m}.png`
against a live instance — all three answer `200 image/png`.
