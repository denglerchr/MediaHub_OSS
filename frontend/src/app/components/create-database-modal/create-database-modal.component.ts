import { Component, OnInit, OnDestroy } from '@angular/core';
import {
  AbstractControl,
  FormArray,
  FormBuilder,
  FormGroup,
  ValidationErrors,
  Validators,
} from '@angular/forms';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { finalize, takeUntil } from 'rxjs/operators';
import { Subject } from 'rxjs';
import { DatabaseConfig } from '../../models';
import {
  CUSTOM_FIELD_NAME_PATTERN,
  DATABASE_NAME_MAX_LENGTH,
  DATABASE_NAME_PATTERN,
  reservedCustomFieldNameValidator,
} from '../../utils/validation';

const INTEGER_ONLY_PATTERN = /^\d+$/;
const CUSTOM_FIELD_NAME_MAX_LENGTH = 64;

@Component({
  selector: 'app-create-database-modal',
  templateUrl: './create-database-modal.component.html',
  styleUrls: ['./create-database-modal.component.css'],
  standalone: false,
})
export class CreateDatabaseModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'createDatabaseModal';
  createDbForm: FormGroup;
  isLoading = false;
  private destroy$ = new Subject<void>();

  constructor(
    private fb: FormBuilder,
    private databaseService: DatabaseService,
    private modalService: ModalService,
  ) {
    this.createDbForm = this.fb.group({
      name: [
        '',
        [
          Validators.required,
          Validators.maxLength(DATABASE_NAME_MAX_LENGTH),
          Validators.pattern(DATABASE_NAME_PATTERN),
        ],
      ],
      content_type: ['image', Validators.required],
      n_max_queued: [
        0,
        [Validators.required, Validators.min(0), Validators.pattern(INTEGER_ONLY_PATTERN)],
      ],
      create_preview: [true],
      auto_conversion: [''],

      housekeeping: this.fb.group({
        interval_value: [
          1,
          [Validators.required, Validators.min(0), Validators.pattern(INTEGER_ONLY_PATTERN)],
        ],
        interval_unit: ['h'],
        disk_space_value: [
          100,
          [Validators.required, Validators.min(0), Validators.pattern(INTEGER_ONLY_PATTERN)],
        ],
        disk_space_unit: ['G'],
        max_age_value: [
          0,
          [Validators.required, Validators.min(0), Validators.pattern(INTEGER_ONLY_PATTERN)],
        ],
        max_age_unit: ['d'],
      }),
      custom_fields: this.fb.array([]),
    });
  }

  ngOnInit(): void {
    this.modalService
      .getModalEvents(CreateDatabaseModalComponent.MODAL_ID)
      .pipe(takeUntil(this.destroy$))
      .subscribe((event) => {
        if (event.action === 'open' || event.action === 'close') {
          this.resetForm();
        }
      });

    this.createDbForm
      .get('content_type')
      ?.valueChanges.pipe(takeUntil(this.destroy$))
      .subscribe(() => {
        this.createDbForm.get('auto_conversion')?.setValue('');
      });

    this.customFields.valueChanges.pipe(takeUntil(this.destroy$)).subscribe(() => {
      this.customFields.controls.forEach((ctrl) => {
        ctrl.get('name')?.updateValueAndValidity({ onlySelf: true, emitEvent: false });
      });
    });
  }

  private resetForm(): void {
    this.isLoading = false;
    this.createDbForm.reset(
      {
        name: '',
        content_type: 'image',
        n_max_queued: 0,
        create_preview: true,
        auto_conversion: '',
        housekeeping: {
          interval_value: 1,
          interval_unit: 'h',
          disk_space_value: 100,
          disk_space_unit: 'G',
          max_age_value: 0,
          max_age_unit: 'd',
        },
      },
      { emitEvent: false },
    );
    this.customFields.clear();
  }

  private duplicateCustomFieldNameValidator = (
    control: AbstractControl,
  ): ValidationErrors | null => {
    const value = typeof control.value === 'string' ? control.value.trim().toLowerCase() : '';
    if (!value) {
      return null;
    }
    const group = control.parent;
    const array = group?.parent as FormArray | null;
    if (!array || !Array.isArray(array.controls)) {
      return null;
    }
    const isDuplicate = array.controls.some((sibling) => {
      if (sibling === group) {
        return false;
      }
      const siblingName = sibling.get('name')?.value;
      return typeof siblingName === 'string' && siblingName.trim().toLowerCase() === value;
    });
    return isDuplicate ? { duplicate: true } : null;
  };

  trackByIndex(index: number): number {
    return index;
  }

  get customFields(): FormArray {
    return this.createDbForm.get('custom_fields') as FormArray;
  }

  addCustomField(): void {
    const fieldGroup = this.fb.group({
      // FE-049/FE-053: reserved names are rejected up front (the backend refuses
      // them in AddField/toModel) and the row renders the specific error.
      name: [
        '',
        [
          Validators.required,
          Validators.maxLength(CUSTOM_FIELD_NAME_MAX_LENGTH),
          Validators.pattern(CUSTOM_FIELD_NAME_PATTERN),
          reservedCustomFieldNameValidator,
          this.duplicateCustomFieldNameValidator,
        ],
      ],
      type: ['TEXT', Validators.required],
      is_indexed: [true],
    });
    this.customFields.push(fieldGroup);
  }

  removeCustomField(index: number): void {
    this.customFields.removeAt(index);
  }

  onSubmit(): void {
    if (this.isLoading) return;

    if (this.createDbForm.invalid) {
      this.createDbForm.markAllAsTouched();
      return;
    }

    this.isLoading = true;
    const formValue = this.createDbForm.value;

    const config: DatabaseConfig = {};
    if (['image', 'audio', 'video'].includes(formValue.content_type)) {
      config.create_preview = formValue.create_preview;
      config.auto_conversion = formValue.auto_conversion;
    }

    const hkForm = formValue.housekeeping;
    const reconstructedHousekeeping = {
      interval: hkForm.interval_value > 0 ? `${hkForm.interval_value}${hkForm.interval_unit}` : '0',
      disk_space:
        hkForm.disk_space_value > 0 ? `${hkForm.disk_space_value}${hkForm.disk_space_unit}` : '0',
      max_age: hkForm.max_age_value > 0 ? `${hkForm.max_age_value}${hkForm.max_age_unit}` : '0',
    };

    const payload = {
      name: formValue.name,
      content_type: formValue.content_type,
      n_max_queued: formValue.n_max_queued,
      config: config,
      housekeeping: reconstructedHousekeeping,
      custom_fields: formValue.custom_fields,
    };

    this.databaseService
      .createDatabase(payload)
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => (this.isLoading = false)),
      )
      .subscribe({
        next: () => {
          this.modalService.close(CreateDatabaseModalComponent.MODAL_ID, true);
          this.resetForm();
        },
        error: () => {},
      });
  }

  closeModal(): void {
    this.modalService.close(CreateDatabaseModalComponent.MODAL_ID, false);
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}
