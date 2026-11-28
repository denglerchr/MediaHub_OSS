// frontend/src/app/pages/profile-page/profile-page.component.ts

import { CommonModule } from '@angular/common';
import {
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  DestroyRef,
  OnDestroy,
  OnInit,
  inject,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { EMPTY, Subject, combineLatest } from 'rxjs';
import { catchError, finalize, map, switchMap } from 'rxjs/operators';
import { ApiKeyTableComponent } from '../../admin/api-key-table/api-key-table.component';
import { PageHeaderComponent } from '../../../shared/components/page-header/page-header.component';
import { User, ApiKey } from '../../../core/models';
import { AccountTypeLabelPipe } from '../../../shared/pipes/account-type-label.pipe';
import { ApiKeyService } from '../../../core/services/api-key.service';
import { AuthService } from '../../../core/services/auth.service';
import { DatabaseService } from '../../../core/services/database.service';
import { NotificationService } from '../../../core/services/notification.service';
import { resolveDatabasePermissions } from '../../../shared/utils/permissions';
import { extractHttpErrorMessage } from '../../../shared/utils/http-error';

@Component({
  selector: 'app-profile-page',
  templateUrl: './profile-page.component.html',
  styleUrls: ['./profile-page.component.css'],
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    ReactiveFormsModule,
    PageHeaderComponent,
    ApiKeyTableComponent,
    AccountTypeLabelPipe,
  ],
})
export class ProfilePageComponent implements OnInit, OnDestroy {
  public user: User | null = null;
  public apiKeys: ApiKey[] = [];
  public databasePermissionsMapped: {
    name: string;
    view: boolean;
    create: boolean;
    edit: boolean;
    delete: boolean;
    admin: boolean;
  }[] = [];

  /** FE-028: stable trackBy keys (was an index-based DOM churn on every re-render). */
  public trackByPermissionName(index: number, p: { name: string }): string {
    return p.name;
  }

  public passwordForm: FormGroup;
  public isPasswordLoading = false;
  public isKeysLoading = false;

  private readonly destroyRef = inject(DestroyRef);
  private loadKeysTrigger$ = new Subject<string>();

  constructor(
    private authService: AuthService,
    private apiKeyService: ApiKeyService,
    private databaseService: DatabaseService,
    private notificationService: NotificationService,
    private fb: FormBuilder,
    private cdr: ChangeDetectorRef,
  ) {
    this.passwordForm = this.fb.group(
      {
        oldPassword: ['', [Validators.required]],
        newPassword: ['', [Validators.required, Validators.minLength(8)]],
        confirmPassword: ['', [Validators.required]],
      },
      {
        validators: this.passwordMatchValidator,
      },
    );
  }

  ngOnInit(): void {
    // 1. Drive API key loading through a switchMap pipeline so overlapping refreshes
    // cancel any prior in-flight request.
    this.loadKeysTrigger$
      .pipe(
        switchMap((userId) => {
          this.isKeysLoading = true;
          this.cdr.markForCheck();

          return this.apiKeyService.getUserKeys(userId).pipe(
            map((keys) => ({ userId, keys: keys || [] })),
            catchError((err) => {
              console.error('Failed to load user API keys', err);
              this.notificationService.showError('Could not load API keys.');
              this.cdr.markForCheck();
              return EMPTY;
            }),
            finalize(() => {
              this.isKeysLoading = false;
              this.cdr.markForCheck();
            }),
          );
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe(({ userId, keys }) => {
        if (this.user?.id === userId) {
          this.apiKeys = keys;
          this.cdr.markForCheck();
        }
      });

    // 2. Listen to current user
    this.authService.currentUser$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((user) => {
      const prevUserId = this.user?.id ?? null;
      this.user = user;
      if (!user) {
        this.apiKeys = [];
        this.isKeysLoading = false;
      } else if (user.id !== prevUserId) {
        this.loadKeysTrigger$.next(user.id);
      }
      this.cdr.markForCheck();
    });

    // 3. Fetch databases
    this.databaseService
      .loadDatabases()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        error: (err) => console.error('Failed to load databases on profile page', err),
      });

    // 4. Reactively map database permissions
    this.mapPermissions();
  }

  ngOnDestroy(): void {
    this.loadKeysTrigger$.complete();
  }

  private passwordMatchValidator(g: FormGroup) {
    const newPass = g.get('newPassword')?.value;
    const confirmPass = g.get('confirmPassword')?.value;
    return newPass === confirmPass ? null : { mismatch: true };
  }

  private loadApiKeys(): void {
    if (!this.user) return;
    this.loadKeysTrigger$.next(this.user.id);
  }

  private mapPermissions(): void {
    // Fetch database list and align with user's permissions array
    combineLatest([this.databaseService.databases$, this.authService.currentUser$])
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(([dbs, currentUser]) => {
        if (!currentUser) {
          this.databasePermissionsMapped = [];
          this.cdr.markForCheck();
          return;
        }

        this.databasePermissionsMapped = dbs
          .map((db) => {
            const resolved = resolveDatabasePermissions(currentUser, db.id);
            return {
              name: db.name,
              view: resolved.canView,
              create: resolved.canCreate,
              edit: resolved.canEdit,
              delete: resolved.canDelete,
              admin: resolved.canAdmin,
            };
          })
          // only display databases where they have at least one permission
          .filter((p) => p.view || p.create || p.edit || p.delete || p.admin);
        this.cdr.markForCheck();
      });
  }

  public onSubmitPassword(): void {
    if (this.isPasswordLoading) return;

    if (this.passwordForm.invalid) {
      this.passwordForm.markAllAsTouched();
      this.cdr.markForCheck();
      return;
    }

    this.isPasswordLoading = true;
    this.cdr.markForCheck();
    const { oldPassword, newPassword } = this.passwordForm.value;

    this.authService
      .changeOwnPassword(oldPassword, newPassword)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => {
          this.isPasswordLoading = false;
          this.cdr.markForCheck();
        }),
      )
      .subscribe({
        next: () => {
          this.notificationService.showSuccess(
            'Password updated successfully! Please sign in again with your new password.',
          );
          this.passwordForm.reset();
          this.cdr.markForCheck();
          this.authService.logout(false);
        },
        error: (err) => {
          console.error('Failed to change password', err);
          const isUnauthorized = (err as { status?: number } | null)?.status === 401;
          extractHttpErrorMessage(err, {
            fallback: isUnauthorized ? 'Incorrect current password.' : 'Failed to update password.',
          })
            .pipe(takeUntilDestroyed(this.destroyRef))
            .subscribe((msg) => {
              const displayMsg =
                isUnauthorized && msg === 'Unauthorized: Please log in again.'
                  ? 'Incorrect current password.'
                  : msg;
              this.notificationService.showError(displayMsg);
              this.cdr.markForCheck();
            });
        },
      });
  }

  public openCreateKeyModal(): void {
    if (!this.user) return;
    const userId = this.user.id;
    this.apiKeyService
      .openCreateKeyModal(userId)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((changed) => {
        if (changed && this.user?.id === userId) {
          this.loadApiKeys();
        }
      });
  }

  public openEditKeyModal(key: ApiKey): void {
    if (!this.user) return;
    const userId = this.user.id;
    this.apiKeyService
      .openEditKeyModal(userId, key)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((changed) => {
        if (changed && this.user?.id === userId) {
          this.loadApiKeys();
        }
      });
  }

  public openRevokeConfirm(key: ApiKey): void {
    if (!this.user) return;
    const userId = this.user.id;
    this.apiKeyService
      .confirmRevokeKey(userId, key)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((changed) => {
        if (changed && this.user?.id === userId) {
          this.loadApiKeys();
        }
      });
  }
}
