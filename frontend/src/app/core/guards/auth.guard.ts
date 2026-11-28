import { inject } from '@angular/core';
import { ActivatedRouteSnapshot, CanActivateFn, Router, UrlTree } from '@angular/router';
import { Observable, from, of } from 'rxjs';
import { catchError, map, switchMap, take } from 'rxjs/operators';
import { AppInfoService } from '../services/app-info.service';
import { AuthService } from '../services/auth.service';

export const authGuard: CanActivateFn = (
  route: ActivatedRouteSnapshot,
): Observable<boolean | UrlTree> => {
  const authService = inject(AuthService);
  const router = inject(Router);
  const appInfoService = inject(AppInfoService);

  // 1. Check for active user session first
  return authService.ensureCurrentUser().pipe(
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
        return of(router.createUrlTree(['/auth/callback'], { queryParams }));
      }

      // 3. If unauthenticated and no code, check if local login page is disabled
      return appInfoService.loadInfo().pipe(
        take(1),
        switchMap((info) => {
          const oidc = info?.oidc;
          if (
            oidc?.enabled &&
            oidc?.login_page_disabled &&
            oidc?.oidc_issuer_url &&
            oidc?.oidc_client_id
          ) {
            return from(authService.redirectToOidc(oidc)).pipe(
              map(() => false),
              catchError((err) => {
                console.error('OIDC redirect failed:', err);
                return of(
                  router.createUrlTree(['/login'], {
                    queryParams: { error: 'sso_failed' },
                  }),
                );
              }),
            );
          }

          return of(router.createUrlTree(['/login']));
        }),
      );
    }),
  );
};
