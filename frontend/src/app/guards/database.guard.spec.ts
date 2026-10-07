// frontend/src/app/guards/database.guard.spec.ts
// Regression spec for FE-015 (AI/Reports/20261006_frontend_review.md):
// "DatabaseGuard is stricter than the documented RBAC model".
//
// The contract (AI/Concept/02_03_00_HTTP_Database.md:209) grants access to
// GET /api/database/{id} on ANY database role — CanView, CanCreate, CanEdit,
// CanDelete or CanAdmin. The old guard required can_view || can_admin, so a
// `can_create`-only user saw the database on the dashboard, clicked it, and got
// "Access Denied" with no path to the upload UI the backend would accept.
import { ActivatedRouteSnapshot, Router, UrlTree } from '@angular/router';
import { Observable, firstValueFrom, of } from 'rxjs';
import { DatabaseGuard } from './database.guard';
import { AuthService } from '../services/auth.service';
import { NotificationService } from '../services/notification.service';
import { Permission, User } from '../models';

describe('DatabaseGuard — FE-015 RBAC widening', () => {
  let guard: DatabaseGuard;
  let authSpy: jasmine.SpyObj<AuthService>;
  let routerSpy: jasmine.SpyObj<Router>;
  let notificationSpy: jasmine.SpyObj<NotificationService>;

  const redirectStub = { toString: () => 'URLTREE:/dashboard' } as unknown as UrlTree;

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

  const run = async (route: ActivatedRouteSnapshot): Promise<boolean | UrlTree> => {
    const result = guard.canActivate(route);
    return result instanceof Observable ? firstValueFrom(result) : result;
  };

  beforeEach(() => {
    authSpy = jasmine.createSpyObj<AuthService>('AuthService', ['ensureCurrentUser']);
    routerSpy = jasmine.createSpyObj<Router>('Router', ['createUrlTree']);
    notificationSpy = jasmine.createSpyObj<NotificationService>('NotificationService', [
      'showError',
    ]);
    routerSpy.createUrlTree.and.returnValue(redirectStub);

    guard = new DatabaseGuard(authSpy, routerSpy, notificationSpy);
  });

  const activate = (user: User | null): Promise<boolean | UrlTree> => {
    authSpy.ensureCurrentUser.and.returnValue(of(user));
    return run(routeFor('db1'));
  };

  it('FE-015: a can_create-only user may enter the database (exit criterion: can reach the upload UI)', async () => {
    expect(await activate(userWithPermission({ can_create: true })))
      .withContext(
        'POST /api/database/{id}/entry requires only CanCreate — the route must not bounce',
      )
      .toBe(true);
    expect(routerSpy.createUrlTree).not.toHaveBeenCalled();
    expect(notificationSpy.showError).not.toHaveBeenCalled();
  });

  it('FE-015: can_edit / can_delete / can_admin alone also grant access', async () => {
    for (const flag of ['can_edit', 'can_delete', 'can_admin'] as const) {
      const result = await activate(userWithPermission({ [flag]: true }));
      expect(result).withContext(`a ${flag}-only user must pass the guard`).toBe(true);
    }
  });

  it('FE-015: can_view still passes (regression) and no permission still redirects', async () => {
    expect(await activate(userWithPermission({ can_view: true }))).toBe(true);

    const denied = await activate(userWithPermission({}));
    expect(denied).withContext('no database role at all must still redirect').toBe(redirectStub);
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
    expect(await activate(admin)).toBe(true);

    authSpy.ensureCurrentUser.and.returnValue(of(admin));
    expect(await run(routeFor(null))).toBe(redirectStub);
  });
});
