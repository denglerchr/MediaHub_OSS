import { Component, OnDestroy, OnInit, ChangeDetectorRef } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { Subject, firstValueFrom } from 'rxjs';
import { takeUntil, filter, switchMap } from 'rxjs/operators';
import { Database, CustomField, UploadEntryMetadata } from '../../models';
import { EntryService } from '../../services/entry.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { isMimeTypeAllowed, getFileAcceptString } from '../../utils/mime-types';
import { NotificationService } from '../../services/notification.service';
import { AuthService } from '../../services/auth.service';
import { extractMetadata } from '../../utils/metadata-extractor';

@Component({
  selector: 'app-upload-entry-modal',
  templateUrl: './upload-entry-modal.component.html',
  styleUrls: ['./upload-entry-modal.component.css'],
  standalone: false,
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
  private destroy$ = new Subject<void>();

  // N3: which modal session the running upload belongs to. A re-opened modal gets
  // a new generation, so a background upload from a dismissed session can never
  // close (or lock) it.
  private uploadGeneration = 0;

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
    this.databaseService.selectedDatabase$
      .pipe(
        takeUntil(this.destroy$),
        filter((db): db is Database => db !== null),
      )
      .subscribe((db: Database) => {
        this.currentDatabase = db;
        this.initializeForm();
        this.fileAcceptString = getFileAcceptString(db.content_type);
      });

    this.modalService
      .getModalEvents(UploadEntryModalComponent.MODAL_ID)
      .pipe(takeUntil(this.destroy$))
      .subscribe((event) => {
        if (event.action === 'open') {
          // N3: a re-open starts a new modal session. The previous session's
          // upload (if still running) belongs to a dismissed modal now, and the
          // re-opened modal is never mid-upload, so unlock the close button again.
          this.uploadGeneration++;
          this.isLoading = false;
          this.resetProgressState();
          if (event.data?.files && event.data.files.length > 1) {
            this.handleMultipleFiles(event.data.files);
          } else if (event.data?.files && event.data.files.length === 1) {
            this.handleFile(event.data.files[0]);
          } else if (event.data?.file) {
            this.handleFile(event.data.file);
          } else if (event.data?.droppedFiles && event.data.droppedFiles.length > 1) {
            this.handleMultipleFiles(event.data.droppedFiles);
          } else if (event.data?.droppedFiles && event.data.droppedFiles.length === 1) {
            this.handleFile(event.data.droppedFiles[0]);
          } else if (event.data?.droppedFile) {
            this.handleFile(event.data.droppedFile);
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

  private getLocalISOString(date: Date): string {
    const offset = date.getTimezoneOffset();
    const shiftedDate = new Date(date.getTime() - offset * 60 * 1000);
    // FE-005: keep second precision — slicing at 16 truncated the extracted capture
    // timestamp to whole minutes despite the "Extracted capture timestamp" toast.
    return shiftedDate.toISOString().slice(0, 19);
  }

  private initializeForm(): void {
    Object.keys(this.uploadForm.controls).forEach((key) => {
      this.uploadForm.removeControl(key);
    });

    this.uploadForm.addControl(
      'timestamp',
      this.fb.control(this.getLocalISOString(new Date()), Validators.required),
    );
    this.uploadForm.addControl('file', this.fb.control(null, Validators.required));

    this.resetFileState();

    if (this.currentDatabase) {
      this.currentDatabase.custom_fields.forEach((field: CustomField) => {
        if (field.type === 'COORDINATE') {
          this.uploadForm.addControl(
            field.name,
            this.fb.group({
              latitude: [null, [Validators.min(-90), Validators.max(90)]],
              longitude: [null, [Validators.min(-180), Validators.max(180)]],
            }),
          );
        } else {
          const defaultValue = field.type === 'BOOLEAN' ? false : '';
          this.uploadForm.addControl(field.name, this.fb.control(defaultValue));
        }
      });
    }
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
    this.uploadForm.get('timestamp')?.setValue(this.getLocalISOString(new Date()));
    this.uploadForm.get('timestamp')?.updateValueAndValidity();

    this.cdr.detectChanges();

    try {
      const ext = await extractMetadata(file);
      if (ext) {
        if (ext.timestamp) {
          this.singleFileTimestamp = ext.timestamp;
          this.uploadForm.patchValue({ timestamp: this.getLocalISOString(ext.timestamp) });
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
              this.uploadForm.get(cf.name)?.patchValue({
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

    // Destructure to separate the core fields from the dynamic custom fields
    const { timestamp, file, ...rawCustomFields } = this.uploadForm.value;

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

    const custom_fields: Record<string, unknown> = {};

    this.currentDatabase.custom_fields.forEach((field) => {
      if (rawCustomFields.hasOwnProperty(field.name)) {
        let value = rawCustomFields[field.name];

        if (field.type === 'BOOLEAN') {
          value = !!value;
        } else if (
          (field.type === 'INTEGER' || field.type === 'REAL') &&
          value !== '' &&
          value !== null
        ) {
          value = Number(value);
        } else if (field.type === 'COORDINATE') {
          value = sanitizeCoord(value);
        }

        if (value !== null && value !== undefined && value !== '') {
          custom_fields[field.name] = value;
        }
      }
    });

    // Pre-emptively ping the server to ensure our access token is fresh.
    this.authService
      .fetchCurrentUser()
      .pipe(
        switchMap(async (user) => {
          if (!user) {
            throw new Error('User session is invalid or expired.');
          }

          if (this.isMultipleUpload) {
            this.totalUploads = this.selectedFiles.length;
            this.uploadProgressIndex = 0;
            this.uploadProgressPercent = 0;

            // FE-022: continue past failures — one bad file must never discard the
            // rest of the queue. Per-file errors are collected and reported once.
            let okCount = 0;
            const failures: string[] = [];

            for (let i = 0; i < this.selectedFiles.length; i++) {
              const f = this.selectedFiles[i];
              this.uploadProgressIndex = i + 1;
              this.currentUploadingFileName = f.name;
              this.uploadProgressPercent = Math.round((i / this.selectedFiles.length) * 100);
              this.cdr.detectChanges();

              const ext = await extractMetadata(f);
              const ts = ext?.timestamp ? ext.timestamp.getTime() : Date.now();
              const itemCustomFields: Record<string, unknown> = { ...custom_fields };

              if (ext?.coordinates && this.currentDatabase) {
                const coordFields = this.currentDatabase.custom_fields.filter(
                  (cf) => cf.type === 'COORDINATE',
                );
                coordFields.forEach((cf) => {
                  itemCustomFields[cf.name] = ext.coordinates;
                });
              }

              const metadata: UploadEntryMetadata = {
                timestamp: ts,
                filename: f.name,
                custom_fields: itemCustomFields,
              };

              try {
                await firstValueFrom(
                  this.entryService.uploadEntry(this.currentDatabase!.id, metadata, f, {
                    // FE-022: per-file error toasts would be noise — the summary below
                    // reports the outcome of the whole batch exactly once.
                    silentSuccess: true,
                    silentError: true,
                    // FE-032: roll the per-byte progress of this file into the batch bar.
                    onProgress: (percent) => {
                      this.uploadProgressPercent = Math.round(
                        ((i + percent / 100) / this.totalUploads) * 100,
                      );
                      this.cdr.detectChanges();
                    },
                  }),
                );
                okCount++;
              } catch (err) {
                failures.push(`${f.name}: ${err instanceof Error ? err.message : 'upload failed'}`);
              }

              this.uploadProgressPercent = Math.round(((i + 1) / this.selectedFiles.length) * 100);
              this.cdr.detectChanges();
            }

            // FE-022: one summary notification for the batch (success or partial failure).
            if (failures.length > 0) {
              this.notificationService.showError(
                `Uploaded ${okCount} of ${this.totalUploads} entries. Failed — ${failures.join('; ')}`,
              );
            } else {
              this.notificationService.showSuccess(`Successfully uploaded ${okCount} entries.`);
            }
            if (okCount > 0) {
              this.entryService.triggerImageListRefresh();
            }
            return { okCount, failureCount: failures.length };
          } else {
            // FE-005: an untouched timestamp input keeps the extracted capture time to
            // the millisecond (like the multi-upload path above); once the user edits
            // the input, its second-precision value is authoritative.
            const tsControl = this.uploadForm.get('timestamp');
            const ts =
              !tsControl?.dirty && this.singleFileTimestamp
                ? this.singleFileTimestamp.getTime()
                : new Date(timestamp).getTime();
            const metadata: UploadEntryMetadata = {
              timestamp: ts,
              filename: this.selectedFile!.name,
              custom_fields: custom_fields,
            };
            // FE-022: silent service error — the single failure toast is raised by the
            // subscriber below (the service toast + component toast used to stack up).
            // FE-032: per-byte progress for the single-file bar.
            this.uploadProgressPercent = 0;
            await firstValueFrom(
              this.entryService.uploadEntry(
                this.currentDatabase!.id,
                metadata,
                this.selectedFile!,
                {
                  silentError: true,
                  onProgress: (percent) => {
                    this.uploadProgressPercent = percent;
                    this.cdr.detectChanges();
                  },
                },
              ),
            );
            return { okCount: 1, failureCount: 0 };
          }
        }),
      )
      .subscribe({
        next: (result) => {
          // N3: this upload belongs to a dismissed (and possibly re-opened) modal
          // session — never auto-close the modal that is showing now.
          if (generation !== this.uploadGeneration) {
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
          if (generation === this.uploadGeneration) {
            this.isLoading = false;
          }
          this.notificationService.showError(err?.message || 'Upload failed.');
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

  trackByFileName(index: number, file: File): string {
    return file.name;
  }

  trackByFieldId(index: number, field: any): number | string {
    return field.id ?? field.name;
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}
