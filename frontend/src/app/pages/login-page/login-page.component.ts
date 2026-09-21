import { Component, OnInit, OnDestroy } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { AuthService } from '../../services/auth.service';
import { NotificationService } from '../../services/notification.service';
import { AppInfoService } from '../../services/app-info.service';
import { AppInfo } from '../../models';
import { finalize, takeUntil } from 'rxjs/operators';
import { Subject } from 'rxjs';
import { HttpErrorResponse } from '@angular/common/http';

@Component({
  selector: 'app-login-page',
  templateUrl: './login-page.component.html',
  styleUrls: ['./login-page.component.css'],
  standalone: false
})
export class LoginPageComponent implements OnInit, OnDestroy {
  loginForm: FormGroup;
  isLoading = false;
  loginError: string | null = null;
  appInfo: AppInfo | null = null;
  
  private destroy$ = new Subject<void>();

  constructor(
    private fb: FormBuilder,
    private authService: AuthService,
    private router: Router,
    private route: ActivatedRoute,
    private notificationService: NotificationService,
    private appInfoService: AppInfoService
  ) {
    this.loginForm = this.fb.group({
      username: ['', Validators.required],
      password: ['', Validators.required],
    });
  }

  ngOnInit(): void {
    if (this.authService.getCurrentUser()) {
      this.router.navigate(['/dashboard']);
      return;
    }

    const code = this.route.snapshot.queryParamMap.get('code');
    if (code) {
      this.router.navigate(['/auth/callback'], {
        queryParams: this.route.snapshot.queryParams,
        replaceUrl: true,
      });
      return;
    }

    const errorParam = this.route.snapshot.queryParamMap.get('error');
    if (errorParam === 'sso_failed') {
      this.loginError = 'Single Sign-On authentication failed. Please try again.';
      console.warn(
        `[MediaHub SSO] Tip for administrators: Ensure "${window.location.origin}/auth/callback" is included in your Identity Provider's (e.g. Keycloak) Valid Redirect URIs.`
      );
    }

    // Load AppInfo to check OIDC settings
    this.appInfoService.loadInfo().pipe(takeUntil(this.destroy$)).subscribe(info => {
      this.appInfo = info;
    });
  }

  onSubmit(): void {
    if (this.loginForm.invalid) return;
    
    this.isLoading = true;
    this.loginError = null;
    this.notificationService.clearGlobalError();
    
    const { username, password } = this.loginForm.value;
    
    this.authService.basicAuthLogin(username, password).pipe(
      finalize(() => this.isLoading = false)
    ).subscribe({
      next: () => this.router.navigate(['/dashboard']),
      error: (err: HttpErrorResponse) => {
        if (err.status === 401) {
          this.loginError = 'Invalid username or password.';
        } else if (err.status === 403) {
          this.loginError = 'Local login is disabled by the server configuration.';
        } else {
          this.loginError = 'A server error occurred. Please try again later.';
        }
      },
    });
  }

  redirectToOIDC(): void {
    const oidcConfig = this.appInfo?.oidc;

    if (!oidcConfig?.oidc_issuer_url || !oidcConfig?.oidc_client_id) {
      this.loginError = 'OIDC configuration is missing from the server.';
      return;
    }
    
    const authEndpoint = `${oidcConfig.oidc_issuer_url}/protocol/openid-connect/auth`;
    const clientId = encodeURIComponent(oidcConfig.oidc_client_id);
    const redirectUri = encodeURIComponent(
      oidcConfig.oidc_redirect_url || `${window.location.origin}/auth/callback`
    );
    
    const oidcUrl = `${authEndpoint}?client_id=${clientId}&redirect_uri=${redirectUri}&response_type=code&scope=openid`;
    
    window.location.href = oidcUrl;
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}