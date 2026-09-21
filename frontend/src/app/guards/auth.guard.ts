import { Injectable } from '@angular/core';
import { CanActivate, UrlTree, Router, ActivatedRouteSnapshot, RouterStateSnapshot } from '@angular/router';
import { Observable, of } from 'rxjs';
import { map, take, switchMap } from 'rxjs/operators';
import { AuthService } from '../services/auth.service';
import { AppInfoService } from '../services/app-info.service';

@Injectable({
  providedIn: 'root',
})
export class AuthGuard implements CanActivate {
  constructor(
    private authService: AuthService,
    private router: Router,
    private appInfoService: AppInfoService
  ) {}

  canActivate(
    route: ActivatedRouteSnapshot,
    state: RouterStateSnapshot
  ): Observable<boolean | UrlTree> | boolean | UrlTree {
    // 1. Check for active user session first
    return this.authService.ensureCurrentUser().pipe(
      take(1),
      switchMap((user) => {
        if (user) {
          return of(true);
        }

        // 2. If unauthenticated, check if an authorization code was passed in the target route params
        const code = route.queryParamMap.get('code');
        if (code) {
          const queryParams: Record<string, string> = {};
          route.queryParamMap.keys.forEach((key) => {
            const val = route.queryParamMap.get(key);
            if (val !== null) {
              queryParams[key] = val;
            }
          });
          return of(this.router.createUrlTree(['/auth/callback'], { queryParams }));
        }

        // 3. If unauthenticated and no code, check if local login page is disabled
        return this.appInfoService.loadInfo().pipe(
          take(1),
          map((info) => {
            const oidc = info?.oidc;
            if (
              oidc?.enabled &&
              oidc?.login_page_disabled &&
              oidc?.oidc_issuer_url &&
              oidc?.oidc_client_id
            ) {
              const authEndpoint = `${oidc.oidc_issuer_url}/protocol/openid-connect/auth`;
              const clientId = encodeURIComponent(oidc.oidc_client_id);
              const redirectUri = encodeURIComponent(
                oidc.oidc_redirect_url || `${window.location.origin}/auth/callback`
              );
              window.location.href = `${authEndpoint}?client_id=${clientId}&redirect_uri=${redirectUri}&response_type=code&scope=openid`;
              return false;
            }

            return this.router.createUrlTree(['/login']);
          })
        );
      })
    );
  }
}