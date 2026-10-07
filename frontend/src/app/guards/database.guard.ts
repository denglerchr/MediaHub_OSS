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
export class DatabaseGuard implements CanActivate {
  constructor(
    private authService: AuthService,
    private router: Router,
    private notificationService: NotificationService,
  ) {}

  canActivate(route: ActivatedRouteSnapshot): Observable<boolean | UrlTree> | boolean | UrlTree {
    // Get the database ID from the URL path (e.g., /dashboard/db/:id)
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

        // FE-015: the documented RBAC model (AI/Concept/02_03_00_HTTP_Database.md:209)
        // grants access on ANY database role — CanView, CanCreate, CanEdit, CanDelete
        // or CanAdmin. Requiring CanView/CanAdmin here bounced `can_create`-only users
        // (e.g. upload-only accounts) straight back to the dashboard with "Access
        // Denied", even though POST /api/database/{id}/entry only requires CanCreate.
        // Individual page affordances are gated per permission (entry-list: entries
        // list requires can_view, the upload UI only can_create).
        const hasAccess = user.permissions?.some(
          (p: Permission) =>
            p.database_id === dbId &&
            (p.can_view || p.can_create || p.can_edit || p.can_delete || p.can_admin),
        );

        if (hasAccess) {
          return true;
        }

        this.notificationService.showError(
          `Access Denied: You do not have any permission for this database.`,
        );
        return this.router.createUrlTree(['/dashboard']);
      }),
    );
  }
}
