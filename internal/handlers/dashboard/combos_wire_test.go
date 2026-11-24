package dashboard

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

// comboListRow is one row of GET /api/combos as the dashboard reads it.
type comboListRow struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     *string  `json:"kind"`
	Models   []string `json:"models"`
	Strategy string   `json:"strategy"`
}

// TestGetCombosEmitsModelsAsArray pins the wire contract upstream 9router
// serves (combosRepo rowToCombo: models: parseJson(row.models, [])): models is
// a JSON array on the wire while the column keeps the JSON string.
func TestGetCombosEmitsModelsAsArray(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	tests := []struct {
		name   string
		stored string
		want   string
	}{
		{"string members", `["openai/gpt-4o-mini","anthropic/claude-3-haiku"]`, `["openai/gpt-4o-mini","anthropic/claude-3-haiku"]`},
		{"empty list", `[]`, `[]`},
		{"legacy object members pass through", `[{"model":"a/b","weight":2}]`, `[{"model":"a/b","weight":2}]`},
		{"blank column", ``, `[]`},
		{"invalid json", `{not json`, `[]`},
		{"non-array json", `"openai/gpt-4o"`, `[]`},
	}
	for i, tt := range tests {
		id := "wire-" + string(rune('a'+i))
		if err := repo.CreateCombo(id, id, "chat", tt.stored, "fallback", ""); err != nil {
			t.Fatalf("%s: CreateCombo: %v", tt.name, err)
		}
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/combos", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/combos = %d: %s", rec.Code, rec.Body.String())
	}
	var rows []struct {
		ID       string         `json:"id"`
		Models   jsontext.Value `json:"models"`
		Strategy string         `json:"strategy"`
		Kind     string         `json:"kind"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("response is not a top-level array of combos: %v: %s", err, rec.Body.String())
	}
	if len(rows) != len(tests) {
		t.Fatalf("got %d combos, want %d", len(rows), len(tests))
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := rows[i]
			if got := compactJSON(t, row.Models); got != tt.want {
				t.Errorf("models on the wire = %s, want %s", got, tt.want)
			}
			if row.Strategy != "fallback" || row.Kind != "chat" {
				t.Errorf("metadata lost: strategy=%q kind=%q", row.Strategy, row.Kind)
			}
		})
	}

	t.Run("storage keeps the JSON string", func(t *testing.T) {
		got, err := repo.GetComboById("wire-a")
		if err != nil || got == nil {
			t.Fatalf("GetComboById: %v", err)
		}
		if want := tests[0].stored; got.Models != want {
			t.Errorf("stored models = %q, want %q", got.Models, want)
		}
	})
}

func compactJSON(t *testing.T, raw jsontext.Value) string {
	t.Helper()
	compacted := raw.Clone()
	if err := compacted.Compact(); err != nil {
		t.Fatalf("compact %s: %v", raw, err)
	}
	return string(compacted)
}
