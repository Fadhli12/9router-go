package dashboard

import (
	"encoding/json/jsontext"
	"strings"

	"9router/proxy/internal/log"
	"9router/proxy/internal/models"
)

// comboWire is the GET /api/combos shape of one combo. The column stores
// models as a JSON string, but the wire carries the array itself, which is what
// upstream's rowToCombo emits (`models: parseJson(row.models, [])`).
type comboWire struct {
	*models.Combo
	Models jsontext.Value `json:"models"`
}

// toComboWire wraps combos for the wire. The storage type is left alone: only
// the response changes shape.
func toComboWire(combos []*models.Combo) []comboWire {
	out := make([]comboWire, len(combos))
	for i, combo := range combos {
		out[i] = comboWire{Combo: combo, Models: comboModelsWire(combo)}
	}
	return out
}

// comboModelsWire returns the stored models column as a JSON array. A column
// that is empty, invalid JSON, or not an array yields [] and a warning, so one
// corrupt row cannot take the whole Combos view down (upstream's parseJson
// falls back to [] the same way). Array elements pass through untouched, so
// legacy object entries ({model, weight}) keep their shape.
func comboModelsWire(combo *models.Combo) jsontext.Value {
	raw := strings.TrimSpace(combo.Models)
	if raw == "" {
		return jsontext.Value("[]")
	}
	value := jsontext.Value(raw)
	if !value.IsValid() || value.Kind() != '[' {
		log.Warn("combos", "stored models are not a JSON array", "combo", combo.Name)
		return jsontext.Value("[]")
	}
	return value
}
