# Upstream Sync Analysis — 2026-10-08

> Analisis rekonsiliasi fork antara branch lokal `main` dan `upstream/main`
> (`luqman-v1/9router-go`). Dibuat untuk memutuskan strategi merge yang aman,
> karena kedua sisi sudah divergen jauh dan ada fitur yang saling menggantikan.

## Ringkasan

| Metrik | Nilai |
|---|---|
| Merge base | `0c639b41` |
| Local `main` head | `50709a7f` (~90 commit di atas base) |
| Upstream `main` head | `ab185274` (8 commit di atas base) |
| Commit upstream baru | 8 (lihat tabel di bawah) |
| File yang dihapus upstream (masih ada di lokal) | 24 (internal) |
| File baru upstream | 40+ (internal) |

## Commit upstream yang perlu di-sync (urutan kronologis)

| SHA | Deskripsi | Risiko |
|---|---|---|
| `7ad73b55` | Ollama Cloud dial localhost (#194) | Rendah (✅ sudah di-cherry-pick) |
| `9b553e75` | per-key governance, vault, guardrails (#185) | **TINGGI** |
| `fe166bc9` | restore Add Custom Provider dialog (#195) | Rendah |
| `0d3e2aef` | per-row refresh button (#196) | Rendah |
| `22fa34d5` | page cache fix (#197) | Rendah |
| `5f7ec841` | action bar wrap + probe logic (#198) | Rendah |
| `4d8aa83b` | satukan API Key ke Endpoint & Key (#203) | **TINGGI** |
| `ab185274` | preserve selector cooldown errors (#202) | Rendah |

## Fitur lokal yang DIHAPUS upstream

### Yang harus DIPERTAHANKAN (keputusan user)

| Fitur | File | Diganti upstream? | Referensi (file yang memakai) |
|---|---|---|---|
| Adaptive scoring | `chat/adaptive_router.go` | ❌ Tanpa pengganti | `chat/fallback.go` (`GlobalAdaptiveRouter`) |
| Prompt cache affinity | `chat/prompt_cache.go` | ❌ Tanpa pengganti | `chat/fallback.go` (`SetPromptCacheAffinity`), `translator/response.go`, `responses_usage.go`, `types.go`, `responses_request.go` |
| Cache /v1/models | `chat/models_cache.go` | ❌ Tanpa pengganti | `chat/models_cache.go` (self-contained) |
| Restriksi provider per-key | `chat/provider_restriction.go` | ⚠️ → `chat/access.go` (model-level, beda granularitas) | `middleware/auth.go`, `chat/responses.go`, `chat/chat.go`, `chat/mimofree.go`, `chat/fallback.go` |
| Concurrency limit | `concurrency/limiter.go` | ⚠️ → `middleware/ratelimit.go` (TPM, beda konsep) | `middleware/auth.go`, `db/client.go`, `dashboard/apikeys.go`, `dashboard/routes.go`, `router.go`, `apikeycache/cache.go`, `constants/transport.go` |
| API key cache | `apikeycache/cache.go` | ⚠️ → `keikey/cache.go` (replacement) | `middleware/auth.go`, `db/apikeys.go`, `db/dashboard.go`, `dashboard/settings.go` |
| Kiro canonicalize | `translator/kiro_canonicalize.go` | ⚠️ logic dihapus | `translator/kiro.go` (dipanggil `canonicalizeKiroConversation`) |
| Sanitizer OpenAI | `translator/sanitizer.go` | ⚠️ di-inline | `translator/gemini.go`, `request.go`, `kiro.go` |

### Yang boleh DIHAPUS (ikut upstream)

| Fitur | File | Catatan |
|---|---|---|
| OmniRoute sync | `dashboard/omniroute_sync.go` | Handler `HandleSyncFromOmniRoute`; ref: `dashboard/routes.go:105`, `router.go:251`, `web/src/api/client.ts:750` (`syncFromOmniRoute`), `ProviderDetailView.svelte:2520`, `ProvidersOverviewGrid.svelte:37` |

### Lainnya (bukan fitur, aman ikut upstream)

- `internal/*/AGENTS.md` — dokumen, dihapus upstream (dibuat oleh commit lokal `f4284535`).
- `internal/db/backup.go` + test — kemungkinan dipindah/di-refactor.
- `internal/proxy/oauth/refresher_test.go` — test saja.

## Fitur baru upstream

| Subsystem | File (dir) | Fungsi |
|---|---|---|
| Guardrails | `internal/guardrails/` (8 file) | Deteksi & blokir konten berbahaya (PII/secret) inbound+outbound |
| Vault | `internal/vault/` (2) | Vault kredensial terenkripsi |
| Key cache baru | `internal/keikey/` (3) | API key cache + fingerprint |
| Observability | `internal/observ/` (4) | Metrics Prometheus-style |
| Rate limit | `internal/middleware/ratelimit*.go` (3) | Rate limiting TPM per key |
| Model access | `internal/db/modelaccess.go`, `chat/access.go` | Per-key model allowlist |
| Guardrails DB | `internal/db/guardrails.go`, `vault.go`, `vault_migrate.go` | Persistensi guardrails/vault |

## Konflik yang teramati saat merge/cherry-pick `9b553e75`

14 file konflik (cherry-pick) / 16 (merge penuh):

1. `internal/app/database.go`
2. `internal/db/apikeys.go`
3. `internal/db/dashboard.go`
4. `internal/db/schema.go`
5. `internal/handlers/chat/chat.go`
6. `internal/handlers/chat/fallback.go` ← fix 402 + GlobalAdaptiveRouter + prompt cache
7. `internal/handlers/dashboard/apikeys.go`
8. `internal/handlers/dashboard/routes.go`
9. `internal/handlers/router.go`
10. `internal/middleware/auth.go` ← provider restriction + concurrency + apikey cache
11. `internal/middleware/auth_test.go`
12. `internal/models/types.go`
13. `web/src/api/client.ts` ← fix logout
14. `web/src/components/ApiKeysView.svelte` (delete/modify — dihapus upstream)

## Keputusan strategi yang direkomendasikan

Karena **hampir semua fitur lokal harus dipertahankan** dan upstream menghapus/mengganti sebagian besar, strategi yang aman adalah:

1. **Jangan merge penuh sekaligus** — terlalu banyak konflik konseptual.
2. **Sync bertahap per-commit**, mulai dari yang tidak menyentuh fitur lokal:
   - ✅ `7ad73b55` (Ollama) — sudah aman.
   - `22fa34d5` (page cache) — hanya `db/client.go`.
   - `0d3e2aef` (refresh button) — 1 file svelte.
   - `5f7ec841` (action bar) — frontend.
   - `fe166bc9` (custom provider) — frontend.
   - `ab185274` (selector cooldown) — `fallback.go` (kecil).
3. **Dua commit besar dikerjakan terakhir dengan keputusan eksplisit**:
   - `9b553e75` (security) — perlu re-apply fitur lokal (adaptive_router, prompt_cache, models_cache, provider_restriction, concurrency, apikeycache, kiro_canonicalize, sanitizer).
   - `4d8aa83b` (API keys) — frontend restruktur besar.

## Risiko utama

1. **Fitur lokal terintegrasi dalam** — `GlobalAdaptiveRouter`, `SetPromptCacheAffinity`, `enforceProviderPermission` dipanggil dari `fallback.go`, `chat.go`, `auth.go`, `responses.go` dll. Menghapus file-nya tanpa menghapus referensi = build error.
2. **Tumbukan konseptual** — `provider_restriction.go` (provider-level) vs `access.go` (model-level) vs `guardrails` (konten-level). Ketiganya menyentuh routing seam yang sama.
3. **Kiro fix saya** (`kiroShortToolID`) dibangun di atas `kiro_canonicalize.go` + `sanitizer.go` yang dihapus upstream. Perlu di-port ulang ke struktur kiro.go baru.

## Status saat ini

- Branch kerja: `sync-upstream-2026-10-08` (berisi `b1152295` = Ollama fix saja).
- `main` lokal: `50709a7f` — bersih, 3 fix bekerja (logout, 402, kiro toolUseId).
- Belum ada perubahan yang di-push ke `main` dari proses sync ini.
