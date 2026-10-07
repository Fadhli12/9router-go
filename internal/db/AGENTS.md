# internal/db — SQLite Persistence Layer

> Parent context: read root `AGENTS.md` (Go engineering §4, testing §5) and `DATABASE.md` (schema + migrations) before working here.

## Role

Owns all SQL operations and schema integrity against `~/.9router/db/data.sqlite`, using pure-Go `modernc.org/sqlite` (no cgo). No other package may issue raw SQL.

| Domain file | Responsibility |
| :--- | :--- |
| `connections.go`, `combos.go`, `proxyPools.go`, `providernodes.go` | Dashboard-managed entities. |
| `accounts.go`, `rotation.go`, `accounts_cooldown.go`, `accounts_oauth_lock.go` | Account rotation, cooldowns, per-model quota locks. |
| `usage.go`, `dashboard.go` | Usage history and dashboard aggregation. |
| `apikeys.go`, `settings.go`, `aliases.go`, `deprecations.go` | Client keys, settings, model aliases, deprecations. |
| `schema.go`, `backup.go`, `client.go`, `repos.go`, `health.go` | Migrations, backup/restore, handle lifecycle. |

## Key Symbols

- `InitGlobalDatabase`, `GetDB` — process-wide `sync.Once` DB handle (do **not** call `InitGlobalDatabase` twice in one binary — see root §5.D.5).
- `GetAccounts`, `UpdateAccountCooldown`, `LockAccountForModel` — account rotation + quota locking.
- `RecordUsage`, `GetAggregatedUsage` — usage persistence/aggregation.
- `BackupDatabase`, `RestoreDatabase` — vacuum-based live backup.

## Hard Rules (enforced by existing code & tests)

1. **A success on one model must not unlock locks on other models** (`accounts.go`). Keep per-model locks strictly scoped.
2. **Proxy pool endpoints must not be dialed via raw `http.ProxyURL`** when direct semantics apply (`proxyPools.go`).
3. **Migrations must be idempotent** and never overwrite user data during bootstrap.
4. **Never construct SQL by string concatenation** with user input — use parameterized queries.

## Conventions

- Every exported query function gets a matching `*_test.go` using in-memory `:memory:` SQLite or `internal/dbtest` fixtures; clean up temp DB files (root §5.C).
- Wrap errors with `%w` (`fmt.Errorf("db.GetAccounts: %w", err)`).
- One file per domain concern — keep files ≤ 300 LoC (root §4.D).

## Quick Checks

```bash
rtk go test ./internal/db/...
```
