import { inject } from '@angular/core';
import {
  HttpRequest,
  HttpHandlerFn,
  HttpEvent,
  HttpErrorResponse,
  HttpInterceptorFn,
} from '@angular/common/http';
import { Observable, throwError } from 'rxjs';
import { catchError, switchMap } from 'rxjs/operators';
import { AuthService } from '../services/auth.service';
import { TokenResponse } from '../models';
import { SKIP_AUTH_REFRESH } from './http-context.tokens';

/**
 * Step 8 (tooling leftovers): functional interceptor registered via
 * `provideHttpClient(withInterceptors([jwtInterceptorFn]))` — the class-based
 * `HTTP_INTERCEPTORS` token / `HttpClientModule` are deprecated on Angular 20.
 * The behaviour is unchanged from the former `JwtInterceptor` class and is guarded
 * by the FE-006/FE-007 specs in jwt.interceptor.spec.ts.
 */
export const jwtInterceptorFn: HttpInterceptorFn = (
  request: HttpRequest<unknown>,
  next: HttpHandlerFn,
): Observable<HttpEvent<unknown>> => {
  const authService = inject(AuthService);
  let authReq = request;
  const token = authService.getAccessToken();

  // UPDATED: Check for 'api/token' without the leading slash to match our relative paths!
  // This ensures both 'api/token' and 'api/token/refresh' are skipped
  if (request.url.includes('api/token')) {
    return next(request);
  }

  // 1. Add Bearer token if available
  if (token) {
    authReq = addTokenHeader(request, token);
  }

  // 2. Handle the request and catch 401s
  return next(authReq).pipe(
    catchError((error) => {
      if (error instanceof HttpErrorResponse && error.status === 401) {
        // FE-007: requests marked with SKIP_AUTH_REFRESH (logout) must fail as-is.
        // Refreshing + retrying them would revoke the wrong (already rotated) token.
        if (request.context.get(SKIP_AUTH_REFRESH)) {
          return throwError(() => error);
        }
        // Skip token refresh for password update requests to avoid logouts on incorrect credentials
        if (request.url.includes('api/me') && request.method === 'PATCH') {
          return throwError(() => error);
        }
        // If 401, try to refresh the token
        return handle401Error(authService, authReq, next);
      }
      return throwError(() => error);
    }),
  );
};

function handle401Error(
  authService: AuthService,
  request: HttpRequest<unknown>,
  next: HttpHandlerFn,
): Observable<HttpEvent<unknown>> {
  // The refresh itself is single-flighted inside AuthService (FE-009) and retries
  // once with the currently stored token if a concurrent rotation invalidated ours.
  return authService.refreshToken().pipe(
    // FE-006: this catchError must sit on the *refresh* stream only. Only a failed
    // refresh means the session is dead; an error from the retried request below
    // must reach the caller untouched instead of logging the user out.
    catchError((err) => {
      authService.logout(false); // Logout locally without hitting API (since tokens are dead)
      return throwError(() => err);
    }),
    switchMap((tokenResponse: TokenResponse) => {
      return next(addTokenHeader(request, tokenResponse.access_token));
    }),
  );
}

function addTokenHeader(request: HttpRequest<unknown>, token: string): HttpRequest<unknown> {
  return request.clone({
    setHeaders: {
      Authorization: `Bearer ${token}`,
    },
  });
}
