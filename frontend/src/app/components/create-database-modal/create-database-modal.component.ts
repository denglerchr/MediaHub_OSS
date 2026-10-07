import { Component, OnInit, OnDestroy } from '@angular/core';
import { FormBuilder, FormGroup, FormArray, Validators } from '@angular/forms';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { finalize, takeUntil } from 'rxjs/operators';
import { Subject } from 'rxjs';
import { DatabaseConfig } from '../../models';
import {
  CUSTOM_FIELD_NAME_PATTERN,
  reservedCustomFieldNameValidator,
} from '../../utils/validation';

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
      name: ['', [Validators.required, Validators.maxLength(100)]],
      content_type: ['image', Validators.required],
      n_max_queued: [0, [Validators.required, Validators.min(0)]],
      create_preview: [true],
      auto_conversion: [''],

      housekeeping: this.fb.group({
        interval_value: [1, [Validators.required, Validators.min(0)]],
        interval_unit: ['h'],
        disk_space_value: [100, [Validators.required, Validators.min(0)]],
        disk_space_unit: ['G'],
        max_age_value: [0, [Validators.required, Validators.min(0)]],
        max_age_unit: ['d'],
      }),
      custom_fields: this.fb.array([]),
    });
  }

  ngOnInit(): void {
    this.createDbForm
      .get('content_type')
      ?.valueChanges.pipe(takeUntil(this.destroy$))
      .subscribe(() => {
        this.createDbForm.get('auto_conversion')?.setValue('');
      });
  }

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
          Validators.pattern(CUSTOM_FIELD_NAME_PATTERN),
          reservedCustomFieldNameValidator,
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
      .pipe(finalize(() => (this.isLoading = false)))
      .subscribe({
        next: () => {
          this.modalService.close(CreateDatabaseModalComponent.MODAL_ID, true);

          this.createDbForm.reset({
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
          });
          this.customFields.clear();
        },
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
