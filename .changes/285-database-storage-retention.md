### 📦 feat(settings): database storage & retention profile (observability limits)

- **Background**: `requestDetails` previously retained diagnostic entries with hardcoded limits
  (500,000 rows ceiling, 30 days) and unconditionally logged massive payloads on every call,
  causing SQLite databases to swell up to hundreds of megabytes on user machines.
- **Observability Mode**: Added `observabilityMode` (`full`, `errors_only`, `off`). When set to
  `off`, request detail logging is bypassed completely (<10 MB permanent DB). When set to `errors_only`,
  only failed/fallback transactions are recorded.
- **Configurable Retention**: Added `retentionDays` and `maxRequestRecords` (default 5,000) settings.
- **Maintenance Endpoints**: Added `GET /api/settings/database/stats`, `POST /api/settings/database/prune`,
  and `POST /api/settings/database/vacuum` allowing operators to prune and reclaim free pages back to disk.
