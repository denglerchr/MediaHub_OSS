// frontend/src/app/services/auth.service.ts

import { Injectable } from '@angular/core';
import { HttpClient, HttpHeaders, HttpErrorResponse, HttpContext } from '@angular/common/http';
import { BehaviorSubject, Observable, of, throwError } from 'rxjs';
import { catchError, map, switchMap, tap, finalize, shareReplay } from 'rxjs/operators';
import { User, TokenResponse, ApiKey, UserPayload, ApiKeyPayload } from '../models';
import { NotificationService } from './notification.service';
import { Router } from '@angular/router';
import {
  generateRandomState,
  generateCodeVerifier,
  generateCodeChallenge,
} from '../utils/pkce.utils';
import { SKIP_AUTH_REFRESH } from '../interceptors/http-context.tokens';

/**
 * Manages user authentication using JWT (JSON Web Tokens).
 * Handles Login (Token Fetch), Logout (Token Revocation), and Token Refresh.
 */
@Injectable({
  providedIn: 'root',
})
export class AuthService {
  private readonly apiUrl = '/api';
  private readonly ACCESS_TOKEN_KEY = 'access_token';
  private readonly REFRESH_TOKEN_KEY = 'refresh_token';

  // FE-008: the access token lives in memory only (never persisted), the refresh
  // token lives in sessionStorage (per-tab, wiped when the tab closes).
  private accessToken: string | null = null;

  // FE-009: single-flight refresh shared by every 401 handler in this tab.
  private refreshInFlight$: Observable<TokenResponse> | null = null;

  private currentUserSubject = new BehaviorSubject<User | null>(null);
  public currentUser$ = this.currentUserSubject.asObservable();

  constructor(
    private http: HttpClient,
    private router: Router,
    // FE-046: shared error reporting (see handleError below).
    private notificationService: NotificationService,
  ) {
    this.migrateLegacyStoredTokens();
  }

  /**
   * FE-008: tokens used to be kept in localStorage. Move a legacy refresh token into
   * sessionStorage so existing sessions survive the upgrade, and scrub the old keys so
   * the token no longer sits in persistent storage shared across tabs and restarts.
   */
  private migrateLegacyStoredTokens(): void {
    try {
      const legacyRefreshToken = localStorage.getItem(this.REFRESH_TOKEN_KEY);
      if (legacyRefreshToken && !sessionStorage.getItem(this.REFRESH_TOKEN_KEY)) {
        sessionStorage.setItem(this.REFRESH_TOKEN_KEY, legacyRefreshToken);
      }
    } catch {
      // Storage can be unavailable (private mode); nothing to migrate then.
    }
    localStorage.removeItem(this.ACCESS_TOKEN_KEY);
    localStorage.removeItem(this.REFRESH_TOKEN_KEY);
  }

  /**
   * Logs the user in by exchanging credentials for JWT tokens,
   * then fetching the user's profile.
   */
  basicAuthLogin(username: string, password: string): Observable<User> {
    // 1. Prepare Basic Auth header for the token endpoint (Unicode-safe base64 encoding)
    const bytes = new TextEncoder().encode(`${username}:${password}`);
    const binString = Array.from(bytes, (b) => String.fromCharCode(b)).join('');
    const basicAuth = 'Basic ' + btoa(binString);
    const headers = new HttpHeaders({ Authorization: basicAuth });

    // 2. Call POST /api/token to get the tokens
    return this.http.post<TokenResponse>(`${this.apiUrl}/token`, {}, { headers }).pipe(
      tap((tokens) => {
        this.storeTokens(tokens);
      }),
      // 3. Once we have tokens, fetch the user profile (Interceptor will inject Bearer token)
      switchMap(() => this.fetchCurrentUser()),
      map((user) => {
        if (!user) throw new Error('Failed to fetch user details after login');
        return user;
      }),
      catchError((err: HttpErrorResponse) => {
        // FE-018: clean up the partial session *without navigating* — a failed local
        // login must not drop the ?local=1 escape hatch from the URL.
        this.clearTokens();
        this.currentUserSubject.next(null);
        return throwError(() => err);
      }),
    );
  }

  /**
   * Logs the user in via OIDC code exchange (POST /api/token/oidc).
   */
  oidcLogin(code: string, redirectUri?: string, codeVerifier?: string): Observable<User> {
    const payload: { code: string; redirect_uri?: string; code_verifier?: string } = {
      code,
      redirect_uri: redirectUri || `${window.location.origin}/auth/callback`,
    };
    if (codeVerifier) {
      payload.code_verifier = codeVerifier;
    }

    return this.http.post<TokenResponse>(`${this.apiUrl}/token/oidc`, payload).pipe(
      tap((tokens) => {
        this.storeTokens(tokens);
      }),
      switchMap(() => this.fetchCurrentUser()),
      map((user) => {
        if (!user) throw new Error('Failed to fetch user details after OIDC login');
        return user;
      }),
      catchError((err: HttpErrorResponse) => {
        this.clearTokens();
        this.currentUserSubject.next(null);
        return throwError(() => err);
      }),
    );
  }

  /**
   * Generates PKCE challenge and CSRF state, stores them in sessionStorage,
   * and navigates to the OIDC authorization endpoint.
   */
  async redirectToOidc(oidcConfig: {
    oidc_issuer_url?: string;
    oidc_client_id?: string;
    oidc_redirect_url?: string;
    oidc_auth_endpoint?: string;
  }): Promise<void> {
    if (!oidcConfig.oidc_issuer_url || !oidcConfig.oidc_client_id) {
      throw new Error('OIDC configuration is missing issuer URL or client ID');
    }

    const authEndpoint =
      oidcConfig.oidc_auth_endpoint ||
      `${oidcConfig.oidc_issuer_url.replace(/\/+$/, '')}/protocol/openid-connect/auth`;

    const state = generateRandomState();
    sessionStorage.setItem('oidc_state', state);

    const codeVerifier = generateCodeVerifier();
    sessionStorage.setItem('oidc_code_verifier', codeVerifier);

    const codeChallenge = await generateCodeChallenge(codeVerifier);

    const clientId = encodeURIComponent(oidcConfig.oidc_client_id);
    const redirectUri = encodeURIComponent(
      oidcConfig.oidc_redirect_url || `${window.location.origin}/auth/callback`,
    );

    const queryParams = [
      `client_id=${clientId}`,
      `redirect_uri=${redirectUri}`,
      `response_type=code`,
      `scope=${encodeURIComponent('openid profile email')}`,
      `state=${encodeURIComponent(state)}`,
      `code_challenge=${encodeURIComponent(codeChallenge)}`,
      `code_challenge_method=S256`,
    ].join('&');

    window.location.href = `${authEndpoint}?${queryParams}`;
  }

  /**
   * Logs the user out.
   * Attempts to revoke the refresh token on the server, then clears local state.
   * @param notifyServer Whether to call the API to revoke the token (default: true)
   */
  logout(notifyServer: boolean = true): void {
    if (notifyServer && this.getRefreshToken()) {
      // FE-007: POST /api/logout sits behind AuthMiddleware (router.go:33), so an
      // expired/absent access token 401s the request *before* the Logout handler
      // ever deletes the refresh token. Establish a valid access token first
      // (silent refresh — the common case after idle), then revoke with the
      // refresh token stored *after* that refresh: rotation (FE-009) means the
      // body must never be captured before the refresh settles.
      this.prepareAccessTokenForLogout()
        .pipe(
          switchMap(() => {
            const refreshToken = this.getRefreshToken();
            if (!refreshToken) {
              // The silent refresh already proved the session dead (401) and cleared
              // the tokens — there is nothing left to revoke.
              return of(null);
            }
            // Mark the request so the interceptor never refreshes + retries it (which
            // would send a stale token in the body and leave the freshly issued one
            // valid server-side).
            return this.http.post(
              `${this.apiUrl}/logout`,
              { refresh_token: refreshToken },
              { context: new HttpContext().set(SKIP_AUTH_REFRESH, true) },
            );
          }),
          catchError((err) => {
            console.warn('Logout failed on server (token might be expired)', err);
            return of(null);
          }),
          // Local cleanup must run no matter how the server-side revocation went.
          finalize(() => this.clearSessionAndRedirect()),
        )
        .subscribe();
    } else {
      this.clearSessionAndRedirect();
    }
  }

  /**
   * FE-007/N4: establishes a valid access token before the revocation request and
   * lets any in-flight refresh (FE-009 single-flight) settle first so the logout
   * body carries the post-rotation refresh token. Never errors — the local cleanup
   * in `logout()` runs regardless of the outcome.
   */
  private prepareAccessTokenForLogout(): Observable<void> {
    const inFlight = this.refreshInFlight$;
    const settled: Observable<void> = inFlight
      ? inFlight.pipe(
          map(() => undefined),
          catchError(() => of(undefined)),
        )
      : of(undefined);
    return settled.pipe(switchMap(() => this.ensureAccessToken()));
  }

  private clearSessionAndRedirect(): void {
    this.clearTokens();
    this.currentUserSubject.next(null);
    // FE-018: keep the ?local=1 escape hatch alive when bouncing to the login page.
    const currentParams = this.router.routerState.snapshot.root.queryParams || {};
    const queryParams =
      currentParams['local'] !== undefined ? { local: currentParams['local'] } : {};
    this.router.navigate(['/login'], { queryParams });
  }

  /**
   * Refreshes the access token using the refresh token.
   * This is typically called by the JwtInterceptor.
   *
   * FE-009: concurrent callers share one in-flight refresh (single-flight), and a
   * 401 is retried once with the currently stored token before the session is
   * declared dead — rotation elsewhere (e.g. a duplicated tab) invalidates the token
   * we attempted with while a fresh one may already be stored.
   */
  refreshToken(): Observable<TokenResponse> {
    if (this.refreshInFlight$) {
      return this.refreshInFlight$;
    }

    const tokenAtAttempt = this.getRefreshToken();
    if (!tokenAtAttempt) {
      return throwError(() => new Error('No refresh token available'));
    }

    this.refreshInFlight$ = this.requestTokenRefresh(tokenAtAttempt).pipe(
      catchError((err: HttpErrorResponse) => {
        const latestToken = this.getRefreshToken();
        if (
          err instanceof HttpErrorResponse &&
          err.status === 401 &&
          latestToken &&
          latestToken !== tokenAtAttempt
        ) {
          return this.requestTokenRefresh(latestToken);
        }
        return throwError(() => err);
      }),
      finalize(() => {
        this.refreshInFlight$ = null;
      }),
      shareReplay(1),
    );

    return this.refreshInFlight$;
  }

  private requestTokenRefresh(refreshToken: string): Observable<TokenResponse> {
    return this.http
      .post<TokenResponse>(`${this.apiUrl}/token/refresh`, {
        refresh_token: refreshToken,
      })
      .pipe(
        tap((tokens) => {
          this.storeTokens(tokens);
        }),
      );
  }

  /**
   * Fetches the current user's data using the stored access token.
   */
  public fetchCurrentUser(): Observable<User | null> {
    return this.ensureAccessToken().pipe(
      switchMap(() => {
        if (!this.getAccessToken()) {
          return of(null);
        }

        return this.http.get<User>(`${this.apiUrl}/me`).pipe(
          tap((user) => {
            this.currentUserSubject.next(user);
          }),
          catchError((err: HttpErrorResponse) => {
            // FE-010: only an actual 401 invalidates the session. Transient failures
            // (status 0, 5xx) must keep the session alive and just report "no user".
            if (err?.status === 401) {
              this.clearSessionAndRedirect();
            }
            return of(null);
          }),
        );
      }),
    );
  }

  /**
   * FE-008: the access token is kept in memory only, so a reload leaves us without one
   * while the refresh token survives in sessionStorage. Silently refresh before
   * concluding the session is gone. A transient refresh failure keeps the refresh
   * token; only a 401 drops it.
   */
  private ensureAccessToken(): Observable<void> {
    if ((this.getAccessToken() && !this.isAccessTokenExpired()) || !this.getRefreshToken()) {
      return of(undefined);
    }
    return this.forceAccessTokenRefresh();
  }

  /**
   * FE-011: unconditional silent access-token refresh. Media elements fail with
   * 401 Range responses that never reach the interceptor, so a media-URL
   * regeneration after such a failure must not trust the stored token — nothing
   * has refreshed it in the meantime. Never errors: a failed refresh keeps the
   * current token in place (only a 401 drops the session) so the caller can still
   * regenerate and compare its URL.
   */
  public forceAccessTokenRefresh(): Observable<void> {
    return this.refreshToken().pipe(
      map(() => undefined),
      catchError((err: HttpErrorResponse) => {
        if (err?.status === 401) {
          this.clearTokens();
          this.currentUserSubject.next(null);
        }
        return of(undefined);
      }),
    );
  }

  /**
   * FE-007: the access token is a JWT with an `exp` claim (tokenhandler/utils.go).
   * A token that is present but expired is useless for authenticated requests
   * (AuthMiddleware rejects it), so `ensureAccessToken()` treats it like a missing
   * one and silently refreshes. Tokens that are not JWTs or carry no readable
   * `exp` are assumed valid (fail-open, same as before this check existed).
   */
  private isAccessTokenExpired(): boolean {
    const token = this.accessToken;
    if (!token) {
      return true;
    }
    const parts = token.split('.');
    if (parts.length !== 3) {
      return false;
    }
    try {
      const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')));
      if (typeof payload.exp !== 'number') {
        return false;
      }
      // Small skew so the token is refreshed before the server starts rejecting it.
      return payload.exp * 1000 <= Date.now() + 5000;
    } catch {
      return false;
    }
  }

  /**
   * Ensures that the current user profile is loaded.
   * If already present in memory, returns it immediately.
   * Otherwise attempts to restore the session (silent refresh on reload) and fetch /me.
   */
  public ensureCurrentUser(): Observable<User | null> {
    const user = this.getCurrentUser();
    if (user) {
      return of(user);
    }
    return this.fetchCurrentUser();
  }

  // --- Token Management Helpers ---

  private storeTokens(tokens: TokenResponse): void {
    // FE-008: access token in memory only, refresh token in per-tab sessionStorage.
    this.accessToken = tokens.access_token;
    sessionStorage.setItem(this.REFRESH_TOKEN_KEY, tokens.refresh_token);
  }

  private clearTokens(): void {
    this.accessToken = null;
    sessionStorage.removeItem(this.REFRESH_TOKEN_KEY);
  }

  public getAccessToken(): string | null {
    return this.accessToken;
  }

  public getRefreshToken(): string | null {
    return sessionStorage.getItem(this.REFRESH_TOKEN_KEY);
  }

  public getCurrentUser(): User | null {
    return this.currentUserSubject.value;
  }

  // --- User Management Methods ---

  /**
   * Checks if the current user has a specific permission for a given database.
   * Global admins automatically return true for all checks.
   * @param databaseId The ULID of the database to check access for.
   * @param permission The specific permission to check (e.g., 'can_view', 'can_create').
   */
  public hasDatabasePermission(
    databaseId: string,
    permission: keyof import('../models').Permission,
  ): boolean {
    const user = this.getCurrentUser();
    if (!user) return false;

    // Global admins bypass all permission checks
    if (user.is_admin) return true;

    // Find the specific permission record for this database using the ULID
    const dbPerms = user.permissions?.find((p) => p.database_id === databaseId);

    if (!dbPerms) return false;

    // Return the requested permission flag
    return !!dbPerms[permission];
  }

  changeOwnPassword(oldPassword: string, newPassword: string): Observable<unknown> {
    const payload = {
      old_password: oldPassword,
      new_password: newPassword,
    };
    // FE-046: deliberately *not* routed through handleError — the caller maps the
    // outcome to password-specific messages (401 → "Incorrect current password.").
    return this.http.patch(`${this.apiUrl}/me`, payload);
  }

  /**
   * FE-046: shared error handler (mirrors DatabaseService/AuditService) — surfaces
   * the server's actual reason (e.g. the 409 "cannot remove the last admin") instead
   * of letting callers swallow it into console.error. Exactly one toast per failure.
   */
  public handleError(error: HttpErrorResponse): Observable<never> {
    let errorMessage = 'An unknown error occurred.';
    if (error.error && typeof error.error.error === 'string') {
      errorMessage = error.error.error;
    } else if (error.status === 0) {
      errorMessage = 'Network error: Server is unreachable.';
    } else if (error.status === 401) {
      errorMessage = 'Unauthorized: Please log in again.';
    } else if (error.status === 403) {
      errorMessage = 'Forbidden: You lack permission for this operation.';
    } else if (error.status === 404) {
      errorMessage = 'Not Found: The requested user resource does not exist.';
    } else if (error.status >= 500) {
      errorMessage = `Server Error (${error.status}): ${error.statusText || 'Internal Error'}`;
    } else if (error.message) {
      errorMessage = error.message;
    }

    this.notificationService.showError(errorMessage);
    return throwError(() => new Error(errorMessage));
  }

  // FE-042: the user/key management payloads are typed (`UserPayload`,
  // `ApiKeyPayload`); only the *mutating* methods route through handleError so a
  // failure shows exactly one precise toast (FE-046).

  getUsers(accountType?: string): Observable<User[]> {
    let url = `${this.apiUrl}/users`;
    if (accountType) {
      url += `?account_type=${accountType}`;
    }
    return this.http.get<User[]>(url);
  }

  getUser(userId: string): Observable<User> {
    return this.http.get<User>(`${this.apiUrl}/user/${userId}`);
  }

  createUser(userData: UserPayload): Observable<User> {
    return this.http
      .post<User>(`${this.apiUrl}/user`, userData)
      .pipe(catchError((err: HttpErrorResponse) => this.handleError(err)));
  }

  updateUser(userId: string, updates: UserPayload): Observable<User> {
    return this.http
      .patch<User>(`${this.apiUrl}/user/${userId}`, updates)
      .pipe(catchError((err: HttpErrorResponse) => this.handleError(err)));
  }

  deleteUser(userId: string): Observable<{ message: string }> {
    return this.http
      .delete<{ message: string }>(`${this.apiUrl}/user/${userId}`)
      .pipe(catchError((err: HttpErrorResponse) => this.handleError(err)));
  }

  // --- API Key Management Methods ---

  getGlobalKeys(): Observable<ApiKey[]> {
    return this.http.get<ApiKey[]>(`${this.apiUrl}/users/keys`);
  }

  getUserKeys(userId: string): Observable<ApiKey[]> {
    return this.http.get<ApiKey[]>(`${this.apiUrl}/user/${userId}/keys`);
  }

  createUserKey(userId: string, data: ApiKeyPayload): Observable<ApiKey> {
    return this.http
      .post<ApiKey>(`${this.apiUrl}/user/${userId}/keys`, data)
      .pipe(catchError((err: HttpErrorResponse) => this.handleError(err)));
  }

  updateUserKey(userId: string, keyId: string, data: ApiKeyPayload): Observable<ApiKey> {
    return this.http
      .patch<ApiKey>(`${this.apiUrl}/user/${userId}/keys/${keyId}`, data)
      .pipe(catchError((err: HttpErrorResponse) => this.handleError(err)));
  }

  deleteUserKey(userId: string, keyId: string): Observable<unknown> {
    return this.http
      .delete(`${this.apiUrl}/user/${userId}/keys/${keyId}`)
      .pipe(catchError((err: HttpErrorResponse) => this.handleError(err)));
  }
}
