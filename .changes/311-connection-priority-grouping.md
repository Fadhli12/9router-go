### 🔄 fix(dashboard): group accounts by provider first and set default priority on connection creation

- **Background**: Previously, `providerConnections` rows created via OAuth lacked a `priority` value
  and were stored as `NULL`. In `sortProviderConnections`, `NULL` priorities defaulted to `math.MaxInt`
  and were compared across providers before grouping by provider. Consequently, a newly added account
  was pushed to the very end of the database—past accounts of other providers—and landed isolated on
  subsequent pagination pages.
- **Provider-First Grouping**: `sortProviderConnections` now groups by provider first so accounts of
  the same provider always remain contiguous in Quota Tracker, followed by priority and name.
- **Default Priority**: Added `ResolveDefaultPriority(provider)` in `internal/db/connections.go`
  and updated OAuth connection inserts so new accounts receive an explicit sequential priority.
