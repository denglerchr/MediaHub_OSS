package sqlutil

import (
	"database/sql"
	"fmt"
	"time"

	repo "mediahub_oss/internal/repository"
)

// RowScanner abstracts *sql.Row and *sql.Rows for shared row scanning.
type RowScanner interface {
	Scan(dest ...any) error
}

var apiKeyColumns = []string{
	"id", "user_id", "name", "key_hash", "key_hint",
	"scope_view", "scope_create", "scope_edit", "scope_delete", "scope_admin",
	"created_at", "expires_at", "last_used_at",
}

// APIKeySelectColumns returns the standard api_keys SELECT column list,
// optionally qualified with tableAlias (e.g. "ak" -> "ak.id").
func APIKeySelectColumns(tableAlias string) []string {
	cols := make([]string, len(apiKeyColumns))
	for i, col := range apiKeyColumns {
		if tableAlias != "" {
			cols[i] = tableAlias + "." + col
		} else {
			cols[i] = col
		}
	}
	return cols
}

type apiKeyScanTarget struct {
	key            repo.APIKey
	createdAtVal   int64
	expiresAtNull  sql.NullInt64
	lastUsedAtNull sql.NullInt64
	scopeView      bool
	scopeCreate    bool
	scopeEdit      bool
	scopeDelete    bool
	scopeAdmin     bool
}

func (t *apiKeyScanTarget) dests() []any {
	return []any{
		&t.key.ID, &t.key.UserID, &t.key.Name, &t.key.KeyHash, &t.key.KeyHint,
		&t.scopeView, &t.scopeCreate, &t.scopeEdit, &t.scopeDelete, &t.scopeAdmin,
		&t.createdAtVal, &t.expiresAtNull, &t.lastUsedAtNull,
	}
}

func (t *apiKeyScanTarget) toModel() repo.APIKey {
	t.key.Scope = repo.NewAccessGrant(t.scopeView, t.scopeCreate, t.scopeEdit, t.scopeDelete, t.scopeAdmin)
	t.key.CreatedAt = time.UnixMilli(t.createdAtVal)
	if t.expiresAtNull.Valid {
		t.key.ExpiresAt = time.UnixMilli(t.expiresAtNull.Int64)
	}
	if t.lastUsedAtNull.Valid {
		t.key.LastUsedAt = time.UnixMilli(t.lastUsedAtNull.Int64)
	}
	return t.key
}

// ScanAPIKey scans a single row matching APIKeySelectColumns into a repo.APIKey.
func ScanAPIKey(scanner RowScanner) (repo.APIKey, error) {
	var target apiKeyScanTarget
	if err := scanner.Scan(target.dests()...); err != nil {
		return repo.APIKey{}, err
	}
	return target.toModel(), nil
}

// ScanAPIKeyWithOwner scans a joined api_keys + users row into (APIKey, User).
func ScanAPIKeyWithOwner(scanner RowScanner) (repo.APIKey, repo.User, error) {
	var target apiKeyScanTarget
	var user repo.User
	var accountTypeUint uint8

	dests := append(target.dests(), &user.ID, &user.Username, &user.PasswordHash, &user.IsAdmin, &accountTypeUint)
	if err := scanner.Scan(dests...); err != nil {
		return repo.APIKey{}, repo.User{}, err
	}

	user.AccountType = repo.AccountType(accountTypeUint)
	return target.toModel(), user, nil
}

// ScanAPIKeyRows scans all rows matching APIKeySelectColumns into a slice of API keys.
func ScanAPIKeyRows(rows *sql.Rows) ([]repo.APIKey, error) {
	var keys []repo.APIKey
	for rows.Next() {
		key, err := ScanAPIKey(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan api_key row: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("api_key row iteration error: %w", err)
	}
	return keys, nil
}
