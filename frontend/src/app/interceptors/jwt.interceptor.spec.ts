// frontend/src/app/interceptors/jwt.interceptor.spec.ts
// Regression specs for FE-006 (a failing retried request must not log the user out)
// and FE-007 (the logout request must never enter the refresh+retry path).
// Step 8: the interceptor is functional (`jwtInterceptorFn`) and registered through
// provideHttpClient(withInterceptors(...)); the specs exercise that exact wiring.
import { TestBed } from '@angular/core/testing';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import {
  provideHttpClient,
  withInterceptors,
  HttpClient,
  HttpContext,
  HttpErrorResponse,
} from '@angular/common/http';
import { of, throwError } from 'rxjs';
import { jwtInterceptorFn } from './jwt.interceptor';
import { SKIP_AUTH_REFRESH } from './http-context.tokens';
import { AuthService } from '../services/auth.service';
import { TokenResponse } from '../models';

describe('JwtInterceptor — FE-006/FE-007 regressions', () => {
  let http: HttpClient;
  let httpMock: HttpTestingController;
  let authSpy: jasmine.SpyObj<AuthService>;
  let capturedError: HttpErrorResponse | null;

  const freshTokens = {
    access_token: 'fresh-access',
    refresh_token: 'fresh-refresh',
  } as TokenResponse;

  beforeEach(() => {
    capturedError = null;
    authSpy = jasmine.createSpyObj<AuthService>('AuthService', [
      'getAccessToken',
      'getRefreshToken',
      'refreshToken',
      'logout',
    ]);
    authSpy.getAccessToken.and.returnValue('stale-access');
    authSpy.getRefreshToken.and.returnValue('stored-refresh');
    authSpy.refreshToken.and.returnValue(of(freshTokens));

    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([jwtInterceptorFn])),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: authSpy },
      ],
    });
    http = TestBed.inject(HttpClient);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('FE-006: a 500 from the *retried* request must NOT call logout(false)', () => {
    http.get('/api/database/db1/entry/999').subscribe({
      error: (err: HttpErrorResponse) => (capturedError = err),
    });

    const first = httpMock.expectOne('/api/database/db1/entry/999');
    expect(first.request.headers.get('Authorization')).toBe('Bearer stale-access');
    first.flush(null, { status: 401, statusText: 'Unauthorized' });

    expect(authSpy.refreshToken).toHaveBeenCalledTimes(1);

    const retried = httpMock.expectOne('/api/database/db1/entry/999');
    expect(retried.request.headers.get('Authorization')).toBe('Bearer fresh-access');
    retried.flush(null, { status: 500, statusText: 'Server Error' });

    expect(authSpy.logout).not.toHaveBeenCalled();
    expect(capturedError?.status).toBe(500);
  });

  it('FE-006: a 404 (or second 401) from the retried request must NOT call logout(false)', () => {
    http.get('/api/database/db1/entry/404').subscribe({
      error: (err: HttpErrorResponse) => (capturedError = err),
    });

    httpMock
      .expectOne('/api/database/db1/entry/404')
      .flush(null, { status: 401, statusText: 'Unauthorized' });

    const retried = httpMock.expectOne('/api/database/db1/entry/404');
    retried.flush(null, { status: 404, statusText: 'Not Found' });

    expect(authSpy.logout).not.toHaveBeenCalled();
    expect(capturedError?.status).toBe(404);
  });

  it('FE-006: a network error (status 0) from the retried request must NOT call logout(false)', () => {
    http.get('/api/database/db1/entry/0').subscribe({
      error: (err: HttpErrorResponse) => (capturedError = err),
    });

    httpMock
      .expectOne('/api/database/db1/entry/0')
      .flush(null, { status: 401, statusText: 'Unauthorized' });

    const retried = httpMock.expectOne('/api/database/db1/entry/0');
    retried.flush(null, { status: 0, statusText: 'Unknown Error' });

    expect(authSpy.logout).not.toHaveBeenCalled();
    expect(capturedError?.status).toBe(0);
  });

  it('FE-006: a failed *refresh* still logs out locally (session is genuinely dead)', () => {
    authSpy.refreshToken.and.returnValue(throwError(() => new Error('No refresh token available')));

    http.get('/api/database/db1/entry/1').subscribe({
      error: (err: HttpErrorResponse) => (capturedError = err),
    });

    httpMock
      .expectOne('/api/database/db1/entry/1')
      .flush(null, { status: 401, statusText: 'Unauthorized' });

    expect(authSpy.logout).toHaveBeenCalledTimes(1);
    expect(authSpy.logout).toHaveBeenCalledWith(false);
  });

  it('FE-007: a 401 on the logout request must NOT trigger refresh + retry', () => {
    http
      .post(
        '/api/logout',
        { refresh_token: 'stored-refresh' },
        {
          context: new HttpContext().set(SKIP_AUTH_REFRESH, true),
        },
      )
      .subscribe({
        error: (err: HttpErrorResponse) => (capturedError = err),
      });

    const req = httpMock.expectOne('/api/logout');
    expect(req.request.headers.get('Authorization')).toBe('Bearer stale-access');
    req.flush(null, { status: 401, statusText: 'Unauthorized' });

    // No refresh, no local logout, and — crucially — no retried logout call.
    expect(authSpy.refreshToken).not.toHaveBeenCalled();
    expect(authSpy.logout).not.toHaveBeenCalled();
    httpMock.expectNone('/api/logout');
    expect(capturedError?.status).toBe(401);
  });

  it('regression guard: PATCH /api/me (password change) 401 still skips the refresh flow', () => {
    http.patch('/api/me', { old_password: 'x', new_password: 'y' }).subscribe({
      error: (err: HttpErrorResponse) => (capturedError = err),
    });

    httpMock.expectOne('/api/me').flush(null, { status: 401, statusText: 'Unauthorized' });

    expect(authSpy.refreshToken).not.toHaveBeenCalled();
    expect(authSpy.logout).not.toHaveBeenCalled();
    expect(capturedError?.status).toBe(401);
  });
});
