//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// The dashboard keys a provider node's custom-model rows with the node id —
// ProviderDetailView stores them under storageAlias, which is selectedNode.id
// — while every reader addresses the model by the node's display prefix
// (/v1/models publishes "prefix/id"; the resolver maps prefix → node). The
// candidate builder used to skip any row whose alias was neither a prefix nor
// a registry id, so a rule aimed at those models reported "Adds 0 of 0
// matches" while the unit tests stayed green: they seeded the custom row under
// the prefix, a shape the dashboard never writes.
//
// This drives the exact production shape through the real router.
func TestSmartRulesMatchCustomModelsKeyedByNodeID(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())

	if _, err := env.Repo.CreateProviderNode("node-cutad", "openai-compatible", "CutadAI",
		`{"prefix":"cutad","apiType":"openai-compatible"}`); err != nil {
		t.Fatalf("create node: %v", err)
	}
	priority := 1
	data := `{"apiKey":"sk-cutad","baseUrl":"` + upstream.URL + `/chat/completions","providerSpecificData":{"prefix":"cutad"}}`
	if err := env.Repo.CreateProviderConnectionFull("conn-cutad", "node-cutad", "apikey", "Cutad", &priority, data); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	// Production shape: key AND value carry the node id, not the prefix.
	if err := env.Repo.SetKV("customModels", "node-cutad|deepseek-v4-flash|llm",
		`{"providerAlias":"node-cutad","id":"deepseek-v4-flash","type":"llm"}`); err != nil {
		t.Fatalf("seed custom model: %v", err)
	}
	env.AddCombo(t, "combo-ds", "ds-all", []string{})

	cases := []struct {
		pattern string
		mode    string
		want    string
	}{
		{"deepseek*", "glob", "cutad/deepseek-v4-flash"},
		{"deepseek", "keyword", "cutad/deepseek-v4-flash"},
		{"cutad/deepseek-v4-flash", "glob", "cutad/deepseek-v4-flash"},
		{"re:^cutad/deepseek-v4-flash$", "regex", "cutad/deepseek-v4-flash"},
	}
	for _, tc := range cases {
		res := env.Post(t, "/api/combos/combo-ds/smart/preview", map[string]any{
			"matchRules": []any{map[string]any{"pattern": tc.pattern, "mode": tc.mode}},
		})
		if res.Status != http.StatusOK {
			t.Fatalf("%s %s: POST preview = %d, want 200 (body: %s)",
				tc.mode, tc.pattern, res.Status, truncate(res.Body))
		}
		var out struct {
			Candidates int `json:"candidates"`
			Added      []string
			Rules      []struct {
				Matched int `json:"matched"`
				Applied int `json:"applied"`
			} `json:"rules"`
		}
		if err := json.Unmarshal(res.Body, &out); err != nil {
			t.Fatalf("%s %s: decode: %v", tc.mode, tc.pattern, err)
		}
		if out.Candidates != 1 || len(out.Added) != 1 || out.Added[0] != tc.want ||
			len(out.Rules) != 1 || out.Rules[0].Matched != 1 || out.Rules[0].Applied != 1 {
			t.Fatalf("%s %q: got candidates=%d added=%v rules=%+v, want the one model %q",
				tc.mode, tc.pattern, out.Candidates, out.Added, out.Rules, tc.want)
		}
	}
	fmt.Printf("node-id-keyed candidates matched under all four modes\n")

	// The stored membership keeps the prefix spelling: it is the one the
	// resolver and /v1/models agree on. A dry run must not have written it.
	stored, err := env.Repo.GetComboRules(t.Context(), "combo-ds")
	if err != nil {
		t.Fatalf("GetComboRules: %v", err)
	}
	if stored != "" && stored != "[]" {
		t.Errorf("the preview wrote rules: %q", stored)
	}

	// Saving the rules materializes the member under the prefix spelling.
	res := env.Post(t, "/api/combos", map[string]any{
		"id":         "combo-save",
		"name":       "ds-save",
		"models":     []string{},
		"matchRules": []any{map[string]any{"pattern": "deepseek*", "mode": "glob"}},
	})
	if res.Status != http.StatusOK {
		t.Fatalf("create combo with rules = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	combo, err := env.Repo.GetComboById("combo-save")
	if err != nil {
		t.Fatalf("GetComboById: %v", err)
	}
	if combo == nil {
		t.Fatalf("combo-save was not stored")
	}
	var members []string
	if err := json.Unmarshal([]byte(combo.Models), &members); err != nil {
		t.Fatalf("decode combo models %q: %v", combo.Models, err)
	}
	if len(members) != 1 || members[0] != "cutad/deepseek-v4-flash" {
		t.Fatalf("saved members = %v, want [cutad/deepseek-v4-flash]", members)
	}
}

// A member written under the node-id spelling survives a reconcile: the
// candidate's AltID keeps the second spelling live so a rule snapshot stored
// before the fix is not pruned.
func TestSmartRulesKeepNodeIDSpelledMember(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())

	if _, err := env.Repo.CreateProviderNode("node-cutad", "openai-compatible", "CutadAI",
		`{"prefix":"cutad","apiType":"openai-compatible"}`); err != nil {
		t.Fatalf("create node: %v", err)
	}
	priority := 1
	data := `{"apiKey":"sk-cutad","baseUrl":"` + upstream.URL + `/chat/completions","providerSpecificData":{"prefix":"cutad"}}`
	if err := env.Repo.CreateProviderConnectionFull("conn-cutad", "node-cutad", "apikey", "Cutad", &priority, data); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := env.Repo.SetKV("customModels", "node-cutad|deepseek-v4-flash|llm",
		`{"providerAlias":"node-cutad","id":"deepseek-v4-flash","type":"llm"}`); err != nil {
		t.Fatalf("seed custom model: %v", err)
	}
	env.AddCombo(t, "combo-ds", "ds-all", []string{"node-cutad/deepseek-v4-flash"})
	if err := env.Repo.SetComboRules(t.Context(), "combo-ds",
		`[{"id":"r1","pattern":"deepseek*","mode":"glob","effect":"include","enabled":true,"maxMembers":20,"allKinds":false,"resolved":["node-cutad/deepseek-v4-flash"]}]`); err != nil {
		t.Fatalf("store rules: %v", err)
	}

	res := env.Post(t, "/api/combos/reconcile", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("POST reconcile = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var applied struct {
		Reconciled int      `json:"reconciled"`
		Removed    []string `json:"removed"`
	}
	res.Decode(t, &applied)
	if applied.Reconciled != 0 {
		t.Errorf("reconciled = %d, want 0 (the node-id member must survive)", applied.Reconciled)
	}
	if len(applied.Removed) != 0 {
		t.Errorf("removed = %v, want nothing pruned", applied.Removed)
	}
}

// A rule pattern written with the candidate's second spelling — the canonical
// provider id beside the alias, or a node id beside its display prefix —
// selects the same model. The matcher used to consult only ID and Bare, so
// "deepseek/*" reported "Adds 0 of 0 matches" against a candidate spelled
// "ds/deepseek-chat" (AltID "deepseek/deepseek-chat"), and "node-cutad/deepseek*"
// reported the same against "cutad/deepseek-v4-flash" (AltID
// "node-cutad/deepseek-v4-flash"). Both spellings are ones an operator has a
// reason to write: the provider id is what the connection is registered under,
// and the node id is what the dashboard stores the custom rows under.
func TestSmartRulesMatchAlternateSpellings(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	priority := 1
	dsData := `{"apiKey":"sk-ds","baseUrl":"` + upstream.URL + `/chat/completions"}`
	if err := env.Repo.CreateProviderConnectionFull("conn-ds", "deepseek", "apikey", "DeepSeek", &priority, dsData); err != nil {
		t.Fatalf("create deepseek connection: %v", err)
	}
	if _, err := env.Repo.CreateProviderNode("node-cutad", "openai-compatible", "CutadAI",
		`{"prefix":"cutad","apiType":"openai-compatible"}`); err != nil {
		t.Fatalf("create node: %v", err)
	}
	nodeData := `{"apiKey":"sk-cutad","baseUrl":"` + upstream.URL + `/chat/completions","providerSpecificData":{"prefix":"cutad"}}`
	if err := env.Repo.CreateProviderConnectionFull("conn-cutad", "node-cutad", "apikey", "Cutad", &priority, nodeData); err != nil {
		t.Fatalf("create node connection: %v", err)
	}
	if err := env.Repo.SetKV("customModels", "node-cutad|deepseek-v4-flash|llm",
		`{"providerAlias":"node-cutad","id":"deepseek-v4-flash","type":"llm"}`); err != nil {
		t.Fatalf("seed custom model: %v", err)
	}
	env.AddCombo(t, "combo-alt", "alt-spellings", []string{})
	cases := []struct {
		name    string
		pattern string
		mode    string
		want    string
	}{
		{"provider id glob", "deepseek/*", "glob", "ds/deepseek-chat"},
		{"provider id regex", `re:^deepseek/.*$`, "regex", "ds/deepseek-chat"},
		{"provider id keyword", "deepseek/", "keyword", "ds/deepseek-chat"},
		{"node id glob", "node-cutad/deepseek*", "glob", "cutad/deepseek-v4-flash"},
		{"node id regex", `re:^node-cutad/deepseek-v4-flash$`, "regex", "cutad/deepseek-v4-flash"},
		{"node id keyword", "node-cutad/", "keyword", "cutad/deepseek-v4-flash"},
	}
	for _, tc := range cases {
		res := env.Post(t, "/api/combos/combo-alt/smart/preview", map[string]any{
			"matchRules": []any{map[string]any{"pattern": tc.pattern, "mode": tc.mode}},
		})
		if res.Status != http.StatusOK {
			t.Fatalf("%s: POST preview = %d, want 200 (body: %s)", tc.name, res.Status, truncate(res.Body))
		}
		var out struct {
			Added []string
			Rules []struct {
				Matched int `json:"matched"`
				Applied int `json:"applied"`
			} `json:"rules"`
		}
		if err := json.Unmarshal(res.Body, &out); err != nil {
			t.Fatalf("%s: decode: %v", tc.name, err)
		}
		if len(out.Rules) != 1 || out.Rules[0].Matched < 1 || out.Rules[0].Applied < 1 {
			t.Fatalf("%s %q: matched/applied = %+v, want at least one under the alternate spelling",
				tc.name, tc.pattern, out.Rules)
		}
		if !slices.Contains(out.Added, tc.want) {
			t.Fatalf("%s %q: added = %v, want it to contain %q", tc.name, tc.pattern, out.Added, tc.want)
		}
	}
	// An exclude rule spelled the same way keeps the model out: the deny map is
	// keyed by the candidate's own id, so the alternate spelling still denies.
	res := env.Post(t, "/api/combos/combo-alt/smart/preview", map[string]any{
		"matchRules": []any{
			map[string]any{"pattern": "*", "mode": "glob"},
			map[string]any{"pattern": "deepseek/*", "mode": "glob", "effect": "exclude"},
		},
	})
	if res.Status != http.StatusOK {
		t.Fatalf("exclude preview = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var out struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("exclude decode: %v", err)
	}
	for _, m := range out.Models {
		if strings.HasPrefix(m, "ds/") {
			t.Fatalf("models = %v, want the provider-id-spelled exclude to have removed the ds/ members", out.Models)
		}
	}
	fmt.Printf("alternate-spelling rules matched under glob, keyword and regex\n")
}

// Gap 1: a member the operator pulled out under one spelling stays out under
// every other spelling. The dashboard shows the node-id spelling while the
// rule claims the prefix one, so comparing Excluded against the candidate's
// own id alone offered the model straight back.
func TestSmartRulesExcludedHoldsUnderEverySpelling(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	if _, err := env.Repo.CreateProviderNode("node-cutad", "openai-compatible", "CutadAI",
		`{"prefix":"cutad","apiType":"openai-compatible"}`); err != nil {
		t.Fatalf("create node: %v", err)
	}
	priority := 1
	data := `{"apiKey":"sk-cutad","baseUrl":"` + upstream.URL + `/chat/completions","providerSpecificData":{"prefix":"cutad"}}`
	if err := env.Repo.CreateProviderConnectionFull("conn-cutad", "node-cutad", "apikey", "Cutad", &priority, data); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := env.Repo.SetKV("customModels", "node-cutad|deepseek-v4-flash|llm",
		`{"providerAlias":"node-cutad","id":"deepseek-v4-flash","type":"llm"}`); err != nil {
		t.Fatalf("seed custom model: %v", err)
	}
	env.AddCombo(t, "combo-excl", "excl", []string{})
	// The operator removed the member while the dashboard spelled it with the
	// node id; the rule claims the prefix spelling.
	rules := `[{"id":"r1","pattern":"cutad/*","mode":"glob","effect":"include","enabled":true,"maxMembers":20,"resolved":["cutad/deepseek-v4-flash"],"excluded":["node-cutad/deepseek-v4-flash"]}]`
	res := env.Post(t, "/api/combos/combo-excl/smart/preview", map[string]any{"matchRules": []any{
		map[string]any{"pattern": "cutad/*", "mode": "glob", "resolved": []string{"cutad/deepseek-v4-flash"}, "excluded": []string{"node-cutad/deepseek-v4-flash"}},
	}})
	if res.Status != http.StatusOK {
		t.Fatalf("preview = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var out struct {
		Models []string `json:"models"`
		Rules  []struct {
			Matched int `json:"matched"`
			Applied int `json:"applied"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Models) != 0 {
		t.Fatalf("models = %v, want the node-id-spelled removal to hold", out.Models)
	}
	if out.Rules[0].Matched != 0 || out.Rules[0].Applied != 0 {
		t.Fatalf("matched/applied = %d/%d, want 0/0", out.Rules[0].Matched, out.Rules[0].Applied)
	}
	_ = rules
}

// Gap 2: the cap keeps the members already in the combo, so a fallback chain
// the operator ordered by hand is not silently re-cut alphabetically.
func TestSmartRulesCapKeepsExistingMembers(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	if _, err := env.Repo.CreateProviderNode("node-cap", "openai-compatible", "CapAI",
		`{"prefix":"cap","apiType":"openai-compatible"}`); err != nil {
		t.Fatalf("create node: %v", err)
	}
	priority := 1
	data := `{"apiKey":"sk-cap","baseUrl":"` + upstream.URL + `/chat/completions","providerSpecificData":{"prefix":"cap"}}`
	if err := env.Repo.CreateProviderConnectionFull("conn-cap", "node-cap", "apikey", "Cap", &priority, data); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	for _, m := range []string{"aaa", "mmm", "zzz"} {
		if err := env.Repo.SetKV("customModels", "node-cap|"+m+"|llm",
			`{"providerAlias":"node-cap","id":"`+m+`","type":"llm"}`); err != nil {
			t.Fatalf("seed %s: %v", m, err)
		}
	}
	// mmm is already a member and sorts between the other two.
	env.AddCombo(t, "combo-cap", "cap", []string{"cap/mmm"})
	res := env.Post(t, "/api/combos/combo-cap/smart/preview", map[string]any{"matchRules": []any{
		map[string]any{"pattern": "cap/*", "mode": "glob", "maxMembers": 2},
	}})
	if res.Status != http.StatusOK {
		t.Fatalf("preview = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var out struct {
		Models []string `json:"models"`
		Rules  []struct {
			Matched int  `json:"matched"`
			Applied int  `json:"applied"`
			Capped  bool `json:"capped"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Rules[0].Capped {
		t.Fatalf("rules = %+v, want Capped reported", out.Rules)
	}
	if !slices.Equal(out.Models, []string{"cap/mmm", "cap/aaa"}) {
		t.Fatalf("models = %v, want the existing member kept plus the first filler", out.Models)
	}
}

// Gap 3: a `!` in front of a `*`-separated segment negates it, which is how
// "every provider except this one" is written in a single rule.
func TestSmartRulesSegmentNegation(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	priority := 1
	dsData := `{"apiKey":"sk-ds","baseUrl":"` + upstream.URL + `/chat/completions"}`
	if err := env.Repo.CreateProviderConnectionFull("conn-ds", "deepseek", "apikey", "DeepSeek", &priority, dsData); err != nil {
		t.Fatalf("create deepseek connection: %v", err)
	}
	if _, err := env.Repo.CreateProviderNode("node-ken", "openai-compatible", "KenariAI",
		`{"prefix":"kenari","apiType":"openai-compatible"}`); err != nil {
		t.Fatalf("create node: %v", err)
	}
	kenData := `{"apiKey":"sk-ken","baseUrl":"` + upstream.URL + `/chat/completions","providerSpecificData":{"prefix":"kenari"}}`
	if err := env.Repo.CreateProviderConnectionFull("conn-ken", "node-ken", "apikey", "Kenari", &priority, kenData); err != nil {
		t.Fatalf("create kenari connection: %v", err)
	}
	if err := env.Repo.SetKV("customModels", "node-ken|kenari-chat|llm",
		`{"providerAlias":"node-ken","id":"kenari-chat","type":"llm"}`); err != nil {
		t.Fatalf("seed custom model: %v", err)
	}
	env.AddCombo(t, "combo-neg", "neg", []string{})
	// Everything except the deepseek family: the 8 registry models are denied
	// and the kenari custom model survives.
	res := env.Post(t, "/api/combos/combo-neg/smart/preview", map[string]any{"matchRules": []any{
		map[string]any{"pattern": "*!deepseek*", "mode": "glob"},
	}})
	if res.Status != http.StatusOK {
		t.Fatalf("preview = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var out struct {
		Models []string `json:"models"`
		Rules  []struct {
			Matched int `json:"matched"`
			Applied int `json:"applied"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Rules[0].Matched != 1 || out.Rules[0].Applied != 1 {
		t.Fatalf("matched/applied = %d/%d, want 1/1 (only the kenari model)", out.Rules[0].Matched, out.Rules[0].Applied)
	}
	if !slices.Equal(out.Models, []string{"kenari/kenari-chat"}) {
		t.Fatalf("models = %v, want only the non-deepseek model", out.Models)
	}
	// A pattern that is nothing but negations selects nothing.
	res2 := env.Post(t, "/api/combos/combo-neg/smart/preview", map[string]any{"matchRules": []any{
		map[string]any{"pattern": "!deepseek*", "mode": "glob"},
	}})
	if res2.Status != http.StatusBadRequest {
		t.Fatalf("a leading negation must be refused: status = %d, body = %s", res2.Status, truncate(res2.Body))
	}
}
