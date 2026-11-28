import { inject } from '@angular/core';
import { ActivatedRouteSnapshot, CanActivateFn, Router } from '@angular/router';
import { hasAnyDatabasePermission, hasDatabasePermission } from '../../shared/utils/permissions';
import { checkUserAccess } from './admin.guard';

export function databasePermissionGuard(mode: 'any' | 'admin'): CanActivateFn {
  return (route: ActivatedRouteSnapshot) => {
    const router = inject(Router);
    // Get the database ID from the URL path (e.g., /dashboard/db/:id)
    const dbId = route.paramMap.get('id');

    if (!dbId) {
      return router.createUrlTree(['/dashboard']);
    }

    // FE-015: 'any' mode grants access on ANY database role — CanView, CanCreate,
    // CanEdit, CanDelete or CanAdmin. 'admin' mode requires global admin or CanAdmin.
    return checkUserAccess(
      (user) =>
        mode === 'admin'
          ? hasDatabasePermission(user, dbId, 'can_admin')
          : hasAnyDatabasePermission(user, dbId),
      mode === 'admin'
        ? 'Access Denied: Administrator privileges are required for this database.'
        : 'Access Denied: You do not have any permission for this database.',
    );
  };
}

export const databaseGuard: CanActivateFn = databasePermissionGuard('any');
export const databaseAdminGuard: CanActivateFn = databasePermissionGuard('admin');
