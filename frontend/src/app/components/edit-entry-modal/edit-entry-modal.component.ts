import { Component, OnDestroy, OnInit } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { Subject } from 'rxjs';
import { takeUntil, filter, finalize } from 'rxjs/operators';
import { Database, Entry } from '../../models';
import { EntryService } from '../../services/entry.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';

@Component({
  selector: 'app-edit-entry-modal',
  templateUrl: './edit-entry-modal.component.html',
  styleUrls: ['./edit-entry-modal.component.css'],
  standalone: false,
})
export class EditEntryModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'editEntryModal';
  editForm: FormGroup;
  isLoading = false;

  public currentDatabase: Database | null = null;
  public currentEntry: Entry | null = null;
  private destroy$ = new Subject<void>();

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
    this.databaseService.selectedDatabase$
      .pipe(
        takeUntil(this.destroy$),
        filter((db): db is Database => !!db),
      )
      .subscribe((db) => {
        this.currentDatabase = db;
        this.initializeForm(db);

        if (this.currentEntry) {
          this.patchForm(this.currentEntry);
        }
      });

    this.entryService.selectedEntry$
      .pipe(
        takeUntil(this.destroy$),
        filter((entry): entry is Entry => !!entry && !!this.currentDatabase),
      )
      .subscribe((entry) => {
        this.currentEntry = entry;
        this.patchForm(entry);
      });
  }

  private getLocalISOString(date: Date): string {
    const offset = date.getTimezoneOffset();
    const shiftedDate = new Date(date.getTime() - offset * 60 * 1000);
    // FE-005: keep second precision — slicing at 16 truncated every saved timestamp
    // to whole minutes and destroyed the capture-time seconds on unrelated edits.
    return shiftedDate.toISOString().slice(0, 19);
  }

  /**
   * Dynamically builds nested form controls based on the database's schema.
   */
  private initializeForm(db: Database): void {
    const customGroup = this.fb.group({});

    // Add custom fields
    db.custom_fields.forEach((field) => {
      if (field.type === 'COORDINATE') {
        customGroup.addControl(
          field.name,
          this.fb.group({
            latitude: [null, [Validators.min(-90), Validators.max(90)]],
            longitude: [null, [Validators.min(-180), Validators.max(180)]],
          }),
        );
      } else {
        const defaultValue = field.type === 'BOOLEAN' ? false : '';
        customGroup.addControl(field.name, this.fb.control(defaultValue));
      }
    });

    this.editForm.setControl('custom_fields', customGroup);
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

    const localDateTime = this.getLocalISOString(new Date(entry.timestamp));

    const customFields: Record<string, unknown> = {};
    this.currentDatabase.custom_fields.forEach((field) => {
      const raw = entry.custom_fields ? entry.custom_fields[field.name] : undefined;
      if (field.type === 'COORDINATE') {
        customFields[field.name] =
          raw && typeof raw === 'object'
            ? { latitude: raw.latitude ?? null, longitude: raw.longitude ?? null }
            : { latitude: null, longitude: null };
      } else if (field.type === 'BOOLEAN') {
        customFields[field.name] = raw === 1 || raw === true;
      } else {
        customFields[field.name] = raw ?? (field.type === 'TEXT' ? '' : null);
      }
    });

    this.editForm.reset({
      timestamp: localDateTime,
      filename: entry.filename ?? '',
      custom_fields: customFields,
    });
  }

  onSubmit(): void {
    if (this.editForm.invalid || !this.currentDatabase || !this.currentEntry) {
      return;
    }

    this.isLoading = true;
    const formValue = this.editForm.getRawValue();

    // FE-042: typed coordinate sanitizer (was `(val: any)`).
    const sanitizeCoord = (val: unknown): { latitude: number; longitude: number } | null => {
      if (!val || typeof val !== 'object') return null;
      const { latitude: lat, longitude: lng } = val as { latitude?: unknown; longitude?: unknown };
      if (
        lat !== null &&
        lat !== '' &&
        lat !== undefined &&
        lng !== null &&
        lng !== '' &&
        lng !== undefined
      ) {
        const numLat = Number(lat);
        const numLng = Number(lng);
        if (!isNaN(numLat) && !isNaN(numLng)) {
          return { latitude: numLat, longitude: numLng };
        }
      }
      return null;
    };

    // Build the clean update payload without media_fields.
    // FE-005: the backend leaves `timestamp` untouched when it is absent from the
    // PATCH (internal/httpserver/entryhandler/entries.go PatchEntry), so only send it
    // when the user actually changed it — an unrelated edit must not truncate the
    // stored capture time (the datetime-local input cannot carry full precision).
    // FE-042: typed update payload instead of `any`.
    const customFields: Record<string, unknown> = {};
    const updates: Partial<Entry> & { custom_fields: Record<string, unknown> } = {
      filename: formValue.filename,
      custom_fields: customFields,
    };
    if (this.editForm.get('timestamp')?.dirty) {
      updates.timestamp = new Date(formValue.timestamp).getTime();
    }

    // Ensure correct data types for the backend
    this.currentDatabase.custom_fields.forEach((field) => {
      const val = formValue.custom_fields[field.name];
      if (field.type === 'BOOLEAN') {
        customFields[field.name] = !!val;
      } else if (field.type === 'INTEGER' || field.type === 'REAL') {
        // FE-004: omit blank numeric fields. Sending "" makes the backend reject the
        // whole request (validateCustomFields requires a number) and broke every edit.
        if (val === '' || val === null || val === undefined) {
          return;
        }
        const num = Number(val);
        if (Number.isFinite(num)) {
          customFields[field.name] = num;
        }
      } else if (field.type === 'COORDINATE') {
        customFields[field.name] = sanitizeCoord(val);
      } else {
        customFields[field.name] = val ?? '';
      }
    });

    this.entryService
      .updateEntry(this.currentDatabase.id, this.currentEntry.id, updates)
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => (this.isLoading = false)),
      )
      .subscribe(() => {
        this.modalService.close(EditEntryModalComponent.MODAL_ID, true);
      });
  }

  closeModal(): void {
    this.modalService.close(EditEntryModalComponent.MODAL_ID, false);
  }

  trackByFieldId(index: number, field: any): number | string {
    return field.id ?? field.name;
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}
