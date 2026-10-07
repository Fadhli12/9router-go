# web/ — Svelte 5 Dashboard Frontend

> Parent context: read root `AGENTS.md` §6 (Frontend Engineering & Svelte 5 Standards) — it is the authoritative contract for this directory.

## Stack

**Svelte 5 + Vite 8 + TypeScript + Tailwind CSS v4**, bundled with **Bun**, compiled to `web/dist`, and embedded into the Go binary via `web/embed.go` (`//go:embed all:dist`).

## Structure

| Path | Responsibility |
| :--- | :--- |
| `src/main.ts` | Bootstrap: `mount(App, ...)`, import `index.css` + Material Symbols, register PWA service worker. |
| `src/App.svelte` | Root shell: auth, routing, hydration (`api.getConnections/getCombos/getSystemVersion`), toasts, top-level modals. |
| `src/lib/router.ts` | Custom SPA router (`ActiveTab`, `TAB_ROUTES`, `pathToTab`, `window.history.pushState`). |
| `src/api/client.ts` | Typed HTTP/SSE client — **all** backend requests go through here. |
| `src/lib/providers.ts` | Master `PROVIDER_CATALOG` (specs, IDs, aliases, auth, model defaults). |
| `src/lib/ui/` | Reusable primitives: `Button`, `Input`, `Card`, `Modal`, `ConfirmModal`, `Toggle`, `Badge`, `ThemeToggle`, `Toasts`, `DeprecatedBadge`. |
| `src/components/{connections,combos,media,analytics,quota,proxypools}/` | Feature views + modals. |
| `src/lib/*.ts` | Helpers (`models.ts`, `notifications.ts`, `pwa.ts`, `consoleLog.ts`, …) with co-located `*.test.ts`. |

## Hard Rules (root §6 — strictly mandatory)

1. **Svelte 5 runes only.** `$props()`, `$state`, `$derived`, `$effect`, `$bindable()`, `{@render snippet()}`. **Forbidden:** `export let`, `on:click`, `<slot />` (legacy Svelte 3/4).
2. **All HTTP goes through `web/src/api/client.ts`.** No ad-hoc `fetch` in components.
3. **Use design tokens** from `src/index.css` (`bg-surface`, `text-text-main`, `border-border`, `bg-brand-500`, `rounded-[10px]`, `shadow-[var(--shadow-warm)]`, …). No hardcoded hex.
4. **Icons:** Material Symbols (`<span class="material-symbols-outlined">…</span>`) or Lucide Svelte (`lucide-svelte`).
5. **Respect registry `modelsFetcher` flags** — never inject one provider's models into another via `else if (providerId === ...)` (root §3.D).

## Build & Type-Check Ratchet (root §6.E — read it)

`tsc -b` **cannot parse `.svelte` files**, so a component calling a missing import builds clean then throws `ReferenceError` on click. The `svelte-check` ratchet (`web/scripts/svelte-check-ratchet.ts`) is the only gate that catches it:

- **Unresolved identifiers fail the build** (`Cannot find name`, `Cannot find module`, missing exports).
- **Total type-error count is pinned** in `scripts/svelte-check-baseline.json`; may only shrink.

```bash
make vet-svelte          # run on ANY frontend change
cd web && bun run ratchet:svelte -- --update   # only when the count legitimately drops
```

Never widen the baseline to hide a failure. The `--tsconfig tsconfig.app.json` flag is mandatory.

## Quick Checks

```bash
make web-build           # tsc -b && vite build
make vet-svelte          # svelte-check ratchet
cd web && bun run lint   # oxlint
```
