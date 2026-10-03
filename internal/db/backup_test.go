package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPerformBackupCreatesValidSQLiteFile(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "source.sqlite")
	raw, err := OpenDatabase(dbPath)
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer raw.Close()

	if _, err := raw.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO t (v) VALUES ('hello')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	repo := NewRepo(raw)
	destDir := filepath.Join(tmpDir, "backups")
	dest, err := repo.PerformBackup(destDir, 7)
	if err != nil {
		t.Fatalf("PerformBackup: %v", err)
	}

	if !strings.HasPrefix(filepath.Base(dest), "9router_backup_") || !strings.HasSuffix(dest, ".db") {
		t.Fatalf("unexpected backup name %q", dest)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	check, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer check.Close()
	var v string
	if err := check.QueryRow(`SELECT v FROM t LIMIT 1`).Scan(&v); err != nil {
		t.Fatalf("query backup: %v", err)
	}
	if v != "hello" {
		t.Fatalf("backup content = %q, want %q", v, "hello")
	}
}

func TestPerformBackupPrunesOldFiles(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "source.sqlite")
	raw, err := OpenDatabase(dbPath)
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer raw.Close()
	repo := NewRepo(raw)

	destDir := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	old := filepath.Join(destDir, "9router_backup_20200101_000000.db")
	recent := filepath.Join(destDir, "9router_backup_29990101_000000.db")
	other := filepath.Join(destDir, "unrelated.db")
	for _, p := range []string{old, recent, other} {
		if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.PerformBackup(destDir, 7); err != nil {
		t.Fatalf("PerformBackup: %v", err)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old backup should be pruned, stat err = %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent backup should remain: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated file should remain: %v", err)
	}
}
