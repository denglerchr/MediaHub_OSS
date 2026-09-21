import { Component, OnInit } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';
import { AuthService } from '../../services/auth.service';
import { AppInfoService } from '../../services/app-info.service';
import { take } from 'rxjs/operators';

@Component({
  selector: 'app-auth-callback-page',
  templateUrl: './auth-callback-page.component.html',
  styleUrls: ['./auth-callback-page.component.css'],
  standalone: false,
})
export class AuthCallbackPageComponent implements OnInit {
  constructor(
    private route: ActivatedRoute,
    private router: Router,
    private authService: AuthService,
    private appInfoService: AppInfoService
  ) {}

  ngOnInit(): void {
    const queryParams = this.route.snapshot.queryParamMap;
    const error = queryParams.get('error');
    const errorDescription = queryParams.get('error_description');
    const code = queryParams.get('code');

    if (error) {
      console.error('OIDC provider returned error:', error, errorDescription);
      console.warn(
        `[MediaHub SSO] Tip for administrators: Ensure "${window.location.origin}/auth/callback" is registered as a Valid Redirect URI in your Identity Provider (e.g. Keycloak).`
      );
      this.router.navigate(['/login'], {
        queryParams: { error: 'sso_failed' },
        replaceUrl: true,
      });
      return;
    }

    if (!code) {
      this.router.navigate(['/login'], { replaceUrl: true });
      return;
    }

    this.appInfoService
      .loadInfo()
      .pipe(take(1))
      .subscribe({
        next: (info) => {
          const redirectUri =
            info?.oidc?.oidc_redirect_url || `${window.location.origin}/auth/callback`;

          this.authService.oidcLogin(code, redirectUri).subscribe({
            next: () => {
              this.router.navigate(['/dashboard'], { replaceUrl: true });
            },
            error: (err) => {
              console.error('OIDC token exchange failed:', err);
              console.warn(
                `[MediaHub SSO] Tip for administrators: Ensure "${redirectUri}" matches the configured Redirect URI in your Identity Provider (e.g. Keycloak) and the token endpoint is reachable.`
              );
              this.router.navigate(['/login'], {
                queryParams: { error: 'sso_failed' },
                replaceUrl: true,
              });
            },
          });
        },
        error: (err) => {
          console.error('Failed to load app info during auth callback:', err);
          this.router.navigate(['/login'], {
            queryParams: { error: 'sso_failed' },
            replaceUrl: true,
          });
        },
      });
  }
}
