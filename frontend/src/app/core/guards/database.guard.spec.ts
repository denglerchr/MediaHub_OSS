// frontend/src/app/guards/database.guard.spec.ts
// Regression spec for FE-015 (AI/Reports/20261006_frontend_review.md):
// "DatabaseGuard is stricter than the documented RBAC model", plus
// databaseAdminGuard and adminGuard route permission checks.
import { TestBed } from '@angular/core/testing';
import {
  ActivatedRouteSnapshot,
  CanActivateFn,
  GuardResult,
  Router,
  RouterStateSnapshot,
  UrlTree,
} from '@angular/router';
import { Observable, firstValueFrom, of } from 'rxjs';
import { adminGuard } from './admin.guard';
import { databaseAdminGuard, databaseGuard } from './database.guard';
import { AuthService } from '../services/auth.service';
import { NotificationService } from '../services/notification.service';
import { Permission, User } from '../models';

describe('databaseGuard / databaseAdminGuard / adminGuard', () => {
  let authSpy: jasmine.SpyObj<AuthService>;
  let routerSpy: jasmine.SpyObj<Router>;
  let notificationSpy: jasmine.SpyObj<NotificationService>;

  const dashboardRedirect = { toString: () => 'URLTREE:/dashboard' } as unknown as UrlTree;
  const loginRedirect = { toString: () => 'URLTREE:/login' } as unknown as UrlTree;
  const mockState = { url: '/dashboard/db/db1' } as RouterStateSnapshot;

  const routeFor = (id: string | null): ActivatedRouteSnapshot =>
    ({ paramMap: { get: () => id } }) as unknown as ActivatedRouteSnapshot;

  const userWithPermission = (permission: Partial<Permission>): User => ({
    id: 'u1',
    username: 'uploader',
    is_admin: false,
    account_type: 'local',
    permissions: [
      {
        database_id: 'db1',
        can_view: false,
        can_create: false,
        can_edit: false,
        can_delete: false,
        can_admin: false,
        ...permission,
      },
    ],
  });

  const runGuard = (guard: CanActivateFn, route: ActivatedRouteSnapshot): Promise<GuardResult> => {
    const result = TestBed.runInInjectionContext(() => guard(route, mockState));
    return result instanceof Observable ? firstValueFrom(result) : Promise.resolve(result);
  };

  beforeEach(() => {
    authSpy = jasmine.createSpyObj<AuthService>('AuthService', ['ensureCurrentUser']);
    routerSpy = jasmine.createSpyObj<Router>('Router', ['createUrlTree']);
    notificationSpy = jasmine.createSpyObj<NotificationService>('NotificationService', [
      'showError',
    ]);
    routerSpy.createUrlTree.and.callFake((commands: unknown[]) =>
      commands[0] === '/login' ? loginRedirect : dashboardRedirect,
    );

    TestBed.configureTestingModule({
      providers: [
        { provide: AuthService, useValue: authSpy },
        { provide: Router, useValue: routerSpy },
        { provide: NotificationService, useValue: notificationSpy },
      ],
    });
  });

  const activate = (guard: CanActivateFn, user: User | null, dbId: string | null = 'db1') => {
    authSpy.ensureCurrentUser.and.returnValue(of(user));
    return runGuard(guard, routeFor(dbId));
  };

  it('FE-015: a can_create-only user may enter the database (exit criterion: can reach the upload UI)', async () => {
    expect(await activate(databaseGuard, userWithPermission({ can_create: true })))
      .withContext(
        'POST /api/database/{id}/entry requires only CanCreate — the route must not bounce',
      )
      .toBe(true);
    expect(routerSpy.createUrlTree).not.toHaveBeenCalled();
    expect(notificationSpy.showError).not.toHaveBeenCalled();
  });

  it('FE-015: can_edit / can_delete / can_admin alone also grant access', async () => {
    for (const flag of ['can_edit', 'can_delete', 'can_admin'] as const) {
      const result = await activate(databaseGuard, userWithPermission({ [flag]: true }));
      expect(result).withContext(`a ${flag}-only user must pass the guard`).toBe(true);
    }
  });

  it('FE-015: can_view still passes (regression) and no permission still redirects', async () => {
    expect(await activate(databaseGuard, userWithPermission({ can_view: true }))).toBe(true);

    const denied = await activate(databaseGuard, userWithPermission({}));
    expect(denied)
      .withContext('no database role at all must still redirect')
      .toBe(dashboardRedirect);
    expect(notificationSpy.showError).toHaveBeenCalledTimes(1);
    expect(routerSpy.createUrlTree).toHaveBeenCalledWith(['/dashboard']);
  });

  it('FE-015: global admins bypass the permission check and a missing id redirects', async () => {
    const admin = {
      id: 'u0',
      username: 'root',
      is_admin: true,
      account_type: 'local',
      permissions: [],
    } as User;
    expect(await activate(databaseGuard, admin)).toBe(true);
    expect(await activate(databaseGuard, admin, null)).toBe(dashboardRedirect);
  });

  it('databaseAdminGuard requires can_admin or global admin and rejects non-admin roles', async () => {
    expect(await activate(databaseAdminGuard, userWithPermission({ can_admin: true }))).toBe(true);

    const admin = {
      id: 'u0',
      username: 'root',
      is_admin: true,
      account_type: 'local',
      permissions: [],
    } as User;
    expect(await activate(databaseAdminGuard, admin)).toBe(true);

    const denied = await activate(
      databaseAdminGuard,
      userWithPermission({ can_view: true, can_create: true, can_edit: true, can_delete: true }),
    );
    expect(denied).toBe(dashboardRedirect);
    expect(notificationSpy.showError).toHaveBeenCalledWith(
      'Access Denied: Administrator privileges are required for this database.',
    );

    expect(await activate(databaseAdminGuard, null)).toBe(loginRedirect);
    expect(await activate(databaseAdminGuard, admin, null)).toBe(dashboardRedirect);
  });

  it('adminGuard enforces global is_admin and redirects unauthenticated or non-admin users', async () => {
    expect(await activate(adminGuard, null)).toBe(loginRedirect);
    expect(await activate(adminGuard, userWithPermission({ can_admin: true }))).toBe(
      dashboardRedirect,
    );
    expect(notificationSpy.showError).toHaveBeenCalledWith(
      'Access Denied: Administrator privileges are required.',
    );

    const admin = {
      id: 'u0',
      username: 'root',
      is_admin: true,
      account_type: 'local',
      permissions: [],
    } as User;
    expect(await activate(adminGuard, admin)).toBe(true);
  });
});
