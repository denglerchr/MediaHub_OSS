import { Injectable } from '@angular/core';
import { Permission } from '../models';
import { CanActivate, ActivatedRouteSnapshot, UrlTree, Router } from '@angular/router';
import { Observable } from 'rxjs';
import { map, take } from 'rxjs/operators';
import { AuthService } from '../services/auth.service';
import { NotificationService } from '../services/notification.service';

@Injectable({
  providedIn: 'root',
})
export class DatabaseAdminGuard implements CanActivate {
  constructor(
    private authService: AuthService,
    private router: Router,
    private notificationService: NotificationService,
  ) {}

  canActivate(route: ActivatedRouteSnapshot): Observable<boolean | UrlTree> | boolean | UrlTree {
    // Get the database ID from the URL path
    const dbId = route.paramMap.get('id');

    if (!dbId) {
      return this.router.createUrlTree(['/dashboard']);
    }

    return this.authService.ensureCurrentUser().pipe(
      take(1),
      map((user) => {
        if (!user) return this.router.createUrlTree(['/login']);

        // Admins can view everything
        if (user.is_admin) return true;

        // Check if the user has explicitly been granted CanAdmin for this specific database ID
        const hasAccess = user.permissions?.some(
          (p: Permission) => p.database_id === dbId && p.can_admin,
        );

        if (hasAccess) {
          return true;
        }

        this.notificationService.showError(
          `Access Denied: Administrator privileges are required for this database.`,
        );
        return this.router.createUrlTree(['/dashboard']);
      }),
    );
  }
}
