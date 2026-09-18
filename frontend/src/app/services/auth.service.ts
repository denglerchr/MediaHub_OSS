// frontend/src/app/services/auth.service.ts

import { Injectable } from '@angular/core';
import { HttpClient, HttpHeaders, HttpErrorResponse } from '@angular/common/http';
import { BehaviorSubject, Observable, of, throwError } from 'rxjs';
import { catchError, map, switchMap, tap, finalize } from 'rxjs/operators';
import { User, TokenResponse, ApiKey } from '../models';
import { Router } from '@angular/router';

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

  private currentUserSubject = new BehaviorSubject<User | null>(null);
  public currentUser$ = this.currentUserSubject.asObservable();

  // Helper to check if we have a user loaded
  public isAuthenticated$ = this.currentUser$.pipe(map((user) => !!user));

  constructor(private http: HttpClient, private router: Router) { }

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
      map(user => {
        if (!user) throw new Error('Failed to fetch user details after login');
        return user;
      }),
      catchError((err: HttpErrorResponse) => {
        this.logout(false); // Clean up if anything fails
        return throwError(() => err);
      })
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
      })
    );
  }


  /**
   * Logs the user out.
   * Attempts to revoke the refresh token on the server, then clears local state.
   * @param notifyServer Whether to call the API to revoke the token (default: true)
   */
  logout(notifyServer: boolean = true): void {
    const refreshToken = this.getRefreshToken();

    if (notifyServer && refreshToken) {
      this.http.post(`${this.apiUrl}/logout`, { refresh_token: refreshToken })
        .pipe(finalize(() => this.clearSessionAndRedirect()))
        .subscribe({
          next: () => console.log('Logout successful on server'),
          error: (err) => console.warn('Logout failed on server (token might be expired)', err)
        });
    } else {
      this.clearSessionAndRedirect();
    }
  }

  private clearSessionAndRedirect(): void {
    this.clearTokens();
    this.currentUserSubject.next(null);
    this.router.navigate(['/login']);
  }

  /**
   * Refreshes the access token using the refresh token.
   * This is typically called by the JwtInterceptor.
   */
  refreshToken(): Observable<TokenResponse> {
    const refreshToken = this.getRefreshToken();
    if (!refreshToken) {
      return throwError(() => new Error('No refresh token available'));
    }

    return this.http.post<TokenResponse>(`${this.apiUrl}/token/refresh`, {
      refresh_token: refreshToken
    }).pipe(
      tap((tokens) => {
        this.storeTokens(tokens);
      })
    );
  }

  /**
   * Fetches the current user's data using the stored access token.
   */
  public fetchCurrentUser(): Observable<User | null> {
    if (!this.getAccessToken()) {
      return of(null);
    }

    return this.http.get<User>(`${this.apiUrl}/me`).pipe(
      tap((user) => {
        this.currentUserSubject.next(user);
      }),
      catchError(() => {
        // If fetching user fails (e.g., 401 even after refresh attempts), log out locally
        this.clearSessionAndRedirect();
        return of(null);
      })
    );
  }

  /**
   * Ensures that the current user profile is loaded.
   * If already present in memory, returns it immediately.
   * If token exists, fetches the user from /me.
   * Otherwise returns null.
   */
  public ensureCurrentUser(): Observable<User | null> {
    const user = this.getCurrentUser();
    if (user) {
      return of(user);
    }
    if (!this.getAccessToken()) {
      return of(null);
    }
    return this.fetchCurrentUser();
  }

  // --- Token Management Helpers ---

  private storeTokens(tokens: TokenResponse): void {
    localStorage.setItem(this.ACCESS_TOKEN_KEY, tokens.access_token);
    localStorage.setItem(this.REFRESH_TOKEN_KEY, tokens.refresh_token);
  }

  private clearTokens(): void {
    localStorage.removeItem(this.ACCESS_TOKEN_KEY);
    localStorage.removeItem(this.REFRESH_TOKEN_KEY);
  }

  public getAccessToken(): string | null {
    return localStorage.getItem(this.ACCESS_TOKEN_KEY);
  }

  public getRefreshToken(): string | null {
    return localStorage.getItem(this.REFRESH_TOKEN_KEY);
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
  public hasDatabasePermission(databaseId: string, permission: keyof import('../models').Permission): boolean {
    const user = this.getCurrentUser();
    if (!user) return false;

    // Global admins bypass all permission checks
    if (user.is_admin) return true;

    // Find the specific permission record for this database using the ULID
    const dbPerms = user.permissions?.find(p => p.database_id === databaseId);

    if (!dbPerms) return false;

    // Return the requested permission flag
    return !!dbPerms[permission];
  }

  changeOwnPassword(oldPassword: string, newPassword: string): Observable<any> {
    const payload = {
      old_password: oldPassword,
      new_password: newPassword
    };
    return this.http.patch(`${this.apiUrl}/me`, payload);
  }

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

  createUser(userData: any): Observable<User> {
    return this.http.post<User>(`${this.apiUrl}/user`, userData);
  }

  updateUser(userId: string, updates: any): Observable<User> {
    return this.http.patch<User>(`${this.apiUrl}/user/${userId}`, updates);
  }

  deleteUser(userId: string): Observable<{ message: string }> {
    return this.http.delete<{ message: string }>(`${this.apiUrl}/user/${userId}`);
  }

  // --- API Key Management Methods ---

  getGlobalKeys(): Observable<ApiKey[]> {
    return this.http.get<ApiKey[]>(`${this.apiUrl}/users/keys`);
  }

  getUserKeys(userId: string): Observable<ApiKey[]> {
    return this.http.get<ApiKey[]>(`${this.apiUrl}/user/${userId}/keys`);
  }

  createUserKey(userId: string, data: any): Observable<ApiKey> {
    return this.http.post<ApiKey>(`${this.apiUrl}/user/${userId}/keys`, data);
  }

  updateUserKey(userId: string, keyId: string, data: any): Observable<ApiKey> {
    return this.http.patch<ApiKey>(`${this.apiUrl}/user/${userId}/keys/${keyId}`, data);
  }

  deleteUserKey(userId: string, keyId: string): Observable<any> {
    return this.http.delete(`${this.apiUrl}/user/${userId}/keys/${keyId}`);
  }
}