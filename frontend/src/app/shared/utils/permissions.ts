// frontend/src/app/utils/permissions.ts

import { Permission, User } from '../../core/models/user.model';

export type PermissionFlag = Exclude<keyof Permission, 'database_id'>;

export interface ResolvedPermissions {
  canView: boolean;
  canCreate: boolean;
  canEdit: boolean;
  canDelete: boolean;
  canAdmin: boolean;
  isGlobalAdmin: boolean;
}

const EMPTY_PERMISSIONS: ResolvedPermissions = {
  canView: false,
  canCreate: false,
  canEdit: false,
  canDelete: false,
  canAdmin: false,
  isGlobalAdmin: false,
};

/**
 * Resolves all effective permissions a user holds for a specific database.
 * Global admins automatically receive all database permissions when a valid
 * database ID is provided.
 */
export function resolveDatabasePermissions(
  user: User | null | undefined,
  dbId: string | null | undefined,
): ResolvedPermissions {
  if (!user) {
    return { ...EMPTY_PERMISSIONS };
  }

  const isGlobalAdmin = Boolean(user.is_admin);
  if (!dbId) {
    return { ...EMPTY_PERMISSIONS, isGlobalAdmin };
  }

  if (isGlobalAdmin) {
    return {
      canView: true,
      canCreate: true,
      canEdit: true,
      canDelete: true,
      canAdmin: true,
      isGlobalAdmin: true,
    };
  }

  const dbPerm = user.permissions?.find((p) => p.database_id === dbId);
  if (!dbPerm) {
    return { ...EMPTY_PERMISSIONS };
  }

  return {
    canView: Boolean(dbPerm.can_view),
    canCreate: Boolean(dbPerm.can_create),
    canEdit: Boolean(dbPerm.can_edit),
    canDelete: Boolean(dbPerm.can_delete),
    canAdmin: Boolean(dbPerm.can_admin),
    isGlobalAdmin: false,
  };
}

/**
 * Checks whether a user holds a specific permission flag for a database.
 */
export function hasDatabasePermission(
  user: User | null | undefined,
  dbId: string | null | undefined,
  flag: PermissionFlag,
): boolean {
  if (!user || !dbId) {
    return false;
  }

  if (user.is_admin) {
    return true;
  }

  const dbPerm = user.permissions?.find((p) => p.database_id === dbId);
  return Boolean(dbPerm?.[flag]);
}

/**
 * Checks whether a user holds at least one role on the given database.
 */
export function hasAnyDatabasePermission(
  user: User | null | undefined,
  dbId: string | null | undefined,
): boolean {
  const resolved = resolveDatabasePermissions(user, dbId);
  return (
    resolved.canView ||
    resolved.canCreate ||
    resolved.canEdit ||
    resolved.canDelete ||
    resolved.canAdmin
  );
}
