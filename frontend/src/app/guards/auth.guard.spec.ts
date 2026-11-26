// frontend/src/app/guards/auth.guard.spec.ts
import { TestBed } from '@angular/core/testing';
import {
  ActivatedRouteSnapshot,
  Router,
  RouterStateSnapshot,
  UrlTree,
  convertToParamMap,
} from '@angular/router';
import { Observable, firstValueFrom, of } from 'rxjs';
import { AuthGuard } from './auth.guard';
import { AuthService } from '../services/auth.service';
import { AppInfoService } from '../services/app-info.service';
import { AppInfo, User } from '../models';

describe('AuthGuard', () => {
  let guard: AuthGuard;
  let authSpy: jasmine.SpyObj<AuthService>;
  let appInfoSpy: jasmine.SpyObj<AppInfoService>;
  let routerSpy: jasmine.SpyObj<Router>;

  const mockUser = { id: 'u1', username: 'alice' } as unknown as User;
  const mockState = { url: '/dashboard' } as RouterStateSnapshot;

  function makeRoute(query: Record<string, string> = {}): ActivatedRouteSnapshot {
    return {
      queryParamMap: convertToParamMap(query),
    } as unknown as ActivatedRouteSnapshot;
  }

  beforeEach(() => {
    authSpy = jasmine.createSpyObj<AuthService>('AuthService', [
      'ensureCurrentUser',
      'redirectToOidc',
    ]);
    appInfoSpy = jasmine.createSpyObj<AppInfoService>('AppInfoService', ['loadInfo']);
    routerSpy = jasmine.createSpyObj<Router>('Router', ['createUrlTree']);
    routerSpy.createUrlTree.and.callFake((commands, extras) => {
      return { commands, extras } as unknown as UrlTree;
    });

    TestBed.configureTestingModule({
      providers: [
        AuthGuard,
        { provide: AuthService, useValue: authSpy },
        { provide: AppInfoService, useValue: appInfoSpy },
        { provide: Router, useValue: routerSpy },
      ],
    });

    guard = TestBed.inject(AuthGuard);
  });

  it('allows activation when an authenticated user exists', async () => {
    authSpy.ensureCurrentUser.and.returnValue(of(mockUser));

    const result = await firstValueFrom(
      guard.canActivate(makeRoute(), mockState) as Observable<boolean | UrlTree>,
    );

    expect(result).toBeTrue();
    expect(appInfoSpy.loadInfo).not.toHaveBeenCalled();
  });

  it('redirects to /auth/callback with query params when unauthenticated and code is present', async () => {
    authSpy.ensureCurrentUser.and.returnValue(of(null));

    const result = await firstValueFrom(
      guard.canActivate(makeRoute({ code: 'abc', state: 'xyz' }), mockState) as Observable<
        boolean | UrlTree
      >,
    );

    expect(routerSpy.createUrlTree).toHaveBeenCalledWith(['/auth/callback'], {
      queryParams: { code: 'abc', state: 'xyz' },
    });
    expect(result).toBeTruthy();
  });

  it('forwards ?error=access_denied to /auth/callback instead of calling redirectToOidc when login_page_disabled is true', async () => {
    authSpy.ensureCurrentUser.and.returnValue(of(null));
    appInfoSpy.loadInfo.and.returnValue(
      of({
        oidc: {
          enabled: true,
          login_page_disabled: true,
          oidc_issuer_url: 'https://idp.example.com/realms/main',
          oidc_client_id: 'mediahub',
        },
      } as unknown as AppInfo),
    );

    const result = await firstValueFrom(
      guard.canActivate(makeRoute({ error: 'access_denied' }), mockState) as Observable<
        boolean | UrlTree
      >,
    );

    expect(routerSpy.createUrlTree).toHaveBeenCalledWith(['/auth/callback'], {
      queryParams: { error: 'access_denied' },
    });
    expect(authSpy.redirectToOidc).not.toHaveBeenCalled();
    expect(result).toBeTruthy();
  });

  it('redirects to /login when unauthenticated and OIDC auto-redirect is not enabled', async () => {
    authSpy.ensureCurrentUser.and.returnValue(of(null));
    appInfoSpy.loadInfo.and.returnValue(
      of({ oidc: { enabled: false, login_page_disabled: false } } as unknown as AppInfo),
    );

    await firstValueFrom(
      guard.canActivate(makeRoute(), mockState) as Observable<boolean | UrlTree>,
    );

    expect(routerSpy.createUrlTree).toHaveBeenCalledWith(['/login']);
  });

  it('redirects to OIDC and returns false when login_page_disabled is true', async () => {
    authSpy.ensureCurrentUser.and.returnValue(of(null));
    const oidc = {
      enabled: true,
      login_page_disabled: true,
      oidc_issuer_url: 'https://idp.example.com/realms/main',
      oidc_client_id: 'mediahub',
    };
    appInfoSpy.loadInfo.and.returnValue(of({ oidc } as unknown as AppInfo));
    authSpy.redirectToOidc.and.returnValue(Promise.resolve());

    const result = await firstValueFrom(
      guard.canActivate(makeRoute(), mockState) as Observable<boolean | UrlTree>,
    );

    expect(authSpy.redirectToOidc).toHaveBeenCalledWith(oidc);
    expect(result).toBeFalse();
  });

  it('falls back to /login?error=sso_failed when redirectToOidc rejects', async () => {
    spyOn(console, 'error');
    authSpy.ensureCurrentUser.and.returnValue(of(null));
    const oidc = {
      enabled: true,
      login_page_disabled: true,
      oidc_issuer_url: 'javascript:alert(1)',
      oidc_client_id: 'mediahub',
    };
    appInfoSpy.loadInfo.and.returnValue(of({ oidc } as unknown as AppInfo));
    authSpy.redirectToOidc.and.returnValue(Promise.reject(new Error('Invalid URL')));

    await firstValueFrom(
      guard.canActivate(makeRoute(), mockState) as Observable<boolean | UrlTree>,
    );

    expect(routerSpy.createUrlTree).toHaveBeenCalledWith(['/login'], {
      queryParams: { error: 'sso_failed' },
    });
  });
});
