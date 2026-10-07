// frontend/src/app/pages/profile-page/profile-page.component.ts

import { Component, OnInit, OnDestroy, ChangeDetectorRef } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { Subject, combineLatest } from 'rxjs';
import { takeUntil, finalize, filter, take } from 'rxjs/operators';
import { User, ApiKey, Database } from '../../models';
import { AuthService } from '../../services/auth.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import { ApiKeyModalComponent } from '../../components/api-key-modal/api-key-modal.component';
import {
  ConfirmationModalComponent,
  ConfirmationModalData,
} from '../../components/confirmation-modal/confirmation-modal.component';

@Component({
  selector: 'app-profile-page',
  templateUrl: './profile-page.component.html',
  styleUrls: ['./profile-page.component.css'],
  standalone: false,
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

  public trackByKeyId(index: number, key: ApiKey): string {
    return key.id;
  }

  public passwordForm: FormGroup;
  public isPasswordLoading = false;
  public isKeysLoading = false;

  private destroy$ = new Subject<void>();

  constructor(
    private authService: AuthService,
    private databaseService: DatabaseService,
    private modalService: ModalService,
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
    // 1. Listen to current user
    this.authService.currentUser$.pipe(takeUntil(this.destroy$)).subscribe((user) => {
      this.user = user;
      if (user) {
        this.loadApiKeys();
      }
      this.cdr.markForCheck();
    });

    // 2. Fetch databases
    this.databaseService
      .loadDatabases()
      .pipe(takeUntil(this.destroy$))
      .subscribe({
        error: (err) => console.error('Failed to load databases on profile page', err),
      });

    // 3. Reactively map database permissions
    this.mapPermissions();
  }

  private passwordMatchValidator(g: FormGroup) {
    const newPass = g.get('newPassword')?.value;
    const confirmPass = g.get('confirmPassword')?.value;
    return newPass === confirmPass ? null : { mismatch: true };
  }

  private loadApiKeys(): void {
    if (!this.user) return;
    this.isKeysLoading = true;
    this.cdr.markForCheck();

    this.authService
      .getUserKeys(this.user.id)
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => {
          this.isKeysLoading = false;
          this.cdr.markForCheck();
        }),
      )
      .subscribe({
        next: (keys) => {
          this.apiKeys = keys || [];
          this.cdr.markForCheck();
        },
        error: (err) => {
          console.error('Failed to load user API keys', err);
          this.notificationService.showError('Could not load API keys.');
          this.cdr.markForCheck();
        },
      });
  }

  private mapPermissions(): void {
    // Fetch database list and align with user's permissions array
    combineLatest([this.databaseService.databases$, this.authService.currentUser$])
      .pipe(takeUntil(this.destroy$))
      .subscribe(([dbs, currentUser]) => {
        if (!currentUser) {
          this.databasePermissionsMapped = [];
          this.cdr.markForCheck();
          return;
        }

        if (currentUser.is_admin) {
          this.databasePermissionsMapped = dbs.map((db) => ({
            name: db.name,
            view: true,
            create: true,
            edit: true,
            delete: true,
            admin: true,
          }));
        } else {
          this.databasePermissionsMapped = dbs
            .map((db) => {
              const perm = currentUser.permissions?.find((p) => p.database_id === db.id);
              return {
                name: db.name,
                view: perm?.can_view || false,
                create: perm?.can_create || false,
                edit: perm?.can_edit || false,
                delete: perm?.can_delete || false,
                admin: perm?.can_admin || false,
              };
            })
            // only display databases where they have at least one permission
            .filter((p) => p.view || p.create || p.edit || p.delete || p.admin);
        }
        this.cdr.markForCheck();
      });
  }

  public onSubmitPassword(): void {
    if (this.passwordForm.invalid) {
      this.passwordForm.markAllAsTouched();
      return;
    }

    this.isPasswordLoading = true;
    this.cdr.markForCheck();
    const { oldPassword, newPassword } = this.passwordForm.value;

    this.authService
      .changeOwnPassword(oldPassword, newPassword)
      .pipe(
        finalize(() => {
          this.isPasswordLoading = false;
          this.cdr.markForCheck();
        }),
      )
      .subscribe({
        next: () => {
          this.notificationService.showSuccess('Password updated successfully!');
          this.passwordForm.reset();
          this.cdr.markForCheck();
        },
        error: (err) => {
          console.error('Failed to change password', err);
          if (err.status === 401) {
            this.notificationService.showError('Incorrect current password.');
          } else {
            this.notificationService.showError('Could not update password. Try again.');
          }
          this.cdr.markForCheck();
        },
      });
  }

  public openCreateKeyModal(): void {
    if (!this.user) return;
    this.modalService
      .open(ApiKeyModalComponent.MODAL_ID, { userId: this.user.id })
      .pipe(take(1))
      .subscribe((created) => {
        if (created) {
          this.loadApiKeys();
        }
      });
  }

  public openEditKeyModal(key: ApiKey): void {
    if (!this.user) return;
    this.modalService
      .open(ApiKeyModalComponent.MODAL_ID, { userId: this.user.id, apiKey: key })
      .pipe(take(1))
      .subscribe((updated) => {
        if (updated) {
          this.loadApiKeys();
        }
      });
  }

  public openRevokeConfirm(key: ApiKey): void {
    if (!this.user) return;

    const modalData: ConfirmationModalData = {
      title: 'Revoke API Key',
      message: `Are you sure you want to revoke/delete the API key "${key.name}"? Applications using this key will immediately lose access.`,
    };

    this.modalService
      .open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(
        take(1),
        filter((isConfirmed) => isConfirmed === true),
      )
      .subscribe(() => {
        // FE-043: the delete request must not outlive the component.
        this.authService
          .deleteUserKey(this.user!.id, key.id)
          .pipe(takeUntil(this.destroy$))
          .subscribe({
            next: () => {
              this.notificationService.showSuccess('API key revoked successfully.');
              this.loadApiKeys();
            },
            error: () => {
              // FE-046: the shared AuthService.handleError already surfaced the reason.
              this.cdr.markForCheck();
            },
          });
      });
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}
