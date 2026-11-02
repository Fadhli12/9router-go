package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"9router/proxy/internal/apikeycache"
	"9router/proxy/internal/models"
)

// errNoSuchApiKey signals an update that matched no row.
var errNoSuchApiKey = errors.New("no such api key")

// ValidateApiKey checks if the given API key exists and is active.
// Legacy plaintext path; hashed rows are resolved through
// FindApiKeyByLookup plus an argon2id verification.
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

const apiKeyColumns = `SELECT id, key, name, machineId, isActive, createdAt,
	rateLimitRPM, rateLimitTPM, rateLimitConcurrency,
	tokenQuota, queueTimeoutMs,
	keyHash, lookupHash, keyDisplay,
	expiresAt, lastUsedAt, usedCount, metadata, planId, maxConcurrent, allowedProviders,
	limitMicros, limitTokens, budgetPeriod, alertPct, hardCutoff`

// apiKeyColumnsLegacy is the pre-F-2 read: everything above except the five
// budget columns.
const apiKeyColumnsLegacy = `SELECT id, key, name, machineId, isActive, createdAt,
	rateLimitRPM, rateLimitTPM, rateLimitConcurrency,
	tokenQuota, queueTimeoutMs,
	keyHash, lookupHash, keyDisplay,
	expiresAt, lastUsedAt, usedCount, metadata, planId, maxConcurrent, allowedProviders`

// apiKeyColumnsBase is the read for a table that predates every optional
// column above it. The base body duplicates the tail of apiKeyColumnsLegacy on
// purpose: one entry has to carry the raw columns, because a database from
// before the rate-limit columns (F-1) has neither those nor the budget set.
const apiKeyColumnsBase = `SELECT id, key, name, machineId, isActive, createdAt,
	keyHash, lookupHash, keyDisplay,
	expiresAt, lastUsedAt, usedCount, metadata, planId`

// apiKeyColumnSets lists the key reads from newest to oldest. Each set names
// the newest column it introduces; the probe picks the first set whose marker
// exists, and every set below it selects the same columns minus its own.
//
// The chain matters because the columns arrived in two independent waves and
// both are probed additively: a database restored from before F-1 has no
// rateLimitRPM *and* no budget columns, so answering the budget question alone
// would hand a 30-column query to a 14-column table.
var apiKeyColumnSets = []struct{ marker, columns string }{
	{"limitMicros", apiKeyColumns},
	{"maxConcurrent", apiKeyColumnsLegacy},
	{"", apiKeyColumnsBase},
}

// apiKeyColumnsFor returns the key read for the schema in hand: the full read
// when every governance column exists, the oldest matching read when some do
// not. A legacy database reads every key with its missing axes at zero rather
// than failing the whole listing — the same degradation quotaResetId already
// gets.
func (r *Repo) apiKeyColumnsFor() string {
	return r.apiKeyColumnSet().columns
}

// apiKeyColumnSet resolves and caches the column set once per process. The
// answer is a property of the schema, which does not change while the process
// runs, and the probe sits on the dashboard's boot path.
func (r *Repo) apiKeyColumnSet() *struct{ marker, columns string } {
	if r == nil || r.db == nil {
		return &apiKeyColumnSets[len(apiKeyColumnSets)-1]
	}
	r.budgetColsOnce.Do(func() {
		r.apiKeyCols = detectApiKeyColumnSet(r.db)
	})
	if r.apiKeyCols < 0 || r.apiKeyCols >= len(apiKeyColumnSets) {
		return &apiKeyColumnSets[len(apiKeyColumnSets)-1]
	}
	return &apiKeyColumnSets[r.apiKeyCols]
}

// detectApiKeyColumnSet returns the index in apiKeyColumnSets that matches the
// apiKeys table, or 0 when the probe fails: assuming the full read is the
// option that cannot silently drop a column a caller depends on.
func detectApiKeyColumnSet(sqlDB *sql.DB) int {
	present, err := apiKeysColumns(sqlDB)
	if err != nil {
		return 0
	}
	for i, set := range apiKeyColumnSets {
		if set.marker == "" || present[set.marker] {
			return i
		}
	}
	return len(apiKeyColumnSets) - 1
}

// apiKeysColumns reports the column names on the apiKeys table.
func apiKeysColumns(sqlDB *sql.DB) (map[string]bool, error) {
	rows, err := sqlDB.QueryContext(context.Background(), `PRAGMA table_info(apiKeys)`)
	if err != nil {
		return nil, fmt.Errorf("probe apiKeys columns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	present := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan apiKeys columns: %w", err)
		}
		present[name] = true
	}
	// A mid-iteration failure would otherwise report a half-read column set,
	// which degrades every key read to the legacy query.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate apiKeys columns: %w", err)
	}
	return present, nil
}

// apiKeyScan returns the Scan destinations for the key read plus an applier
// that must run after Scan has filled the nullable columns.
//
// columns is the set apiKeyColumnsFor resolved, so the destination count always
// matches the column count of the query that produced the row. Passing the
// full 30 destinations against a 14-column row is a scan error, not a zero —
// which is how the legacy-schema tests caught this.
func apiKeyScan(k *models.APIKey, columns string) (dest []any, apply func()) {
	withRateLimit := columns != apiKeyColumnsBase
	withBudget := columns == apiKeyColumns
	var nameVal, machineVal sql.NullString
	var rateRPM, rateTPM, rateConc sql.NullInt64
	var tokenQuota, queueTimeout sql.NullInt64
	var keyHash, lookupHash, keyDisplay sql.NullString
	var expiresAt, lastUsedAt, metadata sql.NullString
	var usedCount sql.NullInt64
	var maxConcurrent sql.NullInt64
	var allowedProviders sql.NullString
	var planID sql.NullString
	// F-2 per-period budget. limitMicros and limitTokens are plain int64 on
	// the model rather than pointers: 0 is the meaningful "not set" value, so
	// there is no NULL to preserve.
	var limitMicros, limitTokens int64
	var budgetPeriod string
	var alertPct, hardCutoff int
	dest = []any{&k.ID, &k.Key, &nameVal, &machineVal, &k.IsActive, &k.CreatedAt}
	if withRateLimit {
		dest = append(dest, &rateRPM, &rateTPM, &rateConc, &tokenQuota, &queueTimeout)
	}
	dest = append(dest, &keyHash, &lookupHash, &keyDisplay,
		&expiresAt, &lastUsedAt, &usedCount, &metadata, &planID)
	if withRateLimit {
		dest = append(dest, &maxConcurrent, &allowedProviders)
	}
	if withBudget {
		dest = append(dest, &limitMicros, &limitTokens, &budgetPeriod, &alertPct, &hardCutoff)
	}
	apply = func() {
		applyApiKeyNulls(k, nameVal, machineVal, rateRPM, rateTPM, rateConc, tokenQuota, queueTimeout, usedCount,
			keyHash, lookupHash, keyDisplay, expiresAt, lastUsedAt, metadata, planID)
		if maxConcurrent.Valid {
			v := int(maxConcurrent.Int64)
			k.MaxConcurrent = &v
		}
		if allowedProviders.Valid {
			k.AllowedProviders = &allowedProviders.String
		}
		if withBudget {
			k.LimitMicros = limitMicros
			k.LimitTokens = limitTokens
			k.BudgetPeriod = budgetPeriod
			k.AlertPct = alertPct
			k.HardCutoff = hardCutoff != 0
		}
	}
	return dest, apply
}

// applyApiKeyNulls copies the scanned nullable columns onto the model.
func applyApiKeyNulls(k *models.APIKey, nameVal, machineVal sql.NullString,
	rateRPM, rateTPM, rateConc, tokenQuota, queueTimeout, usedCount sql.NullInt64,
	keyHash, lookupHash, keyDisplay sql.NullString,
	expiresAt, lastUsedAt, metadata, planID sql.NullString) {
	if nameVal.Valid {
		k.Name = &nameVal.String
	}
	if machineVal.Valid {
		k.MachineID = &machineVal.String
	}
	if rateRPM.Valid {
		v := int(rateRPM.Int64)
		k.RateLimitRPM = &v
	}
	if rateTPM.Valid {
		v := int(rateTPM.Int64)
		k.RateLimitTPM = &v
	}
	if rateConc.Valid {
		v := int(rateConc.Int64)
		k.RateLimitConcurrency = &v
	}
	if tokenQuota.Valid {
		v := int(tokenQuota.Int64)
		k.TokenQuota = &v
	}
	if queueTimeout.Valid {
		v := int(queueTimeout.Int64)
		k.QueueTimeoutMs = &v
	}
	if keyHash.Valid {
		k.KeyHash = &keyHash.String
	}
	if lookupHash.Valid {
		k.LookupHash = &lookupHash.String
	}
	if keyDisplay.Valid {
		k.KeyDisplay = &keyDisplay.String
	}
	if expiresAt.Valid {
		k.ExpiresAt = &expiresAt.String
	}
	if lastUsedAt.Valid {
		k.LastUsedAt = &lastUsedAt.String
	}
	if usedCount.Valid {
		v := int(usedCount.Int64)
		k.UsedCount = &v
	}
	if metadata.Valid {
		k.Metadata = &metadata.String
	}
	if planID.Valid {
		k.PlanID = &planID.String
	}
}

// GetApiKeyByKey retrieves detailed APIKey information by plaintext key.
// Kept for legacy rows whose `key` column still holds the secret; hashed
// rows have a blank `key` and are found via FindApiKeyByLookup.
func (r *Repo) GetApiKeyByKey(key string) (*models.APIKey, error) {
	return r.queryApiKey("FROM apiKeys WHERE key = ? LIMIT 1", key)
}

// FindApiKeyByLookup retrieves an APIKey by its SHA-256 lookup hash (F-6).
// This is the primary lookup path for hashed rows; it never compares
// plaintext. It returns (nil, nil) when no row matches.
func (r *Repo) FindApiKeyByLookup(lookupHash string) (*models.APIKey, error) {
	if lookupHash == "" {
		return nil, nil
	}
	return r.queryApiKey("FROM apiKeys WHERE lookupHash = ? LIMIT 1", lookupHash)
}

// queryApiKey runs a single-row apiKeys read and maps the scanned columns.
func (r *Repo) queryApiKey(whereClause string, arg string) (*models.APIKey, error) {
	var apiKey models.APIKey
	columns := r.apiKeyColumnsFor()
	dest, apply := apiKeyScan(&apiKey, columns)
	err := r.db.QueryRowContext(context.Background(), r.apiKeyColumnsFor()+" "+whereClause, arg).Scan(dest...)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	apply()
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

// hashedKeyPrefix marks a `key` column as a hashed row standing in for the
// original secret. `apiKeys.key` is TEXT UNIQUE NOT NULL, so every row cannot
// simply be blanked to the same value — the second hashed key would collide
// with the first. The sentinel is derived from that row's lookup hash, which
// makes it unique per row, and carries a prefix no client ever sends, so a
// legacy `WHERE key = ?` lookup can never authenticate against it.
const hashedKeyPrefix = "\x00argon2id:"

func hashedKeySentinel(lookupHash string) string {
	return hashedKeyPrefix + lookupHash
}

// IsHashedKeyRow reports whether a row is a pre-#199 hashed row: its `key`
// column holds the sentinel rather than a secret, so the plaintext is
// unrecoverable. Such a row still authenticates through its argon2id verifier
// (see middleware.resolveApiKey) but cannot be revealed in the dashboard.
func IsHashedKeyRow(k *models.APIKey) bool {
	return k != nil && strings.HasPrefix(k.Key, hashedKeyPrefix)
}

// HashApiKeyRow fills the F-6 hash columns for a row and replaces the
// plaintext `key` column with the hashed-row sentinel. A sentinel `key` is how
// a hashed row is recognised; legacy rows keep their plaintext and
// authenticate through GetApiKeyByKey until this runs. After it runs the secret
// exists only as an argon2id verifier and a masked display value.
func (r *Repo) HashApiKeyRow(id, keyHash, lookupHash, keyDisplay string) error {
	res, err := r.db.Exec(
		`UPDATE apiKeys SET key = ?, keyHash = ?, lookupHash = ?, keyDisplay = ? WHERE id = ?`,
		hashedKeySentinel(lookupHash), keyHash, lookupHash, keyDisplay, id,
	)
	if err != nil {
		return fmt.Errorf("hash api key row %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("hash api key row %s: %w", id, errNoSuchApiKey)
	}
	return nil
}

// GetApiKeyByID retrieves one key by its row id rather than by its secret.
// The dashboard policy editor uses it so a partial update can read the current
// values without ever touching the credential columns.
func (r *Repo) GetApiKeyByID(id string) (*models.APIKey, error) {
	return r.queryApiKey("FROM apiKeys WHERE id = ? LIMIT 1", id)
}

// RotateApiKeySecret replaces a row's secret with a new plaintext value and
// refreshes its masked display.
//
// The hash columns are cleared rather than left stale: a row that still carried
// a verifier from before issue #199 would keep being resolved through the
// argon2id path, so its new secret would never be found and the key would
// appear dead to every client holding it.
func (r *Repo) RotateApiKeySecret(id, plaintext, keyDisplay string) error {
	res, err := r.db.Exec(
		`UPDATE apiKeys SET key = ?, keyDisplay = ?, keyHash = '', lookupHash = '' WHERE id = ?`,
		plaintext, keyDisplay, id,
	)
	if err != nil {
		return fmt.Errorf("rotate api key secret %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("rotate api key secret %s: %w", id, errNoSuchApiKey)
	}
	return nil
}

// SetApiKeyDisplay records the masked form of a row's secret without touching
// the secret itself.
func (r *Repo) SetApiKeyDisplay(id, keyDisplay string) error {
	res, err := r.db.Exec(`UPDATE apiKeys SET keyDisplay = ? WHERE id = ?`, keyDisplay, id)
	if err != nil {
		return fmt.Errorf("set api key display %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("set api key display %s: %w", id, errNoSuchApiKey)
	}
	return nil
}
