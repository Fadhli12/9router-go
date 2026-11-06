package app

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"go.uber.org/fx"

	"9router/proxy/internal/config"
	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
	"9router/proxy/internal/resilience"
	"9router/proxy/internal/translator"
	"9router/proxy/internal/vault"
)

// DatabaseModule handles database initialization and provides *sql.DB and *db.Repo.
var DatabaseModule = fx.Module("database",
	fx.Provide(
		ProvideDatabase,
		ProvideRepo,
	),
	// An Invoke, not a Provide: the migration produces no value, it is a
	// startup side effect. As a Provide it would be rejected for returning
	// only an error.
	fx.Invoke(MigrateVault),
	fx.Invoke(RestoreProviderSuspensions),
)

// ProvideDatabase initializes the global SQLite database and registers an OnStop lifecycle hook to close it cleanly.
func ProvideDatabase(lc fx.Lifecycle, cfg *config.Config) (*sql.DB, error) {
	if err := db.InitGlobalDatabase(cfg.DatabasePath); err != nil {
		return nil, fmt.Errorf("database init: %w", err)
	}

	conn, err := db.GetConnection()
	if err != nil {
		return nil, fmt.Errorf("database connect: %w", err)
	}

	// Upstream core schema bootstrap (fresh .9router): create the shared
	// tables/indexes when absent, backfill missing columns, and seed the
	// minimal rows. Idempotent — existing user data is never touched.
	// Best-effort like leases below: a shared test binary may hand us a
	// connection bound to a removed temp file (global singleton); a dead
	// connection is a test artifact, not a production schema failure.
	if err := db.EnsureCoreSchema(conn); err != nil {
		_, statErr := conn.Exec("SELECT 1")
		if statErr == nil {
			return nil, fmt.Errorf("database schema: %w", err)
		}
	}
	// The schema bootstrap is the first write, so this is where SQLite creates
	// the -wal and -shm sidecars. They carry the process umask rather than the
	// 0600 OpenDatabase set, and they hold the same page images as the main
	// file — the same plaintext credentials. Re-assert the modes now that they
	// exist.
	db.RestrictSQLiteModes(filepath.Dir(cfg.DatabasePath), cfg.DatabasePath)

	// Best-effort: a row stuck on a provider id the catalog dropped should
	// not block startup, it should just keep failing the way it already was.
	if err := db.MigrateLegacyProviderIDs(conn); err != nil {
		log.Warn("db", "legacy provider id migration failed", "error", err)
	}

	// Cross-process lease table for upstream coordination (Freebuff
	// sessions, future scopes). Idempotent: no-op when already present,
	// invisible to dashboards that do not know the table. Best-effort:
	// a shared test binary may hand us a connection bound to a removed
	// temp file (global singleton); leases then simply stay unavailable.
	if err := db.EnsureUpstreamLeases(conn); err != nil {
		_, statErr := conn.Exec("SELECT 1")
		if statErr == nil {
			return nil, fmt.Errorf("database leases: %w", err)
		}
	}

	repo := db.NewRepo(conn)
	backupDir := filepath.Join(filepath.Dir(cfg.DatabasePath), "backups")
	backupCtx, backupCancel := context.WithCancel(context.Background())
	repo.StartBackupWorker(backupCtx, backupDir, 7, func(err error) {
		log.Warn("db", "scheduled backup failed", "error", err)
	})

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			backupCancel()
			return conn.Close()
		},
	})

	return conn, nil
}

// ProvideRepo provides *db.Repo using the database connection.
func ProvideRepo(conn *sql.DB) *db.Repo {
	repo := db.NewRepo(conn)
	repo.SetVault(vault.NewFromEnv())
	// G26: hydrate thought signatures from SQLite so a signature written before
	// a restart is still replayable on the next tool turn. Fail-open — a broken
	// database only leaves the store process-local, exactly as it was before.
	translator.SetThoughtSignaturePersistence(repo)
	return repo
}

// MigrateVault seals any credential still stored in plaintext.
//
// It is a separate provider so it runs after ProvideRepo — the Repo is what
// carries the vault — and before the server starts accepting traffic, so a
// connection is never dispatched with a half-migrated credential.
//
// Failure is not fatal. The migration takes its own backup first, and a row it
// cannot seal keeps working in plaintext, which is strictly better than a
// gateway that refuses to start.
func MigrateVault(repo *db.Repo, cfg *config.Config) error {
	ctx := context.Background()
	result, err := repo.MigratePlaintextCredentials(ctx, cfg.DatabasePath)
	if err != nil {
		log.Warn("vault", "plaintext credential migration skipped", "error", err)
		return nil
	}
	if result.Sealed > 0 || len(result.Skipped) > 0 {
		log.Info("vault", "credential migration complete", "sealed", result.Sealed, "skipped", len(result.Skipped))
	}
	return nil
}

// RestoreProviderSuspensions installs the latch store and replays the halts it
// holds, before the server accepts traffic.
//
// Without this the latch is process-local: a gateway restarted while a provider
// was halted would resume rotating through the pool and spend the remaining
// accounts on the verdict that halted it.
//
// Failure is not fatal — a gateway that refuses to start is worse than one
// whose halts are only in memory — but it is logged at error level, because the
// operator has lost the restart guarantee until it is fixed.
func RestoreProviderSuspensions(repo *db.Repo) error {
	// The threshold is applied before the replay, not after: the replayed
	// records are judged against it, so reading it second would report the
	// restored halts using the default instead of the operator's setting.
	//
	// A settings row that cannot be read leaves the default of one account in
	// place, which is the safe direction — it halts sooner, never later.
	if settings, err := repo.GetSettings(); err == nil && settings != nil {
		resilience.SetSuspensionThreshold(settings.SuspensionThreshold)
	} else if err != nil {
		log.Warn("resilience", "suspension threshold unreadable, keeping the one-account default", "error", err)
	}
	if err := resilience.SetSuspensionStore(db.NewSuspensionStore(repo)); err != nil {
		log.Error("resilience", "provider suspension latches could not be restored; halts will not survive a restart", "error", err)
		return nil
	}
	if halted := resilience.AllSuspensions(); len(halted) > 0 {
		for _, rec := range halted {
			log.Warn("resilience", "provider is halted by an account suspension; clear it after appealing",
				"provider", rec.Provider, "accounts", rec.Accounts, "since", rec.FirstSeenAt, "upstream", rec.Reason)
		}
	}
	return nil
}
