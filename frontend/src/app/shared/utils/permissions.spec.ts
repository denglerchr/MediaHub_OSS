// frontend/src/app/utils/permissions.spec.ts

import { Permission, User } from '../../core/models/user.model';
import {
  hasAnyDatabasePermission,
  hasDatabasePermission,
  PermissionFlag,
  resolveDatabasePermissions,
} from './permissions';

describe('permissions utilities', () => {
  const makeUser = (overrides: Partial<User> = {}): User => ({
    id: 'u1',
    username: 'alice',
    is_admin: false,
    account_type: 'local',
    permissions: [],
    ...overrides,
  });

  const makePerm = (dbId: string, overrides: Partial<Permission> = {}): Permission => ({
    database_id: dbId,
    can_view: false,
    can_create: false,
    can_edit: false,
    can_delete: false,
    can_admin: false,
    ...overrides,
  });

  it('returns false for all permissions when user or dbId is missing', () => {
    const user = makeUser({ permissions: [makePerm('db1', { can_view: true })] });

    expect(resolveDatabasePermissions(null, 'db1')).toEqual({
      canView: false,
      canCreate: false,
      canEdit: false,
      canDelete: false,
      canAdmin: false,
      isGlobalAdmin: false,
    });
    expect(resolveDatabasePermissions(user, null)).toEqual({
      canView: false,
      canCreate: false,
      canEdit: false,
      canDelete: false,
      canAdmin: false,
      isGlobalAdmin: false,
    });
    expect(hasDatabasePermission(undefined, 'db1', 'can_view')).toBeFalse();
    expect(hasDatabasePermission(user, '', 'can_view')).toBeFalse();
    expect(hasAnyDatabasePermission(user, undefined)).toBeFalse();
  });

  it('grants all database permissions to global admins when dbId is provided', () => {
    const admin = makeUser({ is_admin: true, permissions: [] });

    expect(resolveDatabasePermissions(admin, 'db1')).toEqual({
      canView: true,
      canCreate: true,
      canEdit: true,
      canDelete: true,
      canAdmin: true,
      isGlobalAdmin: true,
    });
    expect(hasDatabasePermission(admin, 'db1', 'can_delete')).toBeTrue();
    expect(hasAnyDatabasePermission(admin, 'db1')).toBeTrue();

    expect(resolveDatabasePermissions(admin, null)).toEqual({
      canView: false,
      canCreate: false,
      canEdit: false,
      canDelete: false,
      canAdmin: false,
      isGlobalAdmin: true,
    });
    expect(hasAnyDatabasePermission(admin, null)).toBeFalse();
  });

  it('resolves exact per-database flags for non-admin users', () => {
    const user = makeUser({
      permissions: [
        makePerm('db1', { can_view: true, can_edit: true }),
        makePerm('db2', { can_admin: true }),
      ],
    });

    expect(resolveDatabasePermissions(user, 'db1')).toEqual({
      canView: true,
      canCreate: false,
      canEdit: true,
      canDelete: false,
      canAdmin: false,
      isGlobalAdmin: false,
    });

    expect(hasDatabasePermission(user, 'db1', 'can_view')).toBeTrue();
    expect(hasDatabasePermission(user, 'db1', 'can_edit')).toBeTrue();
    expect(hasDatabasePermission(user, 'db1', 'can_admin')).toBeFalse();
    expect(hasDatabasePermission(user, 'db2', 'can_admin')).toBeTrue();
    expect(hasDatabasePermission(user, 'db3', 'can_view')).toBeFalse();
  });

  it('hasAnyDatabasePermission returns true if any single flag is set and false when none are', () => {
    const flags: PermissionFlag[] = [
      'can_view',
      'can_create',
      'can_edit',
      'can_delete',
      'can_admin',
    ];

    for (const flag of flags) {
      const user = makeUser({ permissions: [makePerm('db1', { [flag]: true })] });
      expect(hasAnyDatabasePermission(user, 'db1')).toBeTrue();
    }

    const noRoleUser = makeUser({ permissions: [makePerm('db1')] });
    expect(hasAnyDatabasePermission(noRoleUser, 'db1')).toBeFalse();
  });
});
