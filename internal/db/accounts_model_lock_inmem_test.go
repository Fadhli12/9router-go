package db

import (
	json "encoding/json/v2"
	"testing"
	"time"
)

// ConnectionModelLockedFrom replaces a per-row SELECT with a read of JSON the
// caller already holds, so it has to agree with IsConnectionModelLocked on
// every shape. A divergence here would silently change which accounts the
// selector considers parked.
func TestConnectionModelLockedFromMatchesTheQueryingPath(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepo(database)

	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)

	tests := []struct {
		name  string
		data  string
		model string
		want  bool
	}{
		{
			name:  "active lock",
			data:  `{"apiKey":"sk-1","` + modelLockPrefix + `deepseek-chat":"` + future + `"}`,
			model: "deepseek-chat",
			want:  true,
		},
		{
			name:  "expired lock",
			data:  `{"apiKey":"sk-1","` + modelLockPrefix + `deepseek-chat":"` + past + `"}`,
			model: "deepseek-chat",
			want:  false,
		},
		{
			name:  "lock for a different model",
			data:  `{"apiKey":"sk-1","` + modelLockPrefix + `other-model":"` + future + `"}`,
			model: "deepseek-chat",
			want:  false,
		},
		{
			name:  "no lock key at all",
			data:  `{"apiKey":"sk-1"}`,
			model: "deepseek-chat",
			want:  false,
		},
		{
			name:  "lock cleared to null",
			data:  `{"apiKey":"sk-1","` + modelLockPrefix + `deepseek-chat":null}`,
			model: "deepseek-chat",
			want:  false,
		},
		{
			name:  "lock is an empty string",
			data:  `{"apiKey":"sk-1","` + modelLockPrefix + `deepseek-chat":""}`,
			model: "deepseek-chat",
			want:  false,
		},
		{
			name:  "lock is not a timestamp",
			data:  `{"apiKey":"sk-1","` + modelLockPrefix + `deepseek-chat":"soon"}`,
			model: "deepseek-chat",
			want:  false,
		},
		{
			name:  "lock is a number",
			data:  `{"apiKey":"sk-1","` + modelLockPrefix + `deepseek-chat":1762400000}`,
			model: "deepseek-chat",
			want:  false,
		},
		{
			name:  "malformed data blob",
			data:  `{not json`,
			model: "deepseek-chat",
			want:  false,
		},
		{
			name:  "empty data blob",
			data:  ``,
			model: "deepseek-chat",
			want:  false,
		},
		{
			name:  "empty model name",
			data:  `{"apiKey":"sk-1","` + modelLockPrefix + `deepseek-chat":"` + future + `"}`,
			model: "",
			want:  false,
		},
		{
			name:  "model name containing a slash",
			data:  `{"apiKey":"sk-1","` + modelLockPrefix + `vendor/model-1":"` + future + `"}`,
			model: "vendor/model-1",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConnectionModelLockedFrom(tt.data, tt.model); got != tt.want {
				t.Errorf("ConnectionModelLockedFrom() = %v, want %v", got, tt.want)
			}

			// The querying path is the reference. An empty model is not a case
			// it accepts (callers guard it upstream), and a blob SQLite would
			// never store cannot be seeded, so those two are checked above only.
			if tt.model == "" || tt.data == `{not json` || tt.data == `` {
				return
			}
			connID := "conn-equiv"
			if _, err := database.Exec(`DELETE FROM providerConnections WHERE id = ?`, connID); err != nil {
				t.Fatalf("clear connection: %v", err)
			}
			if _, err := database.Exec(
				`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
				VALUES (?, 'deepseek', 'apikey', 'Equiv', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
				connID, tt.data); err != nil {
				t.Fatalf("seed connection: %v", err)
			}
			queried, err := repo.IsConnectionModelLocked(connID, tt.model)
			if err != nil {
				t.Fatalf("IsConnectionModelLocked: %v", err)
			}
			if queried != tt.want {
				t.Fatalf("fixture drifted: IsConnectionModelLocked() = %v, want %v", queried, tt.want)
			}
			if got := ConnectionModelLockedFrom(tt.data, tt.model); got != queried {
				t.Errorf("in-memory read = %v but the querying path says %v", got, queried)
			}
		})
	}
}

// A lock written by the normal writer must be readable by the in-memory path,
// so the two cannot drift when the storage format changes.
func TestConnectionModelLockedFromReadsAWrittenLock(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepo(database)

	data, _ := json.Marshal(map[string]any{"apiKey": "sk-1"})
	if _, err := database.Exec(
		`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
		VALUES ('conn-written', 'deepseek', 'apikey', 'Written', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		string(data)); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	if err := repo.LockConnectionModel("conn-written", "deepseek-chat", 3600, 1); err != nil {
		t.Fatalf("LockConnectionModel: %v", err)
	}

	var raw string
	if err := database.QueryRow(`SELECT data FROM providerConnections WHERE id = 'conn-written'`).Scan(&raw); err != nil {
		t.Fatalf("read back data: %v", err)
	}

	if !ConnectionModelLockedFrom(raw, "deepseek-chat") {
		t.Errorf("a lock written by LockConnectionModel is invisible to the in-memory read: %s", raw)
	}
	if ConnectionModelLockedFrom(raw, "another-model") {
		t.Error("the lock leaked to an unrelated model")
	}
}
