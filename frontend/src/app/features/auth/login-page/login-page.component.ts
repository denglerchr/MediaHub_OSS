import { Component, DestroyRef, OnInit, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormBuilder, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, Router } from '@angular/router';
import { AuthService } from '../../../core/services/auth.service';
import { NotificationService } from '../../../core/services/notification.service';
import { AppInfoService } from '../../../core/services/app-info.service';
import { AppInfo } from '../../../core/models';
import { throwError } from 'rxjs';
import { catchError, finalize, switchMap } from 'rxjs/operators';
import { extractHttpErrorMessage } from '../../../shared/utils/http-error';
import { AuthCardComponent } from '../../../shared/components/auth-card/auth-card.component';

@Component({
  selector: 'app-login-page',
  templateUrl: './login-page.component.html',
  styleUrls: ['./login-page.component.css'],
  standalone: true,
  imports: [CommonModule, ReactiveFormsModule, AuthCardComponent],
})
export class LoginPageComponent implements OnInit {
  loginForm: FormGroup;
  isLoading = false;
  loginError: string | null = null;
  appInfo: AppInfo | null = null;
  isLocalBypass = false;

  private destroyRef = inject(DestroyRef);

  constructor(
    private fb: FormBuilder,
    private authService: AuthService,
    private router: Router,
    private route: ActivatedRoute,
    private notificationService: NotificationService,
    private appInfoService: AppInfoService,
  ) {
    this.loginForm = this.fb.group({
      username: ['', Validators.required],
      password: ['', Validators.required],
    });
  }

  get showLoginForm(): boolean {
    return !this.appInfo?.oidc?.login_page_disabled || this.isLocalBypass;
  }

  ngOnInit(): void {
    if (this.authService.getCurrentUser()) {
      this.router.navigate(['/dashboard']);
      return;
    }

    this.route.queryParamMap.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((params) => {
      const local = params.get('local');
      this.isLocalBypass = local === '1' || local?.toLowerCase() === 'true';
    });

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
        `[MediaHub SSO] Tip for administrators: Ensure "${window.location.origin}/auth/callback" is included in your Identity Provider's (e.g. Keycloak) Valid Redirect URIs.`,
      );
    } else if (errorParam === 'sso_state_invalid') {
      this.loginError =
        'Single Sign-On session was invalid or expired (CSRF protection). Please try again.';
    }

    // Load AppInfo to check OIDC settings
    this.appInfoService
      .loadInfo()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((info) => {
        this.appInfo = info;
      });
  }

  onSubmit(): void {
    if (this.isLoading || this.loginForm.invalid) {
      return;
    }

    this.isLoading = true;
    this.loginError = null;
    this.notificationService.clearGlobalError();

    const { username, password } = this.loginForm.value;

    this.authService
      .basicAuthLogin(username, password)
      .pipe(
        catchError((err: unknown) => {
          if ((err as { status?: number } | null)?.status === 401) {
            return throwError(() => 'Invalid username or password.');
          }
          return extractHttpErrorMessage(err, {
            fallback: 'A server error occurred. Please try again later.',
          }).pipe(switchMap((msg) => throwError(() => msg)));
        }),
        finalize(() => (this.isLoading = false)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: () => this.router.navigate(['/dashboard']),
        error: (msg: unknown) => {
          this.loginError =
            typeof msg === 'string' ? msg : 'A server error occurred. Please try again later.';
        },
      });
  }

  async redirectToOIDC(): Promise<void> {
    const oidcConfig = this.appInfo?.oidc;

    if (!oidcConfig?.oidc_issuer_url || !oidcConfig?.oidc_client_id) {
      this.loginError = 'OIDC configuration is missing from the server.';
      return;
    }

    try {
      await this.authService.redirectToOidc(oidcConfig);
    } catch (err) {
      console.error('Failed to initiate OIDC redirect:', err);
      this.loginError = 'Failed to initiate Single Sign-On.';
    }
  }
}
