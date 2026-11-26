package utils

import (
	"context"
	"mediahub_oss/internal/repository"
)

// PermissionHolder abstracts authorization checks across the three caller identities:
//   - **GlobalAdmin**: direct (non-API-key) admin session with unrestricted access to all endpoints,
//     databases, and actions. Never use for admin API keys.
//   - **APIKeyOfAdmin**: ANY API key belonging to an admin user (whether AccessAdmin is set or not).
//     IsGlobalAdmin() checks Scope.HasAccess(AccessAdmin), while HasPermission() always enforces (Scope & ag) != 0.
//   - **UserPermissions**: non-admin user (direct session or API key), limited to per-database grants
//     intersected with Scope.
type PermissionHolder interface {
	// IsGlobalAdmin reports whether the caller holds global administrative authority (direct admin
	// session or an admin API key with AccessAdmin). Note: returning true does NOT imply unrestricted
	// database/entry access, because an APIKeyOfAdmin with AccessAdmin returns true here while still
	// enforcing its remaining Scope bits in HasPermission.
	IsGlobalAdmin() bool
	HasPermission(ctx context.Context, database repository.ULID, ag repository.AccessGrant) (bool, error) // check with OR logic, one of the ag bits is enough

	GetAllPermissions(ctx context.Context) (map[repository.ULID]repository.AccessGrant, error)
}

// GlobalAdmin represents a direct (non-API-key) session of an admin user.
// It has unrestricted access to all global admin endpoints and all databases/actions.
// Do NOT use GlobalAdmin for admin API keys — always use APIKeyOfAdmin instead so
// API key scope attenuation is enforced on every database/entry check.
type GlobalAdmin struct{}

func (g *GlobalAdmin) IsGlobalAdmin() bool {
	return true
}

func (g *GlobalAdmin) HasPermission(ctx context.Context, database repository.ULID, ag repository.AccessGrant) (bool, error) {
	return true, nil
}

func (g *GlobalAdmin) GetAllPermissions(ctx context.Context) (map[repository.ULID]repository.AccessGrant, error) {
	return map[repository.ULID]repository.AccessGrant{}, nil // Global admin has implicit access to all databases
}

// APIKeyOfAdmin represents ANY API key belonging to an admin user (with or without AccessAdmin).
// Database and entry operations are always bounded by Scope, while global admin endpoints are
// permitted only when Scope includes AccessAdmin.
type APIKeyOfAdmin struct {
	Scope repository.AccessGrant
	Repo  repository.Repository

	loaded    bool
	databases []repository.ULID
}

func (a *APIKeyOfAdmin) IsGlobalAdmin() bool {
	return a.Scope.HasAccess(repository.AccessAdmin)
}

func (a *APIKeyOfAdmin) HasPermission(ctx context.Context, database repository.ULID, ag repository.AccessGrant) (bool, error) {
	// OR logic: if the API key scope has ANY of the requested bits
	return (a.Scope & ag) != 0, nil
}

func (a *APIKeyOfAdmin) loadDatabases(ctx context.Context) error {
	if a.loaded {
		return nil
	}
	dbs, err := a.Repo.GetDatabases(ctx)
	if err != nil {
		return err
	}
	dbIDs := make([]repository.ULID, len(dbs))
	for i, db := range dbs {
		dbIDs[i] = db.ID
	}
	a.databases = dbIDs
	a.loaded = true
	return nil
}

func (a *APIKeyOfAdmin) GetAllPermissions(ctx context.Context) (map[repository.ULID]repository.AccessGrant, error) {
	if err := a.loadDatabases(ctx); err != nil {
		return nil, err
	}
	perms := make(map[repository.ULID]repository.AccessGrant, len(a.databases))
	for _, dbID := range a.databases {
		perms[dbID] = a.Scope
	}
	return perms, nil
}

// UserPermissions represents a non-admin user (direct session or API key),
// limiting access to per-database grants intersected with Scope.
type UserPermissions struct {
	UserULID repository.ULID
	Scope    repository.AccessGrant
	Repo     repository.Repository

	loaded      bool
	permissions map[repository.ULID]repository.AccessGrant
}

func (u *UserPermissions) IsGlobalAdmin() bool {
	return false
}

func (u *UserPermissions) loadPermissions(ctx context.Context) error {
	if u.loaded {
		return nil
	}
	perms, err := u.Repo.GetAllUserPermissions(ctx, u.UserULID)
	if err != nil {
		return err
	}
	permsMap := make(map[repository.ULID]repository.AccessGrant, len(perms))
	for _, p := range perms {
		permsMap[p.DatabaseID] = p.Roles
	}
	u.permissions = permsMap
	u.loaded = true
	return nil
}

func (u *UserPermissions) HasPermission(ctx context.Context, database repository.ULID, ag repository.AccessGrant) (bool, error) {
	if err := u.loadPermissions(ctx); err != nil {
		return false, err
	}
	if perm, exists := u.permissions[database]; exists {
		// OR logic: if the user's specific database perm & scope has ANY of the requested bits
		return (u.Scope & perm & ag) != 0, nil
	}
	return false, nil
}

func (u *UserPermissions) GetAllPermissions(ctx context.Context) (map[repository.ULID]repository.AccessGrant, error) {
	if err := u.loadPermissions(ctx); err != nil {
		return nil, err
	}
	filtered := make(map[repository.ULID]repository.AccessGrant, len(u.permissions))
	for dbID, perm := range u.permissions {
		filtered[dbID] = perm & u.Scope
	}
	return filtered, nil
}
