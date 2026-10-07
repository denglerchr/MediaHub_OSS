import { Injectable } from '@angular/core';
import { CanActivate, UrlTree, Router } from '@angular/router';
import { Observable } from 'rxjs';
import { map, take } from 'rxjs/operators';
import { AuthService } from '../services/auth.service';
import { NotificationService } from '../services/notification.service';

@Injectable({
  providedIn: 'root',
})
export class AdminGuard implements CanActivate {
  constructor(
    private authService: AuthService,
    private router: Router,
    private notificationService: NotificationService,
  ) {}

  canActivate(): Observable<boolean | UrlTree> | boolean | UrlTree {
    return this.authService.ensureCurrentUser().pipe(
      take(1),
      map((user) => {
        if (!user) {
          return this.router.createUrlTree(['/login']);
        }

        // Global Role: IsAdmin bypasses all permission checks
        if (user.is_admin) {
          return true;
        }

        // Show an error before redirecting
        this.notificationService.showError('Access Denied: Administrator privileges are required.');
        return this.router.createUrlTree(['/dashboard']);
      }),
    );
  }
}
