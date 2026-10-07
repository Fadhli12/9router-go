package db

import (
	"database/sql"
	"fmt"

	"9router/proxy/internal/apikeycache"
	"9router/proxy/internal/models"
)

// ValidateApiKey checks if the given API key exists and is active.
func (r *Repo) ValidateApiKey(key string) (bool, error) {
	var active int
	err := r.db.QueryRow("SELECT isActive FROM apiKeys WHERE key = ? LIMIT 1", key).Scan(&active)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return active == 1, nil
}

// GetApiKeyByKey retrieves detailed APIKey information by key.
func (r *Repo) GetApiKeyByKey(key string) (*models.APIKey, error) {
	var apiKey models.APIKey
	err := r.db.QueryRow(
		"SELECT id, key, name, machineId, isActive, createdAt, maxConcurrent, allowedProviders FROM apiKeys WHERE key = ? LIMIT 1",
		key,
	).Scan(&apiKey.ID, &apiKey.Key, &apiKey.Name, &apiKey.MachineID, &apiKey.IsActive, &apiKey.CreatedAt, &apiKey.MaxConcurrent, &apiKey.AllowedProviders)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &apiKey, nil
}

// UpdateApiKeyAllowedProviders updates the allowedProviders field for an API key.
func (r *Repo) UpdateApiKeyAllowedProviders(id string, allowedProviders string) error {
	_, err := r.db.Exec(`UPDATE apiKeys SET allowedProviders = ? WHERE id = ?`, allowedProviders, id)
	if err != nil {
		return fmt.Errorf("update api key allowed providers %s: %w", id, err)
	}
	apikeycache.Invalidate()
	return nil
}
