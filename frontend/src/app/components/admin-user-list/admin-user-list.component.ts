// frontend/src/app/components/admin-user-list/admin-user-list.component.ts

import { Component, OnInit, OnDestroy, ChangeDetectorRef } from '@angular/core';
import { FormBuilder, FormGroup, FormArray, Validators, AbstractControl } from '@angular/forms';
import { Subject, combineLatest, of } from 'rxjs';
import { takeUntil, finalize, filter, take, switchMap, catchError } from 'rxjs/operators';
import { User, Permission, Database, ApiKey } from '../../models';
import { AuthService } from '../../services/auth.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import {
  ConfirmationModalComponent,
  ConfirmationModalData,
} from '../confirmation-modal/confirmation-modal.component';
import { ApiKeyModalComponent } from '../api-key-modal/api-key-modal.component';

@Component({
  selector: 'app-admin-user-list',
  templateUrl: './admin-user-list.component.html',
  styleUrls: ['./admin-user-list.component.css'],
  standalone: false,
})
export class AdminUserListComponent implements OnInit, OnDestroy {
  public users: User[] = [];
  public availableDatabases: { id: string; name: string }[] = [];
  public isLoading = true;

  // Master-Detail State
  public selectedUser: User | null = null;
  public isNewUser = false;
  public detailForm: FormGroup;
  public isSaving = false;
  // FE-001: gates the username error message (touched || submitted) so the
  // create-user form does not show it before first interaction.
  public submitted = false;

  // Sidebar List Tab (Standard Users vs Service Accounts vs SSO/OIDC)
  public activeListTab: 'standard' | 'service' | 'oidc' = 'standard';

  // Detail Pane Tabs (Settings vs API Keys)
  public activeDetailTab: 'settings' | 'keys' = 'settings';
  public userKeys: ApiKey[] = [];
  public isKeysLoading = false;

  private destroy$ = new Subject<void>();
  // FE-020: selection events funnel through this Subject so `switchMap` cancels the
  // in-flight `GET /api/user/{id}` of the previously selected user. Two fast clicks
  // used to race two requests and could populate the form with the *other* user.
  private selectUser$ = new Subject<User>();

  constructor(
    private authService: AuthService,
    private databaseService: DatabaseService,
    private modalService: ModalService,
    private notificationService: NotificationService,
    private fb: FormBuilder,
    private cdr: ChangeDetectorRef,
  ) {
    // Initialize the Reactive Form for the detail pane
    this.detailForm = this.fb.group({
      id: [null],
      // FE-001: the backend imposes no character restriction on usernames
      // (internal/httpserver/userhandler/users.go only rejects empty names), so the
      // old /^[a-zA-Z0-9_]+$/ pattern must not invalidate OIDC names like "john.doe" —
      // the form shares its validity with the permissions FormArray and blocked every
      // edit for such users. The length cap follows the DB contract
      // (AI/Concept/03_00_Database_Static.md: username VARCHAR(64) with
      // CHECK(length(username) <= 64)): longer names die at the DB constraint.
      username: ['', [Validators.required, Validators.maxLength(64)]],
      password: [''], // Will be required dynamically for new standard users
      is_admin: [false],
      account_type: ['local'],
      permissions: this.fb.array([]),
    });
  }

  ngOnInit(): void {
    this.loadData();

    // FE-020: resolve the full user record through switchMap — a newer selection
    // cancels the older request, so a slow response can never overwrite the form
    // of the user that is highlighted in the list.
    this.selectUser$
      .pipe(
        switchMap((user) =>
          this.authService.getUser(user.id).pipe(
            // A failed detail fetch must not kill the selection stream — fall back
            // to the (shallow) list record that was clicked.
            catchError(() => of(user)),
          ),
        ),
        takeUntil(this.destroy$),
      )
      .subscribe((fullUser) => {
        // A cleared selection must not be re-populated by a late response.
        if (!this.selectedUser || this.selectedUser.id !== fullUser.id) {
          return;
        }
        this.selectedUser = fullUser;
        this.buildForm(fullUser);
        this.cdr.markForCheck();
      });

    // Listen to changes on the is_admin toggle to disable/enable the permissions table
    this.detailForm
      .get('is_admin')
      ?.valueChanges.pipe(takeUntil(this.destroy$))
      .subscribe((isAdmin) => {
        if (isAdmin) {
          this.permissions.disable();
        } else {
          this.permissions.enable();
        }
      });
  }

  /**
   * Sets the active sidebar user list tab and reloads.
   */
  setListTab(tab: 'standard' | 'service' | 'oidc'): void {
    this.activeListTab = tab;
    this.clearSelection();
    this.loadData();
  }

  /**
   * Sets the active detail pane tab.
   */
  setDetailTab(tab: 'settings' | 'keys'): void {
    this.activeDetailTab = tab;
    if (tab === 'keys') {
      this.loadUserKeys();
    }
  }

  /**
   * Fetches both users and databases simultaneously.
   */
  loadData(): void {
    this.isLoading = true;
    this.cdr.markForCheck();

    let filterAccountType: string | undefined;
    if (this.activeListTab === 'service') filterAccountType = 'service_account';
    else if (this.activeListTab === 'oidc') filterAccountType = 'oidc';
    else filterAccountType = 'local';

    combineLatest([
      this.authService.getUsers(filterAccountType),
      this.databaseService.loadDatabases(),
    ])
      .pipe(
        take(1),
        takeUntil(this.destroy$),
        finalize(() => {
          this.isLoading = false;
          this.cdr.markForCheck();
        }),
      )
      .subscribe({
        next: ([users, databases]) => {
          this.users = users;
          this.availableDatabases = databases.map((db) => ({ id: db.id, name: db.name }));

          // If we reloaded data and a user is selected, refresh their form data
          if (this.selectedUser) {
            const refreshedUser = this.users.find((u) => u.id === this.selectedUser!.id);
            if (refreshedUser) {
              this.selectUser(refreshedUser);
            } else {
              this.clearSelection();
            }
          }
        },
        error: (err) => {
          console.error('Failed to load admin user data:', err);
          this.notificationService.showError('Could not load user data.');
        },
      });
  }

  // --- Form Array Getter ---
  get permissions(): FormArray {
    return this.detailForm.get('permissions') as FormArray;
  }

  // --- Master Pane Actions ---

  trackById(index: number, user: User): string {
    return user.id;
  }

  selectUser(user: User): void {
    this.isNewUser = false;
    this.submitted = false;
    this.selectedUser = user;
    this.activeDetailTab = 'settings';

    // 1. Immediately populate form controls synchronously
    this.buildForm(user);
    // FE-043: no `loadUserKeys()` here — the keys tab reloads them itself
    // (setDetailTab('keys')); fetching on every selection was redundant.

    // 2. Fetch the full user record asynchronously (permissions may be stale in the
    //    list). FE-020: routed through selectUser$ so switchMap cancels out-of-order
    //    responses.
    this.selectUser$.next(user);
  }

  createNewUser(): void {
    // FE-043: clicking the "New User Draft" row again (or the New User button while
    // a draft is open) must not wipe unsaved input — it just starts another draft.
    if (this.isNewUser) {
      return;
    }
    this.isNewUser = true;
    this.submitted = false;
    this.selectedUser = null;
    this.activeDetailTab = 'settings';
    this.userKeys = [];

    const isService = this.activeListTab === 'service';
    const emptyUser: Partial<User> = {
      username: '',
      is_admin: false,
      account_type: isService ? 'service_account' : 'local',
      permissions: [],
    };

    this.buildForm(emptyUser as User);

    if (isService) {
      // Password field is hidden and bypassed for service accounts
      this.detailForm.get('password')?.clearValidators();
    } else {
      this.detailForm
        .get('password')
        ?.setValidators([Validators.required, Validators.minLength(8)]);
    }
    this.detailForm.get('password')?.updateValueAndValidity();
    this.cdr.markForCheck();
  }

  clearSelection(): void {
    this.selectedUser = null;
    this.isNewUser = false;
    this.submitted = false;
    this.activeDetailTab = 'settings';
    this.detailForm.reset();
    this.permissions.clear();
    this.userKeys = [];
    this.cdr.markForCheck();
  }

  // --- Detail Pane Logic ---

  private buildForm(user: User): void {
    // 1. Update top-level detailForm controls
    this.detailForm.patchValue(
      {
        id: user.id || null,
        username: user.username || '',
        is_admin: user.is_admin || false,
        account_type: user.account_type || 'local',
        password: '',
      },
      { emitEvent: false },
    );

    // FE-001: OIDC usernames are provisioned verbatim from IdP claims and owned by
    // the identity provider, and service-account names are fixed at creation (the
    // contract for machine identities, see AI/Reports/20261006_frontend_review.md
    // FE-001 / Appendix C) — make both read-only when editing. The control is only
    // disabled here (never at creation), and onSaveUser() uses getRawValue(), so the
    // name still reaches the API. Note: the backend would permit renaming
    // (users.go:441, 02_02_00_HTTP_User.md "Updates an existing user's username") —
    // this UI restriction is a deliberate policy choice, not a backend limitation.
    const usernameCtrl = this.detailForm.get('username');
    if (
      !this.isNewUser &&
      (user.account_type === 'oidc' || user.account_type === 'service_account')
    ) {
      usernameCtrl?.disable({ emitEvent: false });
    } else {
      usernameCtrl?.enable({ emitEvent: false });
    }

    // 2. Patch or add permission FormGroups for available databases
    this.availableDatabases.forEach((db, i) => {
      const existingPerm = user.permissions?.find((p) => p.database_id === db.id);
      const permValues = {
        database_id: db.id,
        database_name: db.name,
        can_view: existingPerm?.can_view || false,
        can_create: existingPerm?.can_create || false,
        can_edit: existingPerm?.can_edit || false,
        can_delete: existingPerm?.can_delete || false,
        can_admin: existingPerm?.can_admin || false,
      };

      if (i < this.permissions.length) {
        // In-place value update so Reactive Forms ControlValueAccessor updates DOM checkboxes seamlessly
        this.permissions.at(i).patchValue(permValues, { emitEvent: false });
      } else {
        this.permissions.push(
          this.fb.group({
            database_id: [permValues.database_id],
            database_name: [permValues.database_name],
            can_view: [permValues.can_view],
            can_create: [permValues.can_create],
            can_edit: [permValues.can_edit],
            can_delete: [permValues.can_delete],
            can_admin: [permValues.can_admin],
          }),
        );
      }
    });

    // Remove extra controls if database count decreased
    while (this.permissions.length > this.availableDatabases.length) {
      this.permissions.removeAt(this.permissions.length - 1);
    }

    // 3. Explicitly sync permissions enabled/disabled state based on user.is_admin
    if (user.is_admin) {
      this.permissions.disable({ emitEvent: false });
    } else {
      this.permissions.enable({ emitEvent: false });
    }

    // 4. Handle password field validation on edit
    if (!this.isNewUser) {
      if (user.account_type === 'service_account' || user.account_type === 'oidc') {
        this.detailForm.get('password')?.clearValidators();
      } else {
        this.detailForm.get('password')?.setValidators([Validators.minLength(8)]);
      }
      this.detailForm.get('password')?.updateValueAndValidity({ emitEvent: false });
    }

    this.cdr.markForCheck();
  }

  toggleColumnAll(
    permissionType: 'can_view' | 'can_create' | 'can_edit' | 'can_delete' | 'can_admin',
  ): void {
    if (this.detailForm.get('is_admin')?.value) return;

    const allTrue = this.permissions.controls.every(
      (ctrl) => ctrl.get(permissionType)?.value === true,
    );
    const newValue = !allTrue;

    this.permissions.controls.forEach((ctrl) => {
      ctrl.get(permissionType)?.setValue(newValue);
    });

    this.detailForm.markAsDirty();
  }

  onSaveUser(): void {
    this.submitted = true;
    if (this.detailForm.invalid) {
      this.detailForm.markAllAsTouched();
      return;
    }

    this.isSaving = true;

    const formData = JSON.parse(JSON.stringify(this.detailForm.getRawValue()));

    // Bypasses password for service accounts and OIDC accounts or if password wasn't provided for edit
    if (
      formData.account_type === 'service_account' ||
      formData.account_type === 'oidc' ||
      !formData.password
    ) {
      delete formData.password;
    }

    if (formData.permissions) {
      // FE-042: typed instead of `(p: any)` — strip the display-only column name.
      formData.permissions = formData.permissions.map((p: Record<string, unknown>) => {
        const { database_name, ...rest } = p;
        return rest;
      });
    }

    if (this.isNewUser) {
      delete formData.id;
    }

    const apiCall$ = this.isNewUser
      ? this.authService.createUser(formData)
      : this.authService.updateUser(formData.id, formData);

    apiCall$.pipe(finalize(() => (this.isSaving = false))).subscribe({
      next: (savedUser) => {
        const action = this.isNewUser ? 'created' : 'updated';
        this.notificationService.showSuccess(`User ${savedUser.username} ${action} successfully!`);
        this.loadData();
      },
      error: () => {
        // FE-046: the shared AuthService.handleError already surfaced the reason.
      },
    });
  }

  openDeleteConfirm(): void {
    const userToDelete = this.selectedUser;
    if (!userToDelete) return;

    const modalData: ConfirmationModalData = {
      title: 'Delete User',
      message: `Are you sure you want to delete the user "${userToDelete.username}"? This action cannot be undone.`,
    };

    this.modalService
      .open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(
        take(1),
        filter((isConfirmed) => isConfirmed === true),
      )
      .subscribe(() => {
        this.authService
          .deleteUser(userToDelete.id)
          .pipe(takeUntil(this.destroy$))
          .subscribe({
            next: () => {
              this.notificationService.showSuccess(`User deleted successfully.`);
              this.clearSelection();
              this.loadData();
            },
            error: () => {
              // FE-046: the shared AuthService.handleError already surfaced the reason.
            },
          });
      });
  }

  // --- API Key Management (Delegated) ---

  loadUserKeys(): void {
    if (!this.selectedUser) return;
    this.isKeysLoading = true;
    this.authService
      .getUserKeys(this.selectedUser.id)
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => {
          this.isKeysLoading = false;
          this.cdr.markForCheck();
        }),
      )
      .subscribe({
        next: (keys) => {
          this.userKeys = keys || [];
        },
        error: (err) => {
          console.error('Failed to load user keys', err);
          this.notificationService.showError('Could not load user API keys.');
        },
      });
  }

  openCreateKeyModal(): void {
    if (!this.selectedUser) return;
    this.modalService
      .open(ApiKeyModalComponent.MODAL_ID, { userId: this.selectedUser.id })
      .pipe(take(1))
      .subscribe((created) => {
        if (created) {
          this.loadUserKeys();
        }
      });
  }

  openEditKeyModal(key: ApiKey): void {
    if (!this.selectedUser) return;
    this.modalService
      .open(ApiKeyModalComponent.MODAL_ID, { userId: this.selectedUser.id, apiKey: key })
      .pipe(take(1))
      .subscribe((updated) => {
        if (updated) {
          this.loadUserKeys();
        }
      });
  }

  openRevokeKeyConfirm(key: ApiKey): void {
    if (!this.selectedUser) return;
    // FE-045: capture the owner up front — re-reading this.selectedUser at confirm
    // time could revoke the key on the *wrong* user if the selection changed while
    // the confirmation modal was open (as openDeleteConfirm already does).
    const keyOwner = this.selectedUser;

    const modalData: ConfirmationModalData = {
      title: 'Revoke User API Key',
      message: `Are you sure you want to revoke/delete the API key "${key.name}" on behalf of this user?`,
    };

    this.modalService
      .open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(
        take(1),
        filter((isConfirmed) => isConfirmed === true),
      )
      .subscribe(() => {
        this.authService
          .deleteUserKey(keyOwner.id, key.id)
          .pipe(takeUntil(this.destroy$))
          .subscribe({
            next: () => {
              this.notificationService.showSuccess('API key revoked successfully.');
              this.loadUserKeys();
            },
            error: () => {
              // FE-046: the shared AuthService.handleError already surfaced the
              // precise reason (previously swallowed + a generic toast).
            },
          });
      });
  }

  trackByDbId(index: number, control: AbstractControl): string | number {
    return control?.get ? (control.get('database_id')?.value ?? index) : index;
  }

  trackByKeyId(index: number, key: ApiKey): string {
    return key.id;
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
    this.selectUser$.complete();
  }
}
