import { inject } from '@angular/core';
import { CanActivateFn, Router, UrlTree } from '@angular/router';
import { Observable } from 'rxjs';
import { map, take } from 'rxjs/operators';
import { User } from '../models';
import { AuthService } from '../services/auth.service';
import { NotificationService } from '../services/notification.service';

/**
 * Shared route-permission helper for functional guards. Ensures the current user
 * is loaded (redirecting to /login if unauthenticated) and enforces `predicate`,
 * notifying and redirecting to /dashboard on denial.
 */
export function checkUserAccess(
  predicate: (user: User) => boolean,
  deniedMessage: string,
): Observable<boolean | UrlTree> {
  const authService = inject(AuthService);
  const router = inject(Router);
  const notificationService = inject(NotificationService);

  return authService.ensureCurrentUser().pipe(
    take(1),
    map((user) => {
      if (!user) {
        return router.createUrlTree(['/login']);
      }

      if (predicate(user)) {
        return true;
      }

      notificationService.showError(deniedMessage);
      return router.createUrlTree(['/dashboard']);
    }),
  );
}

export const adminGuard: CanActivateFn = () =>
  checkUserAccess(
    (user) => Boolean(user.is_admin),
    'Access Denied: Administrator privileges are required.',
  );
