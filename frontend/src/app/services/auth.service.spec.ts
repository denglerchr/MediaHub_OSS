// frontend/src/app/services/auth.service.spec.ts
// Regression specs for FE-007 (logout revokes the currently stored refresh token),
// FE-008 (tokens out of localStorage), FE-009 (multi-tab refresh race),
// FE-010 (transient errors must not destroy the session) and FE-018 (?local=1).
// The second describe block covers the Phase E FE-007/N2/N4 corrections: the
// revocation request is now preceded by a silent refresh (so an expired access
// token no longer skips server-side revocation) and waits for in-flight refreshes.
import { TestBed } from '@angular/core/testing';
import {
  HttpClientTestingModule,
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import {
  HttpClient,
  HttpErrorResponse,
  provideHttpClient,
  withInterceptors,
} from '@angular/common/http';
import { Router } from '@angular/router';
import { AuthService } from './auth.service';
import { jwtInterceptorFn } from '../interceptors/jwt.interceptor';
import { SKIP_AUTH_REFRESH } from '../interceptors/http-context.tokens';
import { User, TokenResponse } from '../models';
import { NotificationService } from './notification.service';

describe('AuthService — FE-007/FE-008/FE-009/FE-010/FE-018 regressions', () => {
  let service: AuthService;
  let httpMock: HttpTestingController;
  let routerSpy: jasmine.SpyObj<Router>;

  const user = {
    id: 'u1',
    username: 'alice',
    is_admin: false,
    permissions: [],
    account_type: 'local',
  } as unknown as User;

  beforeEach(() => {
    // Karma runs every spec in one page — start from a clean storage so the
    // FE-008 migration in the constructor cannot pick up leftovers.
    localStorage.clear();
    sessionStorage.clear();

    routerSpy = jasmine.createSpyObj<Router>('Router', ['navigate'], {
      routerState: { snapshot: { root: { queryParams: {} } } } as any,
    });

    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule],
      providers: [AuthService, { provide: Router, useValue: routerSpy }],
    });
    service = TestBed.inject(AuthService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    httpMock.verify();
  });

  function login(): void {
    service.basicAuthLogin('alice', 'pw').subscribe({ error: () => {} });
    httpMock
      .expectOne('/api/token')
      .flush({ access_token: 'A1', refresh_token: 'R1' } as TokenResponse);
    httpMock.expectOne('/api/me').flush(user);
  }

  // --- FE-008 ---

  it('FE-008: tokens are kept out of localStorage (access in memory, refresh in sessionStorage)', () => {
    login();

    expect(service.getAccessToken()).toBe('A1');
    expect(service.getRefreshToken()).toBe('R1');
    expect(localStorage.getItem('access_token')).toBeNull();
    expect(localStorage.getItem('refresh_token')).toBeNull();
    expect(sessionStorage.getItem('refresh_token')).toBe('R1');
  });

  it('FE-008: a legacy localStorage refresh token is migrated to sessionStorage and scrubbed', () => {
    localStorage.setItem('access_token', 'legacy-access');
    localStorage.setItem('refresh_token', 'legacy-refresh');

    const fresh = new AuthService(
      TestBed.inject(HttpClient),
      routerSpy,
      TestBed.inject(NotificationService),
    );

    expect(fresh.getRefreshToken()).toBe('legacy-refresh');
    expect(fresh.getAccessToken()).toBeNull(); // access token is no longer persisted
    expect(localStorage.getItem('access_token')).toBeNull();
    expect(localStorage.getItem('refresh_token')).toBeNull();
    expect(sessionStorage.getItem('refresh_token')).toBe('legacy-refresh');
  });

  it('FE-008: after a reload the session is silently restored from the sessionStorage refresh token', () => {
    sessionStorage.setItem('refresh_token', 'R1');
    // The in-memory access token is gone after a reload — that must not log us out.

    const received: { user: User | null } = { user: null };
    service.ensureCurrentUser().subscribe((u) => (received.user = u));

    httpMock
      .expectOne('/api/token/refresh')
      .flush({ access_token: 'A2', refresh_token: 'R2' } as TokenResponse);
    httpMock.expectOne('/api/me').flush(user);

    expect(received.user).toEqual(user);
    expect(service.getAccessToken()).toBe('A2');
    expect(service.getRefreshToken()).toBe('R2');
  });

  // --- FE-007 ---

  it('FE-007: logout revokes the *currently stored* (rotated) refresh token and is flagged SKIP_AUTH_REFRESH', () => {
    login();

    // A mid-session refresh rotates the pair (as the interceptor does on 401).
    service.refreshToken().subscribe();
    httpMock
      .expectOne('/api/token/refresh')
      .flush({ access_token: 'A2', refresh_token: 'R2' } as TokenResponse);

    service.logout();
    const logoutReq = httpMock.expectOne('/api/logout');

    // The body must carry the token stored at send time (R2), not the stale R1 —
    // otherwise the backend deletes an already-deleted row and R2 lives on.
    expect(logoutReq.request.body.refresh_token).toBe('R2');
    expect(logoutReq.request.context.get(SKIP_AUTH_REFRESH)).toBeTrue();

    logoutReq.flush({ message: 'Logged out successfully.' });
    expect(service.getRefreshToken()).toBeNull();
    expect(service.getAccessToken()).toBeNull();
  });

  it('FE-007: logout without a stored refresh token just clears the session locally', () => {
    service.logout();
    httpMock.expectNone('/api/logout');
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/login'], { queryParams: {} });
  });

  // --- FE-009 ---

  it('FE-009: concurrent refresh calls share a single refresh request (single-flight)', () => {
    sessionStorage.setItem('refresh_token', 'R1');

    service.refreshToken().subscribe();
    service.refreshToken().subscribe();

    httpMock
      .expectOne('/api/token/refresh')
      .flush({ access_token: 'A2', refresh_token: 'R2' } as TokenResponse);
    httpMock.expectNone('/api/token/refresh');
    expect(service.getRefreshToken()).toBe('R2');
  });

  it('FE-009: a 401 refresh retries once with the currently stored token before failing', () => {
    sessionStorage.setItem('refresh_token', 'R1');

    const received: { tokens: TokenResponse | null } = { tokens: null };
    let failed = false;
    service.refreshToken().subscribe({
      next: (t) => (received.tokens = t),
      error: () => (failed = true),
    });

    const first = httpMock.expectOne('/api/token/refresh');
    expect(first.request.body.refresh_token).toBe('R1');
    // Meanwhile another actor (e.g. a duplicated tab) stored a rotated token.
    sessionStorage.setItem('refresh_token', 'R2');
    first.flush(null, { status: 401, statusText: 'Unauthorized' });

    const retry = httpMock.expectOne('/api/token/refresh');
    expect(retry.request.body.refresh_token).toBe('R2');
    retry.flush({ access_token: 'A3', refresh_token: 'R3' } as TokenResponse);

    expect(failed).toBeFalse();
    expect(received.tokens?.access_token).toBe('A3');
  });

  it('FE-009: a 401 refresh with an unchanged stored token does NOT retry (session is dead)', () => {
    sessionStorage.setItem('refresh_token', 'R1');

    let failed = false;
    service.refreshToken().subscribe({ error: () => (failed = true) });

    httpMock
      .expectOne('/api/token/refresh')
      .flush(null, { status: 401, statusText: 'Unauthorized' });
    httpMock.expectNone('/api/token/refresh');
    expect(failed).toBeTrue();
  });

  // --- FE-010 ---

  it('FE-010: a transient 500 on /api/me keeps the session alive', () => {
    login();

    const received: { user: User | null } = { user };
    service.fetchCurrentUser().subscribe((u) => (received.user = u));
    httpMock.expectOne('/api/me').flush(null, { status: 500, statusText: 'Server Error' });

    expect(received.user).toBeNull();
    expect(service.getRefreshToken()).toBe('R1'); // session untouched
    expect(routerSpy.navigate).not.toHaveBeenCalled();
  });

  it('FE-010: a network failure (status 0) on /api/me keeps the session alive', () => {
    login();

    service.fetchCurrentUser().subscribe();
    httpMock.expectOne('/api/me').flush(null, { status: 0, statusText: 'Unknown Error' });

    expect(service.getRefreshToken()).toBe('R1');
    expect(routerSpy.navigate).not.toHaveBeenCalled();
  });

  it('FE-010: a genuine 401 on /api/me clears the session and redirects', () => {
    login();

    service.fetchCurrentUser().subscribe();
    httpMock.expectOne('/api/me').flush(null, { status: 401, statusText: 'Unauthorized' });

    expect(service.getRefreshToken()).toBeNull();
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/login'], { queryParams: {} });
  });

  // --- FE-018 ---

  it('FE-018: a failed local login clears tokens without navigating (the ?local=1 escape hatch survives)', () => {
    let failed = false;
    service.basicAuthLogin('alice', 'wrong').subscribe({ error: () => (failed = true) });
    httpMock.expectOne('/api/token').flush(null, { status: 401, statusText: 'Unauthorized' });

    expect(failed).toBeTrue();
    expect(service.getAccessToken()).toBeNull();
    expect(service.getRefreshToken()).toBeNull();
    expect(routerSpy.navigate).not.toHaveBeenCalled();
  });

  it('FE-018: when the session dies on /login?local=1, the redirect keeps the ?local=1 param', () => {
    (routerSpy.routerState.snapshot.root as any).queryParams = { local: '1' };

    service.logout(false);

    expect(routerSpy.navigate).toHaveBeenCalledWith(['/login'], { queryParams: { local: '1' } });
  });
});

// --- Phase E: FE-007/N2/N4 corrections — refresh BEFORE logout -----------------
// These specs register the real `jwtInterceptorFn` (via provideHttpClient — the
// class-based HTTP_INTERCEPTORS registration is deprecated on Angular 20) so the
// logout request carries the Bearer header exactly like in the app. The interceptor
// itself is unchanged: a 401 on the logout request is still NOT refreshed/retried
// (jwt.interceptor.spec.ts).
describe('AuthService — FE-007/N2/N4 refresh-before-logout corrections', () => {
  let service: AuthService;
  let httpMock: HttpTestingController;
  let routerSpy: jasmine.SpyObj<Router>;

  const user = {
    id: 'u1',
    username: 'alice',
    is_admin: false,
    permissions: [],
    account_type: 'local',
  } as unknown as User;

  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();

    routerSpy = jasmine.createSpyObj<Router>('Router', ['navigate'], {
      routerState: { snapshot: { root: { queryParams: {} } } } as any,
    });

    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([jwtInterceptorFn])),
        provideHttpClientTesting(),
        AuthService,
        { provide: Router, useValue: routerSpy },
      ],
    });
    service = TestBed.inject(AuthService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    httpMock.verify();
  });

  it('FE-007 (a/b/c): logout after idle silently refreshes first, then revokes the ROTATED token with a Bearer header', () => {
    // >5 min idle / reload: the refresh token survived in sessionStorage while the
    // in-memory access token is gone. POST /api/logout sits behind AuthMiddleware,
    // so without a fresh access token the revocation would 401 before the handler.
    sessionStorage.setItem('refresh_token', 'R2');

    service.logout();

    // (a) the silent refresh fires first — before any logout request exists
    const refreshReq = httpMock.expectOne('/api/token/refresh');
    expect(refreshReq.request.body.refresh_token).toBe('R2');
    httpMock.expectNone('/api/logout');
    refreshReq.flush({ access_token: 'A3', refresh_token: 'R3' } as TokenResponse);

    // (b) the body carries the ROTATED token and a valid Bearer header
    const logoutReq = httpMock.expectOne('/api/logout');
    expect(logoutReq.request.body.refresh_token).toBe('R3');
    expect(logoutReq.request.headers.get('Authorization')).toBe('Bearer A3');

    // (c) the logout request itself is still flagged SKIP_AUTH_REFRESH
    expect(logoutReq.request.context.get(SKIP_AUTH_REFRESH)).toBeTrue();

    logoutReq.flush({ message: 'Logged out successfully.' });
    expect(service.getRefreshToken()).toBeNull();
    expect(service.getAccessToken()).toBeNull();
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/login'], { queryParams: {} });
  });

  it('FE-007: an EXPIRED (but still present) access token is refreshed before the revocation POST', () => {
    // A tab that stayed open past the access-token TTL: the JWT is in memory but dead.
    const expiredJwt = `hdr.${btoa(JSON.stringify({ exp: 1000 }))}.sig`;

    // Establish a session, then let the access token expire in place (idle > TTL).
    service.basicAuthLogin('alice', 'pw').subscribe({ error: () => {} });
    httpMock
      .expectOne('/api/token')
      .flush({ access_token: 'A1', refresh_token: 'R1' } as TokenResponse);
    httpMock.expectOne('/api/me').flush(user);
    service.refreshToken().subscribe();
    httpMock
      .expectOne('/api/token/refresh')
      .flush({ access_token: expiredJwt, refresh_token: 'R2' } as TokenResponse);
    expect(service.getAccessToken()).toBe(expiredJwt);

    service.logout();

    // The dead access token must not skip the revocation: silent refresh first...
    const refreshReq = httpMock.expectOne('/api/token/refresh');
    expect(refreshReq.request.body.refresh_token).toBe('R2');
    refreshReq.flush({ access_token: 'A3', refresh_token: 'R3' } as TokenResponse);

    // ...then the revocation with the rotated token and a valid Bearer.
    const logoutReq = httpMock.expectOne('/api/logout');
    expect(logoutReq.request.body.refresh_token).toBe('R3');
    expect(logoutReq.request.headers.get('Authorization')).toBe('Bearer A3');
    logoutReq.flush({ message: 'Logged out successfully.' });
    expect(service.getRefreshToken()).toBeNull();
  });

  it('FE-007 (d): a failing silent refresh still clears the local session (cleanup always runs)', () => {
    sessionStorage.setItem('refresh_token', 'R2');

    service.logout();

    // Transient refresh failure: the refresh token survives in storage, so the
    // best-effort revocation still goes out with the un-rotated token.
    httpMock
      .expectOne('/api/token/refresh')
      .flush(null, { status: 500, statusText: 'Server Error' });
    const logoutReq = httpMock.expectOne('/api/logout');
    expect(logoutReq.request.body.refresh_token).toBe('R2');
    logoutReq.flush(null, { status: 401, statusText: 'Unauthorized' });

    expect(service.getRefreshToken()).toBeNull();
    expect(service.getAccessToken()).toBeNull();
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/login'], { queryParams: {} });
  });

  it('FE-007 (d): a 401 on the silent refresh means the token is already dead — session cleared without a logout POST', () => {
    sessionStorage.setItem('refresh_token', 'R2');

    service.logout();

    httpMock
      .expectOne('/api/token/refresh')
      .flush(null, { status: 401, statusText: 'Unauthorized' });
    httpMock.expectNone('/api/logout');
    expect(service.getRefreshToken()).toBeNull();
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/login'], { queryParams: {} });
  });

  it('N4 (e): logout racing an in-flight refresh revokes the POST-rotation token', () => {
    sessionStorage.setItem('refresh_token', 'R1');

    // A 401 somewhere else is mid-refresh (rotation will replace R1 with R3).
    service.refreshToken().subscribe();
    service.logout();

    // The logout body must be built only after the rotation settles.
    httpMock.expectNone('/api/logout');
    httpMock
      .expectOne('/api/token/refresh')
      .flush({ access_token: 'A3', refresh_token: 'R3' } as TokenResponse);

    const logoutReq = httpMock.expectOne('/api/logout');
    expect(logoutReq.request.body.refresh_token).toBe('R3');
    expect(logoutReq.request.headers.get('Authorization')).toBe('Bearer A3');
    logoutReq.flush({ message: 'Logged out successfully.' });
    expect(service.getRefreshToken()).toBeNull();
    expect(service.getAccessToken()).toBeNull();
  });
});
