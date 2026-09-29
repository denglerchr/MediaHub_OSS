// The repository package implements the interface to communicate
// with the database (SQLite, PostgreSQL, in the future maybe MSSQL)
// Some standards:
// - timestamps are passed as time.Time and handled as Int64 representing millisecond precision unix epochs internally
// - the zero value (time.Time{}) is used to specify an undefined/missing timestamp
// - omitting a timestamp (by passing time.Time{}) can be used to have the server create a default timestamp entry// - timestamps should use the server time, thus the client should avoid passing timestamps created using time.now()
// - the interface uses time.Duration instead of timestamps where possible to avoid passing client timestamp
package repository

import (
	"context"
	"errors"
	"fmt"
	"mediahub_oss/internal/shared/customerrors"
	"time"
)

type Repository interface {
	// General
	Close() error
	GetDBTime(ctx context.Context) (time.Time, error)

	// Database
	CreateDatabase(ctx context.Context, db Database) (Database, error)
	GetDatabase(ctx context.Context, dbID ULID) (Database, error)
	GetDatabases(ctx context.Context) ([]Database, error)
	UpdateDatabase(ctx context.Context, db Database) (Database, error)
	DeleteDatabase(ctx context.Context, dbID ULID) error
	GetDatabaseStats(ctx context.Context, dbID ULID) (DatabaseStats, error)

	// Custom Fields
	AddCustomField(ctx context.Context, dbID ULID, field CustomFieldDef) (CustomFieldDef, error)
	UpdateCustomField(ctx context.Context, dbID ULID, fieldID int, name *string, isIndexed *bool) (CustomFieldDef, error)
	DeleteCustomField(ctx context.Context, dbID ULID, fieldID int) error
	GetCustomFields(ctx context.Context, dbID ULID) ([]CustomFieldDef, error)

	// Housekeeping
	HouseKeepingRequired(ctx context.Context) ([]Database, error)            // return all databases where the last housekeeping run was longer ago than the provided interval
	HouseKeepingWasCalled(ctx context.Context, dbID ULID) (time.Time, error) // set the LastHkRun to now (server timestamp), used by housekeeping to track when the last run was

	// Entry
	// Deleting or creating entries will also update the database statistics
	CreateEntry(ctx context.Context, db Database, entry Entry) (Entry, error)
	GetEntry(ctx context.Context, dbID ULID, id int64) (Entry, error)
	GetEntries(ctx context.Context, dbID ULID, opts QueryOptions) ([]Entry, error)
	UpdateEntry(ctx context.Context, dbID ULID, entry Entry) (Entry, error)
	UpdateEntriesStatus(ctx context.Context, dbID ULID, entryIDs []int64, status EntryStatus) error
	ClaimQueuedEntry(ctx context.Context, dbID ULID, entryID int64) (bool, error)
	GetEntriesByStatus(ctx context.Context, dbID ULID, status EntryStatus, limit uint64) ([]Entry, error)
	CountEntriesByStatus(ctx context.Context, dbID ULID, status EntryStatus) (int64, error)
	DeleteEntry(ctx context.Context, dbID ULID, id int64) (DeletedEntryMeta, error)
	DeleteEntries(ctx context.Context, dbID ULID, entryIDs []int64) ([]DeletedEntryMeta, error)
	SearchEntries(ctx context.Context, dbID ULID, req SearchRequest, customFields []CustomFieldDef) ([]Entry, error)

	// User
	CreateUser(ctx context.Context, user User) (User, error)
	CountAdminUsers(ctx context.Context) (int64, error)
	DeleteUser(ctx context.Context, id ULID) error
	UpdateUser(ctx context.Context, user User) (User, error)
	GetUsers(ctx context.Context, accountType *AccountType) ([]User, error)
	GetUserByID(ctx context.Context, id ULID) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	SetUserPermissions(ctx context.Context, permissions UserPermissions) error // create or update or delete (in case of empty Roles)
	GetUserPermissions(ctx context.Context, userID ULID, dbID ULID) (UserPermissions, error)
	GetAllUserPermissions(ctx context.Context, userID ULID) ([]UserPermissions, error)

	// OIDC
	GetUserByOIDCIdentity(ctx context.Context, issuer, subject string) (User, error)
	CreateOIDCUser(ctx context.Context, params CreateOIDCUserParams) (User, error)

	// Token
	StoreRefreshToken(ctx context.Context, userID ULID, tokenHash string, validDuration time.Duration) error // TODO adapt implementations
	ValidateRefreshToken(ctx context.Context, tokenHash string) (ULID, error)
	DeleteRefreshToken(ctx context.Context, tokenHash string) error
	DeleteExpiredRefreshTokens(ctx context.Context) (int64, error)
	DeleteAllRefreshTokensForUser(ctx context.Context, userID ULID) error

	// API Key
	CreateAPIKey(ctx context.Context, apiKey APIKey) (APIKey, error)
	GetAPIKeyByID(ctx context.Context, id ULID) (APIKey, error)
	GetAPIKeyByHash(ctx context.Context, keyHash string) (APIKey, error)
	GetAPIKeyWithOwnerByHash(ctx context.Context, keyHash string) (APIKey, User, error)
	GetAPIKeysByUserID(ctx context.Context, userID ULID) ([]APIKey, error)
	GetAllAPIKeys(ctx context.Context) ([]APIKey, error)
	UpdateAPIKey(ctx context.Context, apiKey APIKey) (APIKey, error)
	DeleteAPIKey(ctx context.Context, id ULID) error
	DeleteExpiredAPIKeys(ctx context.Context) (int64, error)
	UpdateAPIKeyLastUsed(ctx context.Context, id ULID, lastUsed time.Duration) error // duration is elapsed time since usage. TIme is calculated on the server side to avoid client time sync issues.

	// Logging
	LogAudit(ctx context.Context, log AuditLog) error
	GetLogs(ctx context.Context, opts QueryOptions) ([]AuditLog, error)
	DeleteLogs(ctx context.Context, maxAge time.Duration) error // delete all logs where the timestamp (checked again server time) is too old // TODO adapt implementations

	// Distributed Locking
	AcquireLock(ctx context.Context, lockName string, ownerID string, ttl time.Duration) (bool, error)
	ReleaseLock(ctx context.Context, lockName string, ownerID string) (bool, error)

	// Migration
	GetMigrationVersion(ctx context.Context) (int, error) // integer is 1000*major version + minor version
	MigrateUp(ctx context.Context) error
	MigrateDown(ctx context.Context) error
}

func UserExists(ctx context.Context, s Repository, username string) (bool, error) {
	_, err := s.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, customerrors.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// formatVersion converts the packed integer (e.g., 2001) into a readable string (e.g., "2.1")
func FormatVersion(v int) string {
	major := v / 1000
	minor := v % 1000
	return fmt.Sprintf("%d.%d", major, minor)
}
