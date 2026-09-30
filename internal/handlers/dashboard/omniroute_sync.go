package dashboard

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"

	json "encoding/json/v2"

	"9router/proxy/internal/handlerutil"
	_ "modernc.org/sqlite"
)

// HandleSyncFromOmniRoute imports custom models, aliases, compat overrides,
// and provider strategies from an existing OmniRoute SQLite database.
func (h *DashboardHandler) HandleSyncFromOmniRoute(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "could not determine home dir: "+err.Error())
		return
	}

	omniDBPath := filepath.Join(home, ".omniroute", "storage.sqlite")
	if _, err := os.Stat(omniDBPath); os.IsNotExist(err) {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "OmniRoute database not found at "+omniDBPath)
		return
	}

	db, err := sql.Open("sqlite", "file:"+omniDBPath+"?mode=ro")
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to open OmniRoute db: "+err.Error())
		return
	}
	defer db.Close()

	stats := map[string]int{
		"customModels":         0,
		"modelAliases":         0,
		"modelCompatOverrides": 0,
	}

	// 1. customModels
	rows, err := db.Query("SELECT key, value FROM key_value WHERE namespace = 'customModels'")
	if err == nil {
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err == nil {
				if err := h.Repo.SetKV("customModels", k, v); err == nil {
					stats["customModels"]++
				}
			}
		}
		rows.Close()
	}

	// 2. modelAliases
	rows2, err := db.Query("SELECT key, value FROM key_value WHERE namespace = 'modelAliases'")
	if err == nil {
		for rows2.Next() {
			var k, v string
			if err := rows2.Scan(&k, &v); err == nil {
				if err := h.Repo.SetKV("modelAliases", k, v); err == nil {
					stats["modelAliases"]++
				}
			}
		}
		rows2.Close()
	}

	// 3. modelCompatOverrides
	rows3, err := db.Query("SELECT key, value FROM key_value WHERE namespace = 'modelCompatOverrides'")
	if err == nil {
		for rows3.Next() {
			var k, v string
			if err := rows3.Scan(&k, &v); err == nil {
				if err := h.Repo.SetKV("modelCompatOverrides", k, v); err == nil {
					stats["modelCompatOverrides"]++
				}
			}
		}
		rows3.Close()
	}

	// 4. providerStrategies
	var stratVal string
	err = db.QueryRow("SELECT value FROM key_value WHERE namespace = 'settings' AND key = 'providerStrategies'").Scan(&stratVal)
	if err == nil && stratVal != "" {
		var omniStrats map[string]any
		if json.Unmarshal([]byte(stratVal), &omniStrats) == nil {
			settings, _ := h.Repo.GetSettingsRaw()
			if settings == nil {
				settings = make(map[string]any)
			}
			curStrats, _ := settings["providerStrategies"].(map[string]any)
			if curStrats == nil {
				curStrats = make(map[string]any)
			}
			for p, strat := range omniStrats {
				if _, exists := curStrats[p]; !exists {
					curStrats[p] = strat
				}
			}
			settings["providerStrategies"] = curStrats
			_ = h.Repo.UpdateSettingsRaw(settings)
		}
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success":              true,
		"message":              "Synced successfully from OmniRoute",
		"customModels":         stats["customModels"],
		"modelAliases":         stats["modelAliases"],
		"modelCompatOverrides": stats["modelCompatOverrides"],
	})
}
