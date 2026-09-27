package pricing

import (
	"math"
	"testing"
)

// TestIsFreeModel covers the zero-priced namespace upstream added in v0.5.91:
// a model reached through Cline's free tier costs nothing even though the same
// id is priced everywhere else, and the namespace is checked before both the
// exact match and the prefix fallbacks.
func TestIsFreeModel(t *testing.T) {
	tests := []struct {
		model   string
		free    bool
		wantIn  float64
		wantOut float64
	}{
		{model: "cline-free/deepseek-v4.1-flash", free: true, wantIn: 0, wantOut: 0},
		{model: "CLINE-FREE/DeepSeek-V4.1-Flash", free: true, wantIn: 0, wantOut: 0},
		// The bare id is priced: the same model is not free through another gateway.
		{model: "deepseek-v4.1-flash", free: false, wantIn: 1.0, wantOut: 3.0}, // defaultPricing
		{model: "deepseek-v4-flash", free: false, wantIn: 0.07, wantOut: 0.28},
		{model: "gpt-4o", free: false, wantIn: 2.5, wantOut: 10.0},
		// A free id that shares a prefix with a priced one must not leak the price.
		{model: "cline-free/unknown-model", free: true, wantIn: 0, wantOut: 0},
		{model: "", free: false, wantIn: 1.0, wantOut: 3.0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := IsFreeModel(tt.model); got != tt.free {
				t.Errorf("IsFreeModel(%q) = %v, want %v", tt.model, got, tt.free)
			}
			p := GetPricing(tt.model)
			if p.InputPer1M != tt.wantIn || p.OutputPer1M != tt.wantOut {
				t.Errorf("GetPricing(%q) = %+v, want {%v %v}", tt.model, p, tt.wantIn, tt.wantOut)
			}
		})
	}
}

func TestEstimateCostFreeNamespace(t *testing.T) {
	const tokens = 1_000_000
	if cost := EstimateCost("cline-free/deepseek-v4.1-flash", tokens, tokens); cost != 0 {
		t.Errorf("free model cost = %v, want 0", cost)
	}
	if cost := EstimateCost("deepseek-v4-flash", tokens, tokens); math.Abs(cost-0.35) > 1e-9 {
		t.Errorf("priced model cost = %v, want 0.35", cost)
	}
}
