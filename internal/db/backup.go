package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const backupPrefix = "9router_backup_"

// PerformBackup runs SQLite VACUUM INTO to produce a consistent snapshot of
// the database at destDir, then prunes backups older than retentionDays.
// Returns the path of the newly created backup file.
func (r *Repo) PerformBackup(destDir string, retentionDays int) (string, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("create backup dir %s: %w", destDir, err)
	}

	name := fmt.Sprintf("%s%s.db", backupPrefix, time.Now().Format("20060102_150405"))
	dest := filepath.Join(destDir, name)

	escaped := strings.ReplaceAll(dest, "'", "''")
	if _, err := r.db.Exec(fmt.Sprintf("VACUUM INTO '%s'", escaped)); err != nil {
		return "", fmt.Errorf("vacuum into %s: %w", dest, err)
	}

	if err := pruneOldBackups(destDir, retentionDays); err != nil {
		return dest, fmt.Errorf("prune backups: %w", err)
	}
	return dest, nil
}

// pruneOldBackups removes 9router_backup_*.db files older than retentionDays.
func pruneOldBackups(destDir string, retentionDays int) error {
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", destDir, err)
	}
	cutoff := time.Duration(retentionDays) * 24 * time.Hour
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), backupPrefix) || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > cutoff {
			if err := os.Remove(filepath.Join(destDir, e.Name())); err != nil {
				return fmt.Errorf("remove %s: %w", e.Name(), err)
			}
		}
	}
	return nil
}

// StartBackupWorker runs PerformBackup every 24h in the background until ctx
// is cancelled. The first backup runs immediately.
func (r *Repo) StartBackupWorker(ctx context.Context, destDir string, retentionDays int, onErr func(error)) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			if _, err := r.PerformBackup(destDir, retentionDays); err != nil && onErr != nil {
				onErr(err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
