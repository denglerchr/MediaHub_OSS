package utils

import (
	"context"
	"mediahub_oss/internal/repository"
)

type PermissionHolder interface {
	IsGlobalAdmin() bool
	HasPermission(database repository.ULID, ag repository.AccessGrant) bool // check with OR logic, one of the ag bits is enough

	GetUserULID() repository.ULID
	GetAllPermissions(ctx context.Context) (map[repository.ULID]repository.AccessGrant, error)
}

// There are three types of permission holders:
// **GlobalAdmin**: has full access to all databases and actions.
// **APIKeyOfAdmin**: has access limited only by the scope of the API key
// **UserPermissions**: has access limited by the specific database permissions and a potential API key scope

type GlobalAdmin struct {
	UserULID repository.ULID
}

func (g *GlobalAdmin) IsGlobalAdmin() bool {
	return true
}

func (g *GlobalAdmin) HasPermission(database repository.ULID, ag repository.AccessGrant) bool {
	return true
}

func (g *GlobalAdmin) GetUserULID() repository.ULID {
	return g.UserULID
}

func (g *GlobalAdmin) GetAllPermissions(ctx context.Context) (map[repository.ULID]repository.AccessGrant, error) {
	return map[repository.ULID]repository.AccessGrant{}, nil // Global admin has implicit access to all databases
}

// An API key without admin scope of an admin user
type APIKeyOfAdmin struct {
	UserULID repository.ULID
	Scope    repository.AccessGrant
	Repo     repository.Repository

	loaded    bool
	databases []repository.ULID
}

func (a *APIKeyOfAdmin) IsGlobalAdmin() bool {
	return false
}

func (a *APIKeyOfAdmin) HasPermission(database repository.ULID, ag repository.AccessGrant) bool {
	// OR logic: if the API key scope has ANY of the requested bits
	return (a.Scope & ag) != 0
}

func (a *APIKeyOfAdmin) GetUserULID() repository.ULID {
	return a.UserULID
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

// A non-admin user with specific database permissions
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

func (u *UserPermissions) loadPermissions(ctx context.Context) {
	if u.loaded {
		return
	}
	perms, err := u.Repo.GetAllUserPermissions(ctx, u.UserULID)
	if err != nil {
		u.permissions = make(map[repository.ULID]repository.AccessGrant)
		u.loaded = true
		return
	}
	permsMap := make(map[repository.ULID]repository.AccessGrant, len(perms))
	for _, p := range perms {
		permsMap[p.DatabaseID] = p.Roles
	}
	u.permissions = permsMap
	u.loaded = true
}

func (u *UserPermissions) HasPermission(database repository.ULID, ag repository.AccessGrant) bool {
	u.loadPermissions(context.Background())
	if perm, exists := u.permissions[database]; exists {
		// OR logic: if the user's specific database perm & scope has ANY of the requested bits
		return (u.Scope & perm & ag) != 0
	}
	return false
}

func (u *UserPermissions) GetUserULID() repository.ULID {
	return u.UserULID
}

func (u *UserPermissions) GetAllPermissions(ctx context.Context) (map[repository.ULID]repository.AccessGrant, error) {
	u.loadPermissions(ctx)
	filtered := make(map[repository.ULID]repository.AccessGrant, len(u.permissions))
	for dbID, perm := range u.permissions {
		filtered[dbID] = perm & u.Scope
	}
	return filtered, nil
}
