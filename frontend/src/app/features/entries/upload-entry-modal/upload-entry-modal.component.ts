import { CommonModule } from '@angular/common';
import { Component, OnDestroy, OnInit, ChangeDetectorRef, DestroyRef, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { defer, from, throwError } from 'rxjs';
import { catchError, concatMap, filter, map, switchMap, take, tap, toArray } from 'rxjs/operators';
import { Database, UploadEntryMetadata } from '../../../core/models';
import { EntryService } from '../../../core/services/entry.service';
import { DatabaseService } from '../../../core/services/database.service';
import { ModalService } from '../../../core/services/modal.service';
import { ModalComponent } from '../../../shared/components/modal/modal.component';
import { CustomFieldsFormComponent } from '../custom-fields-form/custom-fields-form.component';
import { FileDragDropDirective } from '../../../shared/directives/file-drag-drop.directive';
import { FormatBytesPipe } from '../../../shared/pipes/format-bytes.pipe';
import { isMimeTypeAllowed, getFileAcceptString } from '../../../shared/utils/mime-types';
import { NotificationService } from '../../../core/services/notification.service';
import { AuthService } from '../../../core/services/auth.service';
import { extractMetadata } from '../../../shared/utils/metadata-extractor';
import {
  buildCustomFieldsFormGroup,
  coerceCustomFieldValues,
} from '../../../shared/utils/custom-fields';
import { parseDatetimeLocalToMs, toDatetimeLocal } from '../../../shared/utils/datetime';
import { extractHttpErrorMessage } from '../../../shared/utils/http-error';

@Component({
  selector: 'app-upload-entry-modal',
  templateUrl: './upload-entry-modal.component.html',
  styleUrls: ['./upload-entry-modal.component.css'],
  standalone: true,
  imports: [
    CommonModule,
    ReactiveFormsModule,
    ModalComponent,
    CustomFieldsFormComponent,
    FileDragDropDirective,
    FormatBytesPipe,
  ],
})
export class UploadEntryModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'uploadEntryModal';
  uploadForm: FormGroup;
  isLoading = false;
  selectedFile: File | null = null;
  public selectedFileName: string | null = null;
  selectedFiles: File[] = [];
  isMultipleUpload = false;
  currentDatabase: Database | null = null;
  // FE-005: capture timestamp extracted from a single file. While the user has not
  // touched the timestamp input it is posted to the millisecond (parity with the
  // multi-upload path at ext.timestamp.getTime()); the datetime-local input can only
  // carry seconds, so round-tripping through it would drop the milliseconds.
  private singleFileTimestamp: Date | null = null;
  private readonly destroyRef = inject(DestroyRef);

  // N3: which modal session the running upload belongs to. A re-opened modal gets
  // a new generation, so a background upload from a dismissed session can never
  // close (or lock) it.
  private uploadGeneration = 0;
  private isDestroyed = false;

  public fileAcceptString: string | null = null;

  // Batch upload progress state
  public uploadProgressIndex = 0;
  public totalUploads = 0;
  public currentUploadingFileName = '';
  public uploadProgressPercent = 0;

  constructor(
    private fb: FormBuilder,
    private databaseService: DatabaseService,
    private entryService: EntryService,
    private modalService: ModalService,
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
    private authService: AuthService,
  ) {
    this.uploadForm = this.fb.group({});
  }

  ngOnInit(): void {
    this.modalService.registerCloseGuard(UploadEntryModalComponent.MODAL_ID, () => !this.isLoading);

    this.databaseService.selectedDatabase$
      .pipe(
        filter((db): db is Database => db !== null),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((db: Database) => {
        this.currentDatabase = db;
        this.initializeForm();
        this.fileAcceptString = getFileAcceptString(db.content_type);
      });

    this.modalService
      .getModalEvents(UploadEntryModalComponent.MODAL_ID)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((event) => {
        if (event.action === 'open') {
          // N3: a re-open starts a new modal session. The previous session's
          // upload (if still running) belongs to a dismissed modal now, and the
          // re-opened modal is never mid-upload, so unlock the close button again.
          this.uploadGeneration++;
          this.isLoading = false;
          this.resetProgressState();
          const files = event.data?.files;
          if (files && files.length > 1) {
            this.handleMultipleFiles(files);
          } else if (files && files.length === 1) {
            this.handleFile(files[0]);
          } else {
            this.resetFileState();
          }
        }
      });
  }

  private resetProgressState(): void {
    this.uploadProgressIndex = 0;
    this.totalUploads = 0;
    this.currentUploadingFileName = '';
    this.uploadProgressPercent = 0;
  }

  private initializeForm(): void {
    Object.keys(this.uploadForm.controls).forEach((key) => {
      this.uploadForm.removeControl(key);
    });

    this.uploadForm.addControl(
      'timestamp',
      this.fb.control(toDatetimeLocal(new Date()), Validators.required),
    );
    this.uploadForm.addControl('file', this.fb.control(null, Validators.required));

    const customFieldsGroup = this.currentDatabase
      ? buildCustomFieldsFormGroup(this.fb, this.currentDatabase.custom_fields)
      : this.fb.group({});
    this.uploadForm.addControl('custom_fields', customFieldsGroup);

    this.resetFileState();
  }

  private resetFileState(): void {
    this.selectedFile = null;
    this.selectedFileName = null;
    this.selectedFiles = [];
    this.isMultipleUpload = false;
    this.singleFileTimestamp = null;
    this.resetProgressState();
    this.uploadForm.get('file')?.setValue(null, { emitEvent: false });
    this.uploadForm.get('timestamp')?.setValidators([Validators.required]);
    this.uploadForm.get('timestamp')?.updateValueAndValidity();
  }

  onFileSelected(event: Event): void {
    const element = event.currentTarget as HTMLInputElement;
    const fileList: FileList | null = element.files;

    if (fileList && fileList.length > 0) {
      const file = fileList[0];
      this.handleFile(file);
    }
  }

  onMultipleFilesSelected(event: Event): void {
    const element = event.currentTarget as HTMLInputElement;
    const fileList: FileList | null = element.files;
    if (fileList && fileList.length > 0) {
      // FE-022: validation lives in handleMultipleFiles (single source of truth).
      this.handleMultipleFiles(Array.from(fileList));
    }
  }

  onFilesDropped(files: File[]): void {
    if (this.isLoading || files.length === 0) {
      return;
    }
    if (files.length > 1) {
      this.handleMultipleFiles(files);
    } else {
      this.handleFile(files[0]);
    }
  }

  async handleFile(file: File | null): Promise<void> {
    if (!file) {
      this.resetFileState();
      return;
    }

    if (this.currentDatabase && !isMimeTypeAllowed(this.currentDatabase.content_type, file.type)) {
      this.notificationService.showError(
        `Invalid file type (${file.type}). Allowed: ${this.currentDatabase.content_type}`,
      );
      return;
    }

    this.selectedFile = file;
    this.selectedFileName = file.name;
    this.selectedFiles = [];
    this.isMultipleUpload = false;
    this.singleFileTimestamp = null;

    this.uploadForm.patchValue({ file: this.selectedFile });

    this.uploadForm.get('file')?.markAsTouched();
    this.uploadForm.get('file')?.updateValueAndValidity();

    this.uploadForm.get('timestamp')?.setValidators([Validators.required]);
    this.uploadForm.get('timestamp')?.setValue(toDatetimeLocal(new Date()));
    this.uploadForm.get('timestamp')?.markAsPristine();
    this.uploadForm.get('timestamp')?.updateValueAndValidity();

    this.cdr.detectChanges();

    try {
      const ext = await extractMetadata(file);
      if (this.selectedFile !== file || this.isDestroyed) {
        return;
      }
      if (ext) {
        if (ext.timestamp) {
          this.singleFileTimestamp = ext.timestamp;
          this.uploadForm.patchValue({ timestamp: toDatetimeLocal(ext.timestamp) });
          this.notificationService.showSuccess(
            `Extracted capture timestamp from file: ${ext.timestamp.toLocaleString()}`,
          );
        }
        if (ext.coordinates && this.currentDatabase) {
          const coordFields = this.currentDatabase.custom_fields.filter(
            (cf) => cf.type === 'COORDINATE',
          );
          if (coordFields.length > 0) {
            coordFields.forEach((cf) => {
              this.uploadForm.get(['custom_fields', cf.name])?.patchValue({
                latitude: ext.coordinates!.latitude,
                longitude: ext.coordinates!.longitude,
              });
            });
            this.notificationService.showSuccess(
              `Extracted GPS coordinates (${ext.coordinates.latitude.toFixed(6)}, ${ext.coordinates.longitude.toFixed(6)}) from file.`,
            );
          }
        }
        this.cdr.detectChanges();
      }
    } catch (err) {
      console.error('[UploadModal] Error auto-extracting metadata:', err);
    }
  }

  handleMultipleFiles(files: File[]): void {
    // FE-022: MIME validation belongs here — a multi-drop used to bypass it
    // entirely (only handleFile and onMultipleFilesSelected checked) and shipped
    // invalid types to the backend, which answered 415 and (via the
    // abort-on-first-error bug) discarded the rest of the queue.
    const validFiles = this.currentDatabase
      ? files.filter((f) => isMimeTypeAllowed(this.currentDatabase!.content_type, f.type))
      : files;

    if (validFiles.length === 0) {
      this.notificationService.showError(
        `No valid files selected. Allowed types: ${this.currentDatabase?.content_type ?? 'any'}`,
      );
      this.resetFileState();
      return;
    }
    if (validFiles.length < files.length) {
      this.notificationService.showInfo(
        `Skipped ${files.length - validFiles.length} file(s) of invalid type.`,
      );
    }
    if (validFiles.length === 1) {
      // A single remaining file is a single upload (timestamp input stays active).
      this.handleFile(validFiles[0]);
      return;
    }

    this.selectedFiles = validFiles;
    this.isMultipleUpload = true;
    this.selectedFile = null;
    this.selectedFileName = null;

    this.uploadForm.patchValue({ file: validFiles[0] });
    this.uploadForm.get('file')?.updateValueAndValidity();

    this.uploadForm.get('timestamp')?.clearValidators();
    this.uploadForm.get('timestamp')?.updateValueAndValidity();

    this.cdr.detectChanges();
  }

  onSubmit(): void {
    if (this.isLoading) return;

    if (
      this.uploadForm.invalid ||
      !this.currentDatabase ||
      (!this.selectedFile && this.selectedFiles.length === 0)
    ) {
      this.uploadForm.markAllAsTouched();
      return;
    }

    this.isLoading = true;

    // N3: capture the modal session this upload belongs to.
    const generation = this.uploadGeneration;

    // Snapshot mutable session inputs before any async step so dismissing and
    // re-opening the modal cannot mutate the array, null out singleFile, or swap db.
    const db = this.currentDatabase;
    const isMulti = this.isMultipleUpload;
    const filesToUpload = [...this.selectedFiles];
    const singleFile = this.selectedFile;
    const singleTimestamp = this.singleFileTimestamp;
    const tsDirty = !!this.uploadForm.get('timestamp')?.dirty;
    const timestamp = this.uploadForm.value.timestamp;

    const rawCustomFields = (this.uploadForm.value.custom_fields || {}) as Record<string, unknown>;
    const custom_fields = coerceCustomFieldValues(db.custom_fields, rawCustomFields, {
      includeEmptyAsNull: false,
    });

    // Pre-emptively ping the server to ensure our access token is fresh.
    this.authService
      .fetchCurrentUser()
      .pipe(
        switchMap((user) => {
          if (!user) {
            return throwError(() => new Error('User session is invalid or expired.'));
          }

          if (isMulti) {
            if (generation === this.uploadGeneration && !this.isDestroyed) {
              this.totalUploads = filesToUpload.length;
              this.uploadProgressIndex = 0;
              this.uploadProgressPercent = 0;
            }

            // FE-022: continue past failures — one bad file must never discard the
            // rest of the queue. Per-file errors are collected and reported once.
            return from(filesToUpload.map((file, i) => ({ file, i }))).pipe(
              concatMap(({ file, i }) => {
                if (generation === this.uploadGeneration && !this.isDestroyed) {
                  this.uploadProgressIndex = i + 1;
                  this.currentUploadingFileName = file.name;
                  this.uploadProgressPercent = Math.round((i / filesToUpload.length) * 100);
                  this.cdr.detectChanges();
                }

                return defer(() => extractMetadata(file)).pipe(
                  switchMap((ext) => {
                    const ts = ext?.timestamp ? ext.timestamp.getTime() : Date.now();
                    const itemCustomFields: Record<string, unknown> = { ...custom_fields };

                    if (ext?.coordinates) {
                      const coordFields = db.custom_fields.filter((cf) => cf.type === 'COORDINATE');
                      coordFields.forEach((cf) => {
                        itemCustomFields[cf.name] = ext.coordinates;
                      });
                    }

                    const metadata: UploadEntryMetadata = {
                      timestamp: ts,
                      filename: file.name,
                      custom_fields: itemCustomFields,
                    };

                    return this.entryService
                      .uploadEntry(db.id, metadata, file, {
                        // FE-022: per-file error toasts would be noise — the summary below
                        // reports the outcome of the whole batch exactly once.
                        silentSuccess: true,
                        silentError: true,
                        // FE-032: roll the per-byte progress of this file into the batch bar.
                        onProgress: (percent) => {
                          if (generation === this.uploadGeneration && !this.isDestroyed) {
                            const nextPercent = Math.round(
                              ((i + percent / 100) / filesToUpload.length) * 100,
                            );
                            if (nextPercent !== this.uploadProgressPercent) {
                              this.uploadProgressPercent = nextPercent;
                              this.cdr.detectChanges();
                            }
                          }
                        },
                      })
                      .pipe(
                        take(1),
                        map(() => ({ ok: true as const })),
                        catchError((err) =>
                          extractHttpErrorMessage(err, { fallback: 'upload failed' }).pipe(
                            take(1),
                            map((msg) => ({ ok: false as const, error: `${file.name}: ${msg}` })),
                          ),
                        ),
                      );
                  }),
                  tap(() => {
                    if (generation === this.uploadGeneration && !this.isDestroyed) {
                      this.uploadProgressPercent = Math.round(
                        ((i + 1) / filesToUpload.length) * 100,
                      );
                      this.cdr.detectChanges();
                    }
                  }),
                );
              }),
              toArray(),
              map((outcomes) => {
                const okCount = outcomes.filter((o) => o.ok).length;
                const failures = outcomes
                  .filter((o): o is { ok: false; error: string } => !o.ok)
                  .map((o) => o.error);

                // FE-022: one summary notification for the batch (success or partial failure).
                if (failures.length > 0) {
                  this.notificationService.showError(
                    `Uploaded ${okCount} of ${filesToUpload.length} entries. Failed — ${failures.join('; ')}`,
                  );
                } else {
                  this.notificationService.showSuccess(`Successfully uploaded ${okCount} entries.`);
                }
                if (okCount > 0) {
                  this.entryService.triggerImageListRefresh();
                }
                return { okCount, failureCount: failures.length };
              }),
            );
          } else {
            // FE-005: an untouched timestamp input keeps the extracted capture time to
            // the millisecond (like the multi-upload path above); once the user edits
            // the input, its second-precision value is authoritative.
            const ts =
              !tsDirty && singleTimestamp
                ? singleTimestamp.getTime()
                : (parseDatetimeLocalToMs(timestamp) ?? Date.now());
            const metadata: UploadEntryMetadata = {
              timestamp: ts,
              filename: singleFile!.name,
              custom_fields: custom_fields,
            };
            // FE-022: silent service error — the single failure toast is raised by the
            // subscriber below (the service toast + component toast used to stack up).
            // FE-032: per-byte progress for the single-file bar.
            if (generation === this.uploadGeneration && !this.isDestroyed) {
              this.uploadProgressPercent = 0;
            }
            return this.entryService
              .uploadEntry(db.id, metadata, singleFile!, {
                silentError: true,
                onProgress: (percent) => {
                  if (generation === this.uploadGeneration && !this.isDestroyed) {
                    if (percent !== this.uploadProgressPercent) {
                      this.uploadProgressPercent = percent;
                      this.cdr.detectChanges();
                    }
                  }
                },
              })
              .pipe(
                take(1),
                map(() => ({ okCount: 1, failureCount: 0 })),
              );
          }
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: (result) => {
          // N3: this upload belongs to a dismissed (and possibly re-opened) modal
          // session — never auto-close the modal that is showing now.
          if (generation !== this.uploadGeneration || this.isDestroyed) {
            return;
          }
          this.isLoading = false;
          this.resetProgressState();
          // FE-022: after a *total* batch failure keep the modal open so the queue can
          // be retried (nothing was created, so a retry cannot duplicate entries).
          if (result.okCount === 0 && result.failureCount > 0) {
            return;
          }
          this.closeModal();
        },
        error: (err) => {
          // N3: report the failure no matter which modal session is showing now, but
          // only the owning session may touch the modal's state.
          if (generation === this.uploadGeneration && !this.isDestroyed) {
            this.isLoading = false;
          }
          extractHttpErrorMessage(err, { fallback: 'Upload failed.' })
            .pipe(take(1), takeUntilDestroyed(this.destroyRef))
            .subscribe((msg) => this.notificationService.showError(msg));
        },
      });
  }

  closeModal(): void {
    if (this.isLoading) return;
    // FE-019: id-anchored — a background upload completing must never dismiss an
    // unrelated modal the user opened meanwhile.
    this.modalService.close(UploadEntryModalComponent.MODAL_ID);
    if (this.currentDatabase) {
      this.initializeForm();
    }
  }

  get customFieldsGroup(): FormGroup {
    return this.uploadForm.get('custom_fields') as FormGroup;
  }

  trackByFileName(index: number, file: File): string {
    return `${index}-${file.name}-${file.size}`;
  }

  ngOnDestroy(): void {
    this.isDestroyed = true;
    this.modalService.unregisterCloseGuard(UploadEntryModalComponent.MODAL_ID);
  }
}
