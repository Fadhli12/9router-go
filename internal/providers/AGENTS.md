# internal/providers — Provider Catalog & Model Metadata

> Parent context: read root `AGENTS.md` (provider isolation §3 — strictly mandatory) before working here.

## Role

Static + dynamic provider catalog: 120+ provider definitions, model catalogs, aliases, model→format mappings, capabilities, deprecation, and catalog sync. No active network sessions live here — this is metadata, not I/O.

| File | Responsibility |
| :--- | :--- |
| `providers.go`, `registry_models.go`, `registry_aliases.go` | Provider definitions, model catalogs, alias registry. |
| `aliases.go` | Model alias resolution (`ResolveModelAlias`). |
| `model_formats.go`, `model_transport.go`, `model_upstream_id.go` | Which wire format/transport/upstream-id a model uses. |
| `capabilities.go`, `vision_patterns.go`, `thinking_levels.go` | Provider/model capability flags. |
| `catalog_sync.go` | Dynamic remote catalog ingestion. |
| `oauth.go`, `oauth_status.go` | Provider OAuth metadata/status. |
| `deprecation.go`, `errorclassify.go`, `eventstream.go` | Deprecation, error classification, event stream helpers. |

## Key Symbols

- `KnownProviders` — global map of built-in providers, endpoints, default models.
- `ResolveModelAlias` — normalize legacy/custom IDs to canonical provider IDs.
- `GetProviderModelFormat` — resolve OpenAI / Anthropic / Gemini native format.
- `CatalogSync`, `LoadCatalogFromFile` — sync live dynamic catalogs.

## Hard Rules (enforced by existing code & tests)

1. **Never hardcode substring-based provider switching** (e.g. `strings.Contains(model, "muse-spark")` → `opencode`). Routing must come from catalog registration, DB aliases, custom node prefixes, or `provider/model` wire syntax (root §3.B).
2. **Auto-fetched models must not wrongly inherit source-format transports** (`model_formats.go`).
3. **Modality limits (`:8192`, `:32768`) must not stack inappropriately** during catalog ingestion (`catalog_sync.go`).
4. **Zero cross-provider aliasing** — `ag`↔`antigravity`, `oc`↔`opencode` etc. are genuine aliases only; never fall through across providers (root §3.A).

## Conventions

- Keep provider definitions **data-driven** in `registry_*.go`; add capabilities as flags rather than new `if providerId ==` branches.
- Table-driven `testing.T` tests (this package has ~20 test files — follow their fixture style).
- Any new provider ID must be added consistently across `registry_models.go`, `registry_aliases.go`, and the web catalog (`web/src/lib/providers.ts`) — see root §2 mapping.

## Quick Checks

```bash
rtk go test ./internal/providers/...
```
