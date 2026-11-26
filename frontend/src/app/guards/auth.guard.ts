import { Injectable } from '@angular/core';
import {
  CanActivate,
  UrlTree,
  Router,
  ActivatedRouteSnapshot,
  RouterStateSnapshot,
} from '@angular/router';
import { Observable, of, from } from 'rxjs';
import { map, take, switchMap, catchError } from 'rxjs/operators';
import { AuthService } from '../services/auth.service';
import { AppInfoService } from '../services/app-info.service';

@Injectable({
  providedIn: 'root',
})
export class AuthGuard implements CanActivate {
  constructor(
    private authService: AuthService,
    private router: Router,
    private appInfoService: AppInfoService,
  ) {}

  canActivate(
    route: ActivatedRouteSnapshot,
    state: RouterStateSnapshot,
  ): Observable<boolean | UrlTree> | boolean | UrlTree {
    // 1. Check for active user session first
    return this.authService.ensureCurrentUser().pipe(
      take(1),
      switchMap((user) => {
        if (user) {
          return of(true);
        }

        // 2. If unauthenticated, check if an OIDC authorization code or error was passed in the target route params
        const code = route.queryParamMap.get('code');
        const error = route.queryParamMap.get('error');
        if (code || error) {
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
          switchMap((info) => {
            const oidc = info?.oidc;
            if (
              oidc?.enabled &&
              oidc?.login_page_disabled &&
              oidc?.oidc_issuer_url &&
              oidc?.oidc_client_id
            ) {
              return from(this.authService.redirectToOidc(oidc)).pipe(
                map(() => false),
                catchError((err) => {
                  console.error('OIDC redirect failed:', err);
                  return of(
                    this.router.createUrlTree(['/login'], {
                      queryParams: { error: 'sso_failed' },
                    }),
                  );
                }),
              );
            }

            return of(this.router.createUrlTree(['/login']));
          }),
        );
      }),
    );
  }
}
