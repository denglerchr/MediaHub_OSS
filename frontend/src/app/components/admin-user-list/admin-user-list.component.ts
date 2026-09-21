// frontend/src/app/components/admin-user-list/admin-user-list.component.ts

import { Component, OnInit, OnDestroy, ChangeDetectorRef } from '@angular/core';
import { FormBuilder, FormGroup, FormArray, Validators } from '@angular/forms';
import { Subject, combineLatest } from 'rxjs';
import { takeUntil, finalize, filter, take } from 'rxjs/operators';
import { User, Permission, Database, ApiKey } from '../../models';
import { AuthService } from '../../services/auth.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import { ConfirmationModalComponent, ConfirmationModalData } from '../confirmation-modal/confirmation-modal.component';
import { ApiKeyModalComponent } from '../api-key-modal/api-key-modal.component';

@Component({
  selector: 'app-admin-user-list',
  templateUrl: './admin-user-list.component.html',
  styleUrls: ['./admin-user-list.component.css'],
  standalone: false,
})
export class AdminUserListComponent implements OnInit, OnDestroy {
  public users: User[] = [];
  public availableDatabases: { id: string, name: string }[] = [];
  public isLoading = true;
  
  // Master-Detail State
  public selectedUser: User | null = null;
  public isNewUser = false;
  public detailForm: FormGroup;
  public isSaving = false;

  // Sidebar List Tab (Standard Users vs Service Accounts vs SSO/OIDC)
  public activeListTab: 'standard' | 'service' | 'oidc' = 'standard';
  
  // Detail Pane Tabs (Settings vs API Keys)
  public activeDetailTab: 'settings' | 'keys' = 'settings';
  public userKeys: ApiKey[] = [];
  public isKeysLoading = false;

  private destroy$ = new Subject<void>();

  constructor(
    private authService: AuthService,
    private databaseService: DatabaseService,
    private modalService: ModalService,
    private notificationService: NotificationService,
    private fb: FormBuilder,
    private cdr: ChangeDetectorRef
  ) {
    // Initialize the Reactive Form for the detail pane
    this.detailForm = this.fb.group({
      id: [null],
      username: ['', [Validators.required, Validators.pattern(/^[a-zA-Z0-9_]+$/)]],
      password: [''], // Will be required dynamically for new standard users
      is_admin: [false],
      account_type: ['local'],
      permissions: this.fb.array([])
    });
  }

  ngOnInit(): void {
    this.loadData();

    // Listen to changes on the is_admin toggle to disable/enable the permissions table
    this.detailForm.get('is_admin')?.valueChanges
      .pipe(takeUntil(this.destroy$))
      .subscribe(isAdmin => {
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
      this.databaseService.loadDatabases()
    ])
    .pipe(
      take(1),
      takeUntil(this.destroy$),
      finalize(() => {
        this.isLoading = false;
        this.cdr.markForCheck();
      })
    )
    .subscribe({
      next: ([users, databases]) => {
        this.users = users;
        this.availableDatabases = databases.map(db => ({ id: db.id, name: db.name }));
        
        // If we reloaded data and a user is selected, refresh their form data
        if (this.selectedUser) {
          const refreshedUser = this.users.find(u => u.id === this.selectedUser!.id);
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
      }
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
    this.selectedUser = user;
    this.activeDetailTab = 'settings';

    // 1. Immediately populate form controls synchronously
    this.buildForm(user);
    this.loadUserKeys();

    // 2. Fetch full user record asynchronously to ensure permissions are up to date
    this.authService.getUser(user.id)
      .pipe(take(1), takeUntil(this.destroy$))
      .subscribe({
        next: (fullUser) => {
          this.selectedUser = fullUser;
          this.buildForm(fullUser);
        },
        error: () => {
          this.cdr.markForCheck();
        }
      });
  }

  createNewUser(): void {
    this.isNewUser = true;
    this.selectedUser = null; 
    this.activeDetailTab = 'settings';
    this.userKeys = [];
    
    const isService = this.activeListTab === 'service';
    const emptyUser: Partial<User> = {
      username: '',
      is_admin: false,
      account_type: isService ? 'service_account' : 'local',
      permissions: []
    };
    
    this.buildForm(emptyUser as User);
    
    if (isService) {
      // Password field is hidden and bypassed for service accounts
      this.detailForm.get('password')?.clearValidators();
    } else {
      this.detailForm.get('password')?.setValidators([Validators.required, Validators.minLength(8)]);
    }
    this.detailForm.get('password')?.updateValueAndValidity();
    this.cdr.markForCheck();
  }

  clearSelection(): void {
    this.selectedUser = null;
    this.isNewUser = false;
    this.activeDetailTab = 'settings';
    this.detailForm.reset();
    this.permissions.clear();
    this.userKeys = [];
    this.cdr.markForCheck();
  }

  // --- Detail Pane Logic ---

  private buildForm(user: User): void {
    // 1. Update top-level detailForm controls
    this.detailForm.patchValue({
      id: user.id || null,
      username: user.username || '',
      is_admin: user.is_admin || false,
      account_type: user.account_type || 'local',
      password: ''
    }, { emitEvent: false });

    // 2. Patch or add permission FormGroups for available databases
    this.availableDatabases.forEach((db, i) => {
      const existingPerm = user.permissions?.find(p => p.database_id === db.id);
      const permValues = {
        database_id: db.id,
        database_name: db.name,
        can_view: existingPerm?.can_view || false,
        can_create: existingPerm?.can_create || false,
        can_edit: existingPerm?.can_edit || false,
        can_delete: existingPerm?.can_delete || false,
        can_admin: existingPerm?.can_admin || false
      };

      if (i < this.permissions.length) {
        // In-place value update so Reactive Forms ControlValueAccessor updates DOM checkboxes seamlessly
        this.permissions.at(i).patchValue(permValues, { emitEvent: false });
      } else {
        this.permissions.push(this.fb.group({
          database_id: [permValues.database_id],
          database_name: [permValues.database_name],
          can_view: [permValues.can_view],
          can_create: [permValues.can_create],
          can_edit: [permValues.can_edit],
          can_delete: [permValues.can_delete],
          can_admin: [permValues.can_admin]
        }));
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

  toggleColumnAll(permissionType: 'can_view' | 'can_create' | 'can_edit' | 'can_delete' | 'can_admin'): void {
    if (this.detailForm.get('is_admin')?.value) return;

    const allTrue = this.permissions.controls.every(ctrl => ctrl.get(permissionType)?.value === true);
    const newValue = !allTrue;

    this.permissions.controls.forEach(ctrl => {
      ctrl.get(permissionType)?.setValue(newValue);
    });
    
    this.detailForm.markAsDirty();
  }

  onSaveUser(): void {
    if (this.detailForm.invalid) {
      this.detailForm.markAllAsTouched();
      return;
    }

    this.isSaving = true;
    
    const formData = JSON.parse(JSON.stringify(this.detailForm.getRawValue()));

    // Bypasses password for service accounts and OIDC accounts or if password wasn't provided for edit
    if (formData.account_type === 'service_account' || formData.account_type === 'oidc' || !formData.password) {
      delete formData.password;
    }

    if (formData.permissions) {
      formData.permissions = formData.permissions.map((p: any) => {
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

    apiCall$.pipe(finalize(() => this.isSaving = false)).subscribe({
      next: (savedUser) => {
        const action = this.isNewUser ? 'created' : 'updated';
        this.notificationService.showSuccess(`User ${savedUser.username} ${action} successfully!`);
        this.loadData();
      },
      error: (err) => {
        console.error('Failed to save user:', err);
      }
    });
  }

  openDeleteConfirm(): void {
    const userToDelete = this.selectedUser;
    if (!userToDelete) return;

    const modalData: ConfirmationModalData = {
      title: 'Delete User',
      message: `Are you sure you want to delete the user "${userToDelete.username}"? This action cannot be undone.`,
    };

    this.modalService.open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(take(1), filter(isConfirmed => isConfirmed === true))
      .subscribe(() => {
        this.authService.deleteUser(userToDelete.id)
          .pipe(takeUntil(this.destroy$))
          .subscribe({
            next: () => {
              this.notificationService.showSuccess(`User deleted successfully.`);
              this.clearSelection();
              this.loadData();
            },
            error: (err) => {
              console.error('Failed to delete user', err);
            }
          });
      });
  }

  // --- API Key Management (Delegated) ---

  loadUserKeys(): void {
    if (!this.selectedUser) return;
    this.isKeysLoading = true;
    this.authService.getUserKeys(this.selectedUser.id)
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => {
          this.isKeysLoading = false;
          this.cdr.markForCheck();
        })
      )
      .subscribe({
        next: (keys) => {
          this.userKeys = keys || [];
        },
        error: (err) => {
          console.error('Failed to load user keys', err);
          this.notificationService.showError('Could not load user API keys.');
        }
      });
  }

  openCreateKeyModal(): void {
    if (!this.selectedUser) return;
    this.modalService.open(ApiKeyModalComponent.MODAL_ID, { userId: this.selectedUser.id })
      .pipe(take(1))
      .subscribe(created => {
        if (created) {
          this.loadUserKeys();
        }
      });
  }

  openEditKeyModal(key: ApiKey): void {
    if (!this.selectedUser) return;
    this.modalService.open(ApiKeyModalComponent.MODAL_ID, { userId: this.selectedUser.id, apiKey: key })
      .pipe(take(1))
      .subscribe(updated => {
        if (updated) {
          this.loadUserKeys();
        }
      });
  }

  openRevokeKeyConfirm(key: ApiKey): void {
    if (!this.selectedUser) return;

    const modalData: ConfirmationModalData = {
      title: 'Revoke User API Key',
      message: `Are you sure you want to revoke/delete the API key "${key.name}" on behalf of this user?`
    };

    this.modalService.open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(take(1), filter(isConfirmed => isConfirmed === true))
      .subscribe(() => {
        this.authService.deleteUserKey(this.selectedUser!.id, key.id)
          .pipe(takeUntil(this.destroy$))
          .subscribe({
            next: () => {
              this.notificationService.showSuccess('API key revoked successfully.');
              this.loadUserKeys();
            },
            error: (err) => {
              console.error('Failed to revoke key', err);
              this.notificationService.showError('Could not revoke key.');
            }
          });
      });
  }

  trackByDbId(index: number, control: any): string | number {
    return control?.get ? (control.get('database_id')?.value ?? index) : index;
  }

  trackByKeyId(index: number, key: ApiKey): string {
    return key.id;
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}