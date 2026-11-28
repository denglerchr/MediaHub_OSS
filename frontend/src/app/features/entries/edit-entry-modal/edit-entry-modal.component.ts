import { CommonModule } from '@angular/common';
import { Component, DestroyRef, OnDestroy, OnInit, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { filter, finalize } from 'rxjs/operators';
import { Database, Entry } from '../../../core/models';
import { EntryService } from '../../../core/services/entry.service';
import { DatabaseService } from '../../../core/services/database.service';
import { ModalService } from '../../../core/services/modal.service';
import { ModalComponent } from '../../../shared/components/modal/modal.component';
import { CustomFieldsFormComponent } from '../custom-fields-form/custom-fields-form.component';
import {
  buildCustomFieldsFormGroup,
  coerceCustomFieldValues,
} from '../../../shared/utils/custom-fields';
import { parseDatetimeLocalToMs, toDatetimeLocal } from '../../../shared/utils/datetime';

@Component({
  selector: 'app-edit-entry-modal',
  templateUrl: './edit-entry-modal.component.html',
  styleUrls: ['./edit-entry-modal.component.css'],
  standalone: true,
  imports: [CommonModule, ReactiveFormsModule, ModalComponent, CustomFieldsFormComponent],
})
export class EditEntryModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'editEntryModal';
  editForm: FormGroup;
  isLoading = false;

  public currentDatabase: Database | null = null;
  public currentEntry: Entry | null = null;
  private sessionGeneration = 0;
  private readonly destroyRef = inject(DestroyRef);

  constructor(
    private fb: FormBuilder,
    private databaseService: DatabaseService,
    private entryService: EntryService,
    private modalService: ModalService,
  ) {
    this.editForm = this.fb.group({
      timestamp: ['', Validators.required],
      filename: [''],
      custom_fields: this.fb.group({}),
    });
  }

  ngOnInit(): void {
    this.modalService.registerCloseGuard(EditEntryModalComponent.MODAL_ID, () => !this.isLoading);

    this.modalService
      .getModalEvents(EditEntryModalComponent.MODAL_ID)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((event) => {
        if (event.action === 'open' || event.action === 'close') {
          this.sessionGeneration++;
          if (event.action === 'open') {
            this.isLoading = false;
          }
        }
      });

    this.databaseService.selectedDatabase$
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        filter((db): db is Database => !!db),
      )
      .subscribe((db) => {
        if (
          this.currentEntry &&
          this.currentEntry.database_id &&
          this.currentEntry.database_id !== db.id
        ) {
          this.currentEntry = null;
        }
        this.currentDatabase = db;
        this.initializeForm(db);

        if (this.currentEntry) {
          this.patchForm(this.currentEntry);
        }
      });

    this.entryService.selectedEntry$
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((entry) => {
        if (
          !entry ||
          !this.currentDatabase ||
          (entry.database_id && entry.database_id !== this.currentDatabase.id)
        ) {
          this.currentEntry = null;
          return;
        }
        this.currentEntry = entry;
        this.patchForm(entry);
      });
  }

  /**
   * Dynamically builds nested form controls based on the database's schema.
   */
  private initializeForm(db: Database): void {
    this.editForm.setControl(
      'custom_fields',
      buildCustomFieldsFormGroup(this.fb, db.custom_fields),
    );
  }

  /**
   * Populates the form with data from the selected entry.
   *
   * FE-003: use reset() with a value covering every schema field. patchValue() only
   * overwrites the keys it receives, so fields unset on this entry kept the values of
   * the previously edited entry and onSubmit() then wrote those stale values into it.
   */
  private patchForm(entry: Entry): void {
    if (!this.editForm || !this.currentDatabase) return;

    const localDateTime = toDatetimeLocal(entry.timestamp);

    const customFields = buildCustomFieldsFormGroup(
      this.fb,
      this.currentDatabase.custom_fields,
      entry.custom_fields,
    ).getRawValue() as Record<string, unknown>;
    for (const field of this.currentDatabase.custom_fields) {
      if (field.type === 'TEXT' && customFields[field.name] === null) {
        customFields[field.name] = '';
      }
    }

    this.editForm.reset({
      timestamp: localDateTime,
      filename: entry.filename ?? '',
      custom_fields: customFields,
    });
  }

  onSubmit(): void {
    if (this.isLoading || this.editForm.invalid || !this.currentDatabase || !this.currentEntry) {
      return;
    }

    const gen = this.sessionGeneration;
    this.isLoading = true;
    const formValue = this.editForm.getRawValue();

    // FE-004: omit blank numeric fields when already unset, but preserve explicit null
    // when clearing an existing value so the backend resets the column to SQL NULL.
    const customFields = coerceCustomFieldValues(
      this.currentDatabase.custom_fields,
      (formValue.custom_fields || {}) as Record<string, unknown>,
      { includeEmptyAsNull: true },
    );
    for (const field of this.currentDatabase.custom_fields) {
      if (
        (field.type === 'INTEGER' || field.type === 'REAL') &&
        customFields[field.name] === null &&
        this.currentEntry.custom_fields?.[field.name] == null
      ) {
        delete customFields[field.name];
      }
    }

    // Build the clean update payload without media_fields.
    // FE-005: the backend leaves `timestamp` untouched when it is absent from the
    // PATCH (internal/httpserver/entryhandler/entries.go PatchEntry), so only send it
    // when the user actually changed it — an unrelated edit must not truncate the
    // stored capture time (the datetime-local input cannot carry full precision).
    // FE-042: typed update payload instead of `any`.
    const updates: Partial<Entry> & { custom_fields: Record<string, unknown> } = {
      filename: formValue.filename,
      custom_fields: customFields,
    };
    if (this.editForm.get('timestamp')?.dirty) {
      const parsedTimestamp = parseDatetimeLocalToMs(formValue.timestamp);
      if (parsedTimestamp !== null) {
        updates.timestamp = parsedTimestamp;
      }
    }

    this.entryService
      .updateEntry(this.currentDatabase.id, this.currentEntry.id, updates)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => {
          if (gen === this.sessionGeneration) {
            this.isLoading = false;
          }
        }),
      )
      .subscribe({
        next: () => {
          if (gen === this.sessionGeneration) {
            this.isLoading = false;
            this.modalService.close(EditEntryModalComponent.MODAL_ID, true);
          }
        },
        error: () => {},
      });
  }

  get customFieldsGroup(): FormGroup {
    return this.editForm.get('custom_fields') as FormGroup;
  }

  closeModal(): void {
    if (this.isLoading) return;
    this.modalService.close(EditEntryModalComponent.MODAL_ID, false);
  }

  ngOnDestroy(): void {
    this.modalService.unregisterCloseGuard(EditEntryModalComponent.MODAL_ID);
  }
}
