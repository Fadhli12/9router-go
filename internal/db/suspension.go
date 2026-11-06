package db

import (
	json "encoding/json/v2"
	"fmt"

	"9router/proxy/internal/resilience"
)

// settingsSuspensionsKey is the settings-row key holding the provider halt
// latches, as a provider-keyed object.
//
// The settings row is used rather than a new table because the latch is
// gateway-wide configuration, not per-connection state, and an additive JSON
// key keeps the database readable by upstream 9router (DATABASE.md: bootstrap
// only ever adds).
const settingsSuspensionsKey = "providerSuspensions"

// SuspensionStore persists provider halt latches in the settings row.
//
// It satisfies resilience.SuspensionStore. The latch exists so a suspended
// account cannot cascade through the rest of the pool, and that guarantee has
// to survive a restart: an in-memory latch would be dropped on the next boot
// and the cascade would resume where it left off.
type SuspensionStore struct {
	repo *Repo
}

// NewSuspensionStore returns the store backed by repo.
func NewSuspensionStore(repo *Repo) *SuspensionStore {
	return &SuspensionStore{repo: repo}
}

// LoadSuspensions returns every persisted latch.
//
// A malformed entry is skipped rather than failing the load: one corrupt record
// must not stop the remaining latches from being replayed, because a latch that
// fails to load silently re-opens the provider.
func (s *SuspensionStore) LoadSuspensions() ([]resilience.SuspensionRecord, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	raw, err := s.repo.GetSettingsRaw()
	if err != nil {
		return nil, fmt.Errorf("load suspensions: %w", err)
	}
	stored, ok := raw[settingsSuspensionsKey]
	if !ok || stored == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("load suspensions: remarshal: %w", err)
	}
	var byProvider map[string]resilience.SuspensionRecord
	if err := json.Unmarshal(encoded, &byProvider); err != nil {
		return nil, fmt.Errorf("load suspensions: %w", err)
	}
	out := make([]resilience.SuspensionRecord, 0, len(byProvider))
	for provider, rec := range byProvider {
		if rec.Provider == "" {
			rec.Provider = provider
		}
		if rec.Provider == "" {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// SaveSuspension writes one latch, preserving the others.
//
// The read-modify-write runs inside updateSettings so it holds the settings
// write lock: two providers halted at the same moment must not drop each
// other's latch.
func (s *SuspensionStore) SaveSuspension(rec resilience.SuspensionRecord) error {
	if s == nil || s.repo == nil {
		return nil
	}
	if rec.Provider == "" {
		return nil
	}
	return s.repo.updateSettings(func(existing map[string]any) error {
		byProvider := suspensionsFromSettings(existing)
		byProvider[rec.Provider] = rec
		existing[settingsSuspensionsKey] = byProvider
		return nil
	})
}

// DeleteSuspension removes one latch, which is how an operator releases a
// provider after appealing.
func (s *SuspensionStore) DeleteSuspension(provider string) error {
	if s == nil || s.repo == nil {
		return nil
	}
	if provider == "" {
		return nil
	}
	return s.repo.updateSettings(func(existing map[string]any) error {
		byProvider := suspensionsFromSettings(existing)
		delete(byProvider, provider)
		if len(byProvider) == 0 {
			delete(existing, settingsSuspensionsKey)
			return nil
		}
		existing[settingsSuspensionsKey] = byProvider
		return nil
	})
}

// suspensionsFromSettings decodes the latch map, returning an empty map when
// the key is absent or unreadable. An unreadable map is replaced rather than
// propagated: the alternative is refusing every future write because of one bad
// value, which would leave new latches unpersisted.
func suspensionsFromSettings(existing map[string]any) map[string]resilience.SuspensionRecord {
	byProvider := map[string]resilience.SuspensionRecord{}
	stored, ok := existing[settingsSuspensionsKey]
	if !ok || stored == nil {
		return byProvider
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return byProvider
	}
	if err := json.Unmarshal(encoded, &byProvider); err != nil {
		return map[string]resilience.SuspensionRecord{}
	}
	return byProvider
}
