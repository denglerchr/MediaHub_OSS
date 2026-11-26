import { Component, OnInit, OnDestroy, ChangeDetectorRef } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';
import { FormBuilder, FormGroup, Validators, FormControl } from '@angular/forms';
import { HttpEvent, HttpEventType, HttpResponse } from '@angular/common/http';
import { Subject, Subscription } from 'rxjs';
import { takeUntil } from 'rxjs/operators';

import { Database } from '../../models';
import { DatabaseService } from '../../services/database.service';
import { EntryService } from '../../services/entry.service';
import { NotificationService } from '../../services/notification.service';
import { AuthService } from '../../services/auth.service';
import { extractEntriesCsvHeaderLine } from '../../utils/zip-csv-header';

@Component({
  selector: 'app-import-page',
  templateUrl: './import-page.component.html',
  styleUrls: ['./import-page.component.css'],
  standalone: false,
})
export class ImportPageComponent implements OnInit, OnDestroy {
  public currentDatabase: Database | null = null;
  public dbLoadError = false;
  private destroy$ = new Subject<void>();
  private uploadSub?: Subscription;
  private isDestroyed = false;

  // --- Native Stepper State ---
  public currentStep = 1;

  // Forms
  public configStepForm: FormGroup;
  public mappingStepForm: FormGroup;

  // File & CSV State
  public selectedFile: File | null = null;
  public isParsingZip = false;
  public customCsvHeaders: string[] = [];

  // FE-050: exactly the CSV columns the backend parses as standard fields
  // (validateCSVHeaders, entryhandler/utils_import.go:126). The old list also hid
  // `created_at`, `updated_at` and `preview_filesize` — but
  // mapCustomFields (entryhandler/utils_import.go:292) treats every column after the
  // seven standard ones as *custom* unless it is a media field, so with
  // unmapped_fields:'fail' the import aborted right after the "Upload Successful!"
  // toast on archives carrying those columns. They are now visible in the mapping
  // step (map them to a custom field or let the config decide). Standard header
  // order matches validateCSVHeaders (utils_import.go:126).
  private readonly STANDARD_HEADERS = [
    'id',
    'filename',
    'timestamp',
    'filesize',
    'previewsize',
    'mime_type',
    'status',
  ];

  // FE-050/N-D7: the media columns can never be custom fields (reserved — FE-049),
  // but Go only skips the ones for the *database's content type*
  // (media.GetMetadataFields(db.ContentType), utils_import.go:282). Hiding
  // width/height/duration/channels for every database left e.g. a `width` CSV
  // column invisible in the mapping step on an audio DB while the backend still
  // counted it as an unmapped custom column — aborting the import under
  // unmapped_fields:'fail'. Mirrors media.GetMetadataFields (internal/media/utils.go).
  private static readonly MEDIA_HEADERS_BY_CONTENT_TYPE: Record<string, readonly string[]> = {
    image: ['width', 'height'],
    video: ['width', 'height', 'duration'],
    audio: ['duration', 'channels'],
    file: [],
  };

  // Upload State
  public isUploading = false;
  public uploadProgress = 0;
  public isUploadComplete = false;

  constructor(
    private route: ActivatedRoute,
    private router: Router,
    private fb: FormBuilder,
    private databaseService: DatabaseService,
    private entryService: EntryService,
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
    private authService: AuthService,
  ) {
    this.configStepForm = this.fb.group({
      mode: ['generate_new', Validators.required],
      unmapped_fields: ['ignore', Validators.required],
    });

    this.mappingStepForm = this.fb.group({});
  }

  ngOnInit(): void {
    const dbId = this.route.snapshot.paramMap.get('id');
    if (dbId) {
      this.dbLoadError = false;
      this.databaseService
        .selectDatabase(dbId)
        .pipe(takeUntil(this.destroy$))
        .subscribe({
          next: (db) => {
            this.currentDatabase = db;
            this.dbLoadError = !db;
            this.cdr.markForCheck();
          },
          error: () => {
            this.currentDatabase = null;
            this.dbLoadError = true;
            this.cdr.markForCheck();
          },
        });
    } else {
      this.dbLoadError = true;
    }
  }

  // --- Stepper Navigation ---

  public nextStep(): void {
    this.currentStep++;
  }

  public prevStep(): void {
    this.currentStep--;
  }

  // --- File Handling ---

  /**
   * Safely handles the raw browser event from the <input type="file"> element.
   */
  public onFileInputChange(event: Event): void {
    const element = event.target as HTMLInputElement;
    const fileList: FileList | null = element.files;

    if (fileList && fileList.length > 0) {
      this.onFileSelected(fileList[0]);
    }

    element.value = '';
  }

  public async onFileSelected(file: File): Promise<void> {
    if (!file.name.toLowerCase().endsWith('.zip')) {
      this.notificationService.showError('Please select a valid .zip archive.');
      return;
    }

    this.selectedFile = file;
    this.isParsingZip = true;
    if (!this.isDestroyed) {
      this.cdr.detectChanges();
    }

    try {
      const csvHeaderLine = await extractEntriesCsvHeaderLine(file);
      if (this.isDestroyed) {
        return;
      }

      if (csvHeaderLine === null) {
        this.notificationService.showError(
          'The archive does not contain an entries.csv file in the root folder.',
        );
        this.resetFile();
        return;
      }

      await this.extractCsvHeaders(csvHeaderLine);
    } catch (err) {
      // FE-042: `unknown` instead of `any`.
      console.error('Failed to parse ZIP:', err);
      if (!this.isDestroyed) {
        this.notificationService.showError(
          'Failed to read the ZIP archive. It might be corrupted.',
        );
        this.resetFile();
      }
    }
  }

  public resetFile(): void {
    this.selectedFile = null;
    this.isParsingZip = false;
    this.customCsvHeaders = [];
    this.currentStep = 1;
    if (!this.isDestroyed) {
      this.cdr.detectChanges();
    }
  }

  private async extractCsvHeaders(csvText: string): Promise<void> {
    const cleanedCsvText = csvText.replace(/^\uFEFF/, '');
    const PapaModule = await import('papaparse');
    if (this.isDestroyed) {
      return;
    }
    const Papa = PapaModule.default;

    Papa.parse(cleanedCsvText, {
      header: true,
      preview: 1,
      skipEmptyLines: true,
      complete: (results) => {
        if (this.isDestroyed) {
          return;
        }
        const allHeaders = results.meta.fields || [];
        const lowerHeaders = allHeaders.map((h) => h.toLowerCase());

        const hasValidStandardOrder =
          allHeaders.length >= this.STANDARD_HEADERS.length &&
          this.STANDARD_HEADERS.every((expected, idx) => lowerHeaders[idx] === expected);
        if (!hasValidStandardOrder) {
          this.notificationService.showError(
            `Invalid entries.csv header format. Expected first columns: ${this.STANDARD_HEADERS.join(', ')}.`,
          );
          this.resetFile();
          return;
        }

        const renamedHeaders = (results.meta as { renamedHeaders?: Record<string, string> })
          .renamedHeaders;
        if (renamedHeaders && Object.keys(renamedHeaders).length > 0) {
          const firstDuplicate = Object.values(renamedHeaders)[0];
          this.notificationService.showError(
            `Invalid entries.csv: duplicate column header "${firstDuplicate}".`,
          );
          this.resetFile();
          return;
        }

        const seenHeaders = new Set<string>();
        const duplicateHeader = allHeaders.find((h) => {
          const lower = h.toLowerCase();
          if (seenHeaders.has(lower)) {
            return true;
          }
          seenHeaders.add(lower);
          return false;
        });
        if (duplicateHeader !== undefined) {
          this.notificationService.showError(
            `Invalid entries.csv: duplicate column header "${duplicateHeader}".`,
          );
          this.resetFile();
          return;
        }

        const hiddenHeaders = this.hiddenCsvHeaders();
        this.customCsvHeaders = allHeaders.filter(
          (header) => !hiddenHeaders.includes(header.toLowerCase()),
        );

        this.buildMappingForm();
        this.isParsingZip = false;
        if (!this.isDestroyed) {
          this.cdr.detectChanges();
        }
      },
    });
  }

  /**
   * FE-050/N-D7: the CSV columns that can never become custom fields — the seven
   * standard fields plus the media fields of this database's content type
   * (mirror of Go's mapCustomFields, utils_import.go:282).
   */
  private hiddenCsvHeaders(): string[] {
    const mediaHeaders = this.currentDatabase
      ? (ImportPageComponent.MEDIA_HEADERS_BY_CONTENT_TYPE[this.currentDatabase.content_type] ?? [])
      : [];
    return [...this.STANDARD_HEADERS, ...mediaHeaders];
  }

  private buildMappingForm(): void {
    Object.keys(this.mappingStepForm.controls).forEach((key) => {
      this.mappingStepForm.removeControl(key);
    });

    this.customCsvHeaders.forEach((csvHeader) => {
      this.mappingStepForm.addControl(csvHeader, new FormControl(''));
    });
  }

  // --- Upload Logic ---

  public startImport(): void {
    if (!this.currentDatabase || !this.selectedFile) return;

    const custom_field_mapping: Record<string, string> = {};
    const mappingValues = this.mappingStepForm.value as Record<string, string>;
    const mappedTargets = new Set<string>();

    for (const csvHeader of Object.keys(mappingValues)) {
      const target = mappingValues[csvHeader];
      if (target) {
        if (mappedTargets.has(target)) {
          this.notificationService.showError(
            `Multiple CSV columns cannot be mapped to the same custom field ("${target}").`,
          );
          return;
        }
        mappedTargets.add(target);
        custom_field_mapping[csvHeader] = target;
      }
    }

    this.isUploading = true;
    this.uploadProgress = 0;

    const config = {
      mode: this.configStepForm.value.mode,
      unmapped_fields: this.configStepForm.value.unmapped_fields,
      custom_field_mapping: custom_field_mapping,
    };

    this.uploadSub?.unsubscribe();
    this.uploadSub = this.entryService
      .importEntries(this.currentDatabase.id, this.selectedFile, config)
      .pipe(takeUntil(this.destroy$))
      .subscribe({
        next: (event: HttpEvent<unknown>) => {
          if (event.type === HttpEventType.UploadProgress && event.total) {
            this.uploadProgress = Math.round((100 * event.loaded) / event.total);
            this.cdr.markForCheck();
          } else if (event instanceof HttpResponse) {
            this.isUploading = false;
            this.isUploadComplete = true;
            this.notificationService.showSuccess('Archive uploaded successfully!');
            this.cdr.markForCheck();
          }
        },
        error: (err) => {
          this.isUploading = false;
          this.cdr.markForCheck();
          const errorMessage =
            err.error?.message ||
            err.error?.error ||
            err.message ||
            'An unexpected error occurred during the upload.';
          this.notificationService.showError(`Upload failed: ${errorMessage}`);
        },
      });
  }

  public cancelUpload(): void {
    if (!this.isUploading) return;
    this.uploadSub?.unsubscribe();
    this.uploadSub = undefined;
    this.isUploading = false;
    this.uploadProgress = 0;
    this.notificationService.showInfo('Import upload cancelled.');
    if (!this.isDestroyed) {
      this.cdr.markForCheck();
    }
  }

  public navigateBack(): void {
    if (this.currentDatabase) {
      this.router.navigate(['/dashboard/settings', this.currentDatabase.id]);
    }
  }

  ngOnDestroy(): void {
    this.isDestroyed = true;
    this.uploadSub?.unsubscribe();
    this.destroy$.next();
    this.destroy$.complete();
  }
}
