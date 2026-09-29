import { Component, OnDestroy, OnInit } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { Observable, Subject, of } from 'rxjs';
import { switchMap, takeUntil, filter, take, finalize } from 'rxjs/operators';
import { Database, User, DatabaseConfig, CustomField, CustomFieldType } from '../../models';
import { DatabaseService, DatabaseUpdatePayload } from '../../services/database.service';
import { AuthService } from '../../services/auth.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import { ConfirmationModalComponent, ConfirmationModalData } from '../confirmation-modal/confirmation-modal.component';
import { isValidCustomFieldName } from '../../utils/validation';

@Component({
  selector: 'app-database-settings',
  templateUrl: './database-settings.component.html',
  styleUrls: ['./database-settings.component.css'],
  standalone: false
})
export class DatabaseSettingsComponent implements OnInit, OnDestroy {
  public selectedDatabase$: Observable<Database | null>;
  public currentUser$: Observable<User | null>;
  public settingsForm: FormGroup;
  public isLoading = false;

  public currentDb: Database | null = null;
  
  public canAdmin = false;
  public canCreate = false;
  public canDelete = false;
  public isGlobalAdmin = false;

  // Custom Field Form State
  public newFieldName = '';
  public newFieldType: CustomFieldType = 'TEXT';
  public newFieldIsIndexed = true;

  public editingFieldId: number | null = null;
  public editingFieldName = '';
  public editingFieldIsIndexed = false;

  private destroy$ = new Subject<void>();

  constructor(
    private route: ActivatedRoute,
    private databaseService: DatabaseService,
    private authService: AuthService,
    private modalService: ModalService,
    private notificationService: NotificationService,
    private fb: FormBuilder
  ) {
    this.selectedDatabase$ = this.databaseService.selectedDatabase$;
    this.currentUser$ = this.authService.currentUser$;

    this.settingsForm = this.fb.group({
      name: ['', [Validators.required]], // NEW: Added name field for renaming
      n_max_queued: [0, [Validators.required, Validators.min(0)]],
      create_preview: [true],
      auto_conversion: [''], 
      housekeeping: this.fb.group({
        interval_value: [0, [Validators.required, Validators.min(0)]],
        interval_unit: ['h'],
        disk_space_value: [0, [Validators.required, Validators.min(0)]],
        disk_space_unit: ['G'],
        max_age_value: [0, [Validators.required, Validators.min(0)]],
        max_age_unit: ['d'],
      }),
    });
  }

  ngOnInit(): void {
    this.route.paramMap
      .pipe(
        takeUntil(this.destroy$),
        switchMap((params) => {
          const id = params.get('id'); // UPDATED: Extract 'id' instead of 'name'
          this.settingsForm.reset();
          this.isLoading = true; 
          return id ? this.databaseService.selectDatabase(id) : of(null);
        })
      )
      .subscribe();

    this.selectedDatabase$
      .pipe(takeUntil(this.destroy$))
      .subscribe((db) => {
          this.isLoading = false; 
          if (db) {
            this.currentDb = db;
            
            // Extract numerical values and string units from the DB payload
            const interval = this.parseHousekeepingString(db.housekeeping.interval, 'h');
            const diskSpace = this.parseHousekeepingString(db.housekeeping.disk_space, 'G');
            const maxAge = this.parseHousekeepingString(db.housekeeping.max_age, 'd');

            this.settingsForm.patchValue({
              name: db.name,
              n_max_queued: db.n_max_queued,
              ...db.config, 
              housekeeping: {
                interval_value: interval.value,
                interval_unit: interval.unit,
                disk_space_value: diskSpace.value,
                disk_space_unit: diskSpace.unit,
                max_age_value: maxAge.value,
                max_age_unit: maxAge.unit
              }
            });
          } else {
            this.currentDb = null;
            this.settingsForm.reset(); 
          }
          this.updatePermissions();
        });

    this.currentUser$
      .pipe(takeUntil(this.destroy$))
      .subscribe(() => {
        this.updatePermissions();
      });
  }

  /**
   * Helper method to break apart strings like "100G" or "24h"
   */
  private parseHousekeepingString(val: string | number | undefined, defaultUnit: string): { value: number, unit: string } {
    if (!val || val === 0 || val === '0') {
      return { value: 0, unit: defaultUnit };
    }
    const str = String(val).trim();
    // Match the numeric part and the unit part (e.g. 100 and G)
    const match = str.match(/^(\d+)([a-zA-Z]*)$/);
    
    if (match) {
      const numericVal = parseInt(match[1], 10);
      const stringUnit = match[2] ? match[2] : defaultUnit;
      return { value: numericVal, unit: stringUnit };
    }
    return { value: 0, unit: defaultUnit };
  }

  private updatePermissions(): void {
    const user = this.authService.getCurrentUser();
    
    if (!user || !this.currentDb) {
      this.canAdmin = false;
      this.canCreate = false;
      this.canDelete = false;
      this.isGlobalAdmin = false;
      this.settingsForm.disable();
      return;
    }

    this.isGlobalAdmin = user.is_admin;

    if (user.is_admin) {
      this.canAdmin = true;
      this.canCreate = true;
      this.canDelete = true;
    } else {
      // UPDATED: Match the permission's database_id against currentDb.id
      const dbPermission = user.permissions?.find(p => p.database_id === this.currentDb!.id);
      this.canAdmin = dbPermission?.can_admin || false;
      this.canCreate = dbPermission?.can_create || false;
      this.canDelete = dbPermission?.can_delete || false;
    }

    if (this.canAdmin) {
      this.settingsForm.enable();
    } else {
      this.settingsForm.disable();
    }
  }

  onSaveSettings(): void {
    if (this.settingsForm.invalid || !this.currentDb || !this.canAdmin) {
      return;
    }
    this.isLoading = true;

    const formValue = this.settingsForm.value;
    const config: DatabaseConfig = {};
    
    if (['image', 'audio', 'video'].includes(this.currentDb.content_type)) {
      config.create_preview = formValue.create_preview;
      config.auto_conversion = formValue.auto_conversion;
    }

    // Reconstruct the strings (e.g. {value: 100, unit: "G"} => "100G")
    // If the value is 0, we send "0" to disable it as per concept spec
    const hkForm = formValue.housekeeping;
    const reconstructedHousekeeping = {
      interval: hkForm.interval_value > 0 ? `${hkForm.interval_value}${hkForm.interval_unit}` : "0",
      disk_space: hkForm.disk_space_value > 0 ? `${hkForm.disk_space_value}${hkForm.disk_space_unit}` : "0",
      max_age: hkForm.max_age_value > 0 ? `${hkForm.max_age_value}${hkForm.max_age_unit}` : "0",
    };

    const payload: DatabaseUpdatePayload = {
      name: formValue.name, // NEW: Include name in payload
      n_max_queued: formValue.n_max_queued,
      config: config,
      housekeeping: reconstructedHousekeeping,
    };

    this.databaseService
      .updateDatabase(this.currentDb.id, payload) // UPDATED: Use ULID
      .pipe(finalize(() => (this.isLoading = false)))
      .subscribe(() => {
        this.settingsForm.markAsPristine(); 
      });
  }

  onTriggerHousekeeping(): void {
    if (!this.currentDb || !this.canDelete) return;
    this.isLoading = true;
    this.databaseService
      .triggerHousekeeping(this.currentDb.id) // UPDATED: Use ULID
      .pipe(finalize(() => (this.isLoading = false)))
      .subscribe();
  }

  onDeleteDatabase(): void {
    if (!this.currentDb || !this.isGlobalAdmin) return;

    const modalData: ConfirmationModalData = {
      title: 'Confirm Database Deletion',
      message: `This action will permanently delete the '${this.currentDb.name}' database and all entries within it. This action cannot be undone.`,
    };

    this.modalService
      .open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(
        take(1),
        filter((confirmed) => confirmed === true)
      )
      .subscribe(() => {
        if (this.currentDb) {
          this.isLoading = true;
          this.databaseService
            .deleteDatabase(this.currentDb.id)
            .pipe(
              takeUntil(this.destroy$),
              finalize(() => (this.isLoading = false))
            )
            .subscribe();
        }
      });
  }

  onAddField(): void {
    if (!this.currentDb || !this.canAdmin || !this.newFieldName) return;

    if (!isValidCustomFieldName(this.newFieldName)) {
      this.notificationService.showError('Field name must start with a letter or underscore and contain only alphanumeric characters or underscores.');
      return;
    }

    this.isLoading = true;
    const newField: CustomField = {
      name: this.newFieldName,
      type: this.newFieldType,
      is_indexed: this.newFieldIsIndexed
    };

    this.databaseService.addCustomField(this.currentDb.id, newField)
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => this.isLoading = false)
      )
      .subscribe(() => {
        this.newFieldName = '';
        this.newFieldType = 'TEXT';
        this.newFieldIsIndexed = true;
      });
  }

  onStartEditField(field: CustomField): void {
    if (field.id === undefined) return;
    this.editingFieldId = field.id;
    this.editingFieldName = field.name;
    this.editingFieldIsIndexed = field.is_indexed || false;
  }

  onCancelEditField(): void {
    this.editingFieldId = null;
  }

  onSaveField(fieldId: number): void {
    if (!this.currentDb || !this.canAdmin || !this.editingFieldName) return;

    if (!isValidCustomFieldName(this.editingFieldName)) {
      this.notificationService.showError('Field name must start with a letter or underscore and contain only alphanumeric characters or underscores.');
      return;
    }

    this.isLoading = true;
    this.databaseService.updateCustomField(this.currentDb.id, fieldId, this.editingFieldName, this.editingFieldIsIndexed)
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => {
          this.isLoading = false;
          this.editingFieldId = null;
        })
      )
      .subscribe();
  }

  onDeleteField(field: CustomField): void {
    if (!this.currentDb || !this.canAdmin || field.id === undefined) return;

    const modalData: ConfirmationModalData = {
      title: 'Confirm Custom Field Deletion',
      message: `This action will permanently delete the custom field '${field.name}' from database '${this.currentDb.name}' and discard all entry data stored under this field. This action cannot be undone.`,
    };

    this.modalService
      .open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(
        take(1),
        filter((confirmed) => confirmed === true)
      )
      .subscribe(() => {
        if (this.currentDb && field.id !== undefined) {
          this.isLoading = true;
          this.databaseService
            .deleteCustomField(this.currentDb.id, field.id)
            .pipe(
              takeUntil(this.destroy$),
              finalize(() => (this.isLoading = false))
            )
            .subscribe();
        }
      });
  }

  trackByFieldId(index: number, field: CustomField): number | string {
    return field.id ?? field.name;
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}