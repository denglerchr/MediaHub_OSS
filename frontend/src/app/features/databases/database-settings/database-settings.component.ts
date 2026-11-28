import { CommonModule } from '@angular/common';
import { Component, DestroyRef, OnInit, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { FormBuilder, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { Observable, of, EMPTY } from 'rxjs';
import { switchMap, filter, finalize, catchError } from 'rxjs/operators';
import { Database, User, DatabaseConfig, ConfirmationModalData } from '../../../core/models';
import { DatabaseService, DatabaseUpdatePayload } from '../../../core/services/database.service';
import { AuthService } from '../../../core/services/auth.service';
import { ModalService } from '../../../core/services/modal.service';
import { NotificationService } from '../../../core/services/notification.service';
import { DATABASE_NAME_MAX_LENGTH, DATABASE_NAME_PATTERN } from '../../../shared/utils/validation';
import {
  HOUSEKEEPING_INTEGER_PATTERN,
  formatHousekeeping,
  parseHousekeepingString,
} from '../../../shared/utils/housekeeping';
import { resolveDatabasePermissions } from '../../../shared/utils/permissions';
import { supportsAutoPreview } from '../../../shared/utils/entry-domain';
import { FormatBytesPipe } from '../../../shared/pipes/format-bytes.pipe';
import { PageHeaderComponent } from '../../../shared/components/page-header/page-header.component';
import { DatabaseMediaConfigComponent } from '../database-media-config/database-media-config.component';
import { HousekeepingFieldsComponent } from '../housekeeping-fields/housekeeping-fields.component';
import { DatabaseCustomFieldsEditorComponent } from '../database-custom-fields-editor/database-custom-fields-editor.component';

@Component({
  selector: 'app-database-settings',
  templateUrl: './database-settings.component.html',
  styleUrls: ['./database-settings.component.css'],
  standalone: true,
  imports: [
    CommonModule,
    ReactiveFormsModule,
    RouterLink,
    FormatBytesPipe,
    PageHeaderComponent,
    DatabaseMediaConfigComponent,
    HousekeepingFieldsComponent,
    DatabaseCustomFieldsEditorComponent,
  ],
})
export class DatabaseSettingsComponent implements OnInit {
  public selectedDatabase$: Observable<Database | null>;
  public currentUser$: Observable<User | null>;
  public settingsForm: FormGroup;
  public isLoading = false;
  public isSavingSettings = false;
  public isRunningHousekeeping = false;
  public isDeletingDatabase = false;

  public currentDb: Database | null = null;

  public canAdmin = false;
  public canCreate = false;
  public canDelete = false;
  public isGlobalAdmin = false;

  // FE-002: raw server strings for housekeeping values that could not be parsed.
  // While one is pending, saving is blocked: the backend silently zeroes any string
  // it cannot parse (internal/httpserver/databasehandler/utils.go
  // HousekeepingPayload.toModel), so writing the row back would reset it to "0".
  // The entry is cleared as soon as the admin re-enters the row (real user edit).
  private housekeepingRaw: { interval?: string; disk_space?: string; max_age?: string } = {};

  // FE-002: true while server state is written into the form. Programmatic updates
  // (patchValue, and enable()/disable() re-emitting valueChanges on every child)
  // must not be mistaken for user edits, which clear the raw state above.
  private isApplyingServerValue = false;

  private pendingLoadDbId: string | null = null;
  private expectedDbId: string | null = null;
  private loadedFormDbId: string | null = null;
  private saveRequestSeq = 0;

  /**
   * FE-002: warning shown when a saved housekeeping value could not be parsed. Saving
   * is blocked while this is set: the backend silently zeroes any unparseable string
   * (internal/httpserver/databasehandler/utils.go HousekeepingPayload.toModel), so the
   * admin must enter a new value for the affected row(s) before saving.
   */
  public housekeepingParseWarning: string | null = null;

  /**
   * FE-002: true while a housekeeping row holds a server value the parser could not
   * read. Saving is disabled in this state (see housekeepingParseWarning).
   */
  public hasUnparseableHousekeeping = false;

  private readonly destroyRef = inject(DestroyRef);

  constructor(
    private route: ActivatedRoute,
    private databaseService: DatabaseService,
    private authService: AuthService,
    private modalService: ModalService,
    private notificationService: NotificationService,
    private fb: FormBuilder,
  ) {
    this.selectedDatabase$ = this.databaseService.selectedDatabase$;
    this.currentUser$ = this.authService.currentUser$;

    this.settingsForm = this.fb.group({
      name: [
        '',
        [
          Validators.required,
          Validators.maxLength(DATABASE_NAME_MAX_LENGTH),
          Validators.pattern(DATABASE_NAME_PATTERN),
        ],
      ], // NEW: Added name field for renaming
      n_max_queued: [
        0,
        [Validators.required, Validators.min(0), Validators.pattern(HOUSEKEEPING_INTEGER_PATTERN)],
      ],
      create_preview: [true],
      auto_conversion: [''],
      housekeeping: this.fb.group({
        interval_value: [
          0,
          [
            Validators.required,
            Validators.min(0),
            Validators.pattern(HOUSEKEEPING_INTEGER_PATTERN),
          ],
        ],
        interval_unit: ['h'],
        disk_space_value: [
          0,
          [
            Validators.required,
            Validators.min(0),
            Validators.pattern(HOUSEKEEPING_INTEGER_PATTERN),
          ],
        ],
        disk_space_unit: ['G'],
        max_age_value: [
          0,
          [
            Validators.required,
            Validators.min(0),
            Validators.pattern(HOUSEKEEPING_INTEGER_PATTERN),
          ],
        ],
        max_age_unit: ['d'],
      }),
    });
  }

  ngOnInit(): void {
    // FE-002: as soon as the admin edits a housekeeping row, its raw pass-through
    // value is replaced by the value the admin entered.
    const housekeepingGroup = this.settingsForm.get('housekeeping');
    (['interval', 'disk_space', 'max_age'] as const).forEach((name) => {
      [`${name}_value`, `${name}_unit`].forEach((controlName) => {
        housekeepingGroup
          ?.get(controlName)
          ?.valueChanges.pipe(takeUntilDestroyed(this.destroyRef))
          .subscribe(() => {
            // FE-002: only a real user edit replaces the unreadable server value —
            // programmatic emissions while loading must not clear it.
            if (this.isApplyingServerValue) {
              return;
            }
            if (this.housekeepingRaw[name] !== undefined) {
              delete this.housekeepingRaw[name];
              this.updateHousekeepingParseState();
            }
          });
      });
    });

    this.route.paramMap
      .pipe(
        switchMap((params) => {
          const id = params.get('id'); // UPDATED: Extract 'id' instead of 'name'
          this.saveRequestSeq++;
          this.isSavingSettings = false;
          this.settingsForm.reset();
          this.housekeepingRaw = {};
          this.updateHousekeepingParseState();
          this.currentDb = null;
          this.loadedFormDbId = null;
          this.expectedDbId = id;
          this.pendingLoadDbId = id;
          this.isLoading = !!id;
          return id
            ? this.databaseService.selectDatabase(id).pipe(
                catchError(() => {
                  if (this.pendingLoadDbId === id) {
                    this.pendingLoadDbId = null;
                    this.isLoading = false;
                  }
                  return EMPTY;
                }),
              )
            : of(null);
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: () => {
          if (this.pendingLoadDbId !== null) {
            this.pendingLoadDbId = null;
            this.isLoading = false;
          }
        },
        error: () => {
          this.pendingLoadDbId = null;
          this.isLoading = false;
        },
      });

    this.selectedDatabase$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((db) => {
      if (db && this.expectedDbId && db.id !== this.expectedDbId) {
        if (this.pendingLoadDbId === null) {
          this.currentDb = null;
          this.loadedFormDbId = null;
          this.housekeepingRaw = {};
          this.updateHousekeepingParseState();
          this.settingsForm.reset();
          this.isLoading = false;
          this.updatePermissions();
        }
        return;
      }

      if (db) {
        if (this.pendingLoadDbId === db.id) {
          this.pendingLoadDbId = null;
          this.isLoading = false;
        }
        this.currentDb = db;

        // Preserve unsaved settingsForm edits when custom field CRUD / housekeeping
        // re-emits selectedDatabase$ for the already-loaded database.
        if (this.loadedFormDbId !== db.id || this.settingsForm.pristine) {
          this.applyDatabaseToForm(db);
        }
      } else {
        this.currentDb = null;
        this.loadedFormDbId = null;
        this.housekeepingRaw = {};
        this.updateHousekeepingParseState();
        this.settingsForm.reset();
        if (this.pendingLoadDbId === null) {
          this.isLoading = false;
        }
      }
      this.updatePermissions();
    });

    this.currentUser$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe(() => {
      this.updatePermissions();
    });
  }

  private applyDatabaseToForm(db: Database): void {
    this.loadedFormDbId = db.id;

    // Extract numerical values and string units from the DB payload.
    // FE-002: the backend re-emits these strings through
    // internal/shared/strgen.go ("25h" comes back as "1d 1h", "1536M" as
    // "1.5G", "90m" as "1h 30min"), so parsing must be lenient and must
    // never silently coerce a non-empty value to 0.
    const interval = parseHousekeepingString(db.housekeeping.interval, 'duration', 'h');
    const diskSpace = parseHousekeepingString(db.housekeeping.disk_space, 'size', 'G');
    const maxAge = parseHousekeepingString(db.housekeeping.max_age, 'duration', 'd');

    // FE-002: patchValue emits valueChanges per control — guard the
    // edit-detection subscriptions above while applying server state.
    this.isApplyingServerValue = true;
    try {
      this.settingsForm.patchValue({
        name: db.name,
        n_max_queued: db.n_max_queued,
        ...db.config,
        housekeeping: {
          interval_value: interval ? interval.value : 0,
          interval_unit: interval ? interval.unit : 'h',
          disk_space_value: diskSpace ? diskSpace.value : 0,
          disk_space_unit: diskSpace ? diskSpace.unit : 'G',
          max_age_value: maxAge ? maxAge.value : 0,
          max_age_unit: maxAge ? maxAge.unit : 'd',
        },
      });

      // Keep unparseable server values verbatim so the warning names them and
      // the save stays blocked until the admin re-enters the row (the backend
      // would silently zero them — see the housekeepingRaw declaration).
      this.housekeepingRaw = {};
      if (!interval) {
        this.housekeepingRaw.interval = String(db.housekeeping.interval ?? '');
      }
      if (!diskSpace) {
        this.housekeepingRaw.disk_space = String(db.housekeeping.disk_space ?? '');
      }
      if (!maxAge) {
        this.housekeepingRaw.max_age = String(db.housekeeping.max_age ?? '');
      }
      this.updateHousekeepingParseState();
      this.settingsForm.markAsPristine();
    } finally {
      this.isApplyingServerValue = false;
    }
  }

  get housekeepingGroup(): FormGroup {
    return this.settingsForm.get('housekeeping') as FormGroup;
  }

  private updateHousekeepingParseState(): void {
    const parts: string[] = [];
    if (this.housekeepingRaw.interval !== undefined) {
      parts.push(`run interval "${this.housekeepingRaw.interval}"`);
    }
    if (this.housekeepingRaw.disk_space !== undefined) {
      parts.push(`max disk space "${this.housekeepingRaw.disk_space}"`);
    }
    if (this.housekeepingRaw.max_age !== undefined) {
      parts.push(`max entry age "${this.housekeepingRaw.max_age}"`);
    }
    this.hasUnparseableHousekeeping = parts.length > 0;
    this.housekeepingParseWarning =
      parts.length > 0
        ? `Could not read the saved housekeeping setting(s): ${parts.join(', ')}. Enter a new value for the affected row(s) before saving — saving is blocked because the backend would silently reset them to 0.`
        : null;
  }

  private updatePermissions(): void {
    const resolved = resolveDatabasePermissions(
      this.authService.getCurrentUser(),
      this.currentDb?.id,
    );

    this.canAdmin = resolved.canAdmin;
    this.canCreate = resolved.canCreate;
    this.canDelete = resolved.canDelete;
    this.isGlobalAdmin = Boolean(this.currentDb) && resolved.isGlobalAdmin;

    if (this.canAdmin) {
      // FE-002: never emit valueChanges here — enable()/disable() re-emit on every
      // child control and the edit-detection subscriptions would read that as user
      // edits, wiping the unparseable-value safety net right after loading.
      this.settingsForm.enable({ emitEvent: false });
    } else {
      this.settingsForm.disable({ emitEvent: false });
    }
  }

  onSaveSettings(): void {
    if (this.isSavingSettings || this.settingsForm.invalid || !this.currentDb || !this.canAdmin) {
      return;
    }
    // FE-002: block the save while a housekeeping row holds a server value the parser
    // could not read. PUT replaces the whole housekeeping block and the backend
    // silently zeroes any string it cannot parse
    // (internal/httpserver/databasehandler/utils.go HousekeepingPayload.toModel), so
    // there is no way to save the other rows without resetting that one to "0".
    if (this.hasUnparseableHousekeeping) {
      this.notificationService.showError(
        'Enter a new value for the housekeeping setting(s) that could not be read before saving.',
      );
      return;
    }
    this.isSavingSettings = true;

    const formValue = this.settingsForm.value;
    const config: DatabaseConfig = {};

    if (supportsAutoPreview(this.currentDb.content_type)) {
      config.create_preview = formValue.create_preview;
      config.auto_conversion = formValue.auto_conversion;
    }

    // Reconstruct the strings (e.g. {value: 100, unit: "G"} => "100G").
    // If the value is 0, we send "0" to disable it as per concept spec.
    // FE-002: unreadable server values never reach this point (saving is blocked
    // above until the admin re-enters the row), so nothing is silently zeroed.
    const hkForm = formValue.housekeeping;
    const interval = formatHousekeeping(hkForm.interval_value, hkForm.interval_unit, 'duration');
    const diskSpace = formatHousekeeping(hkForm.disk_space_value, hkForm.disk_space_unit, 'size');
    const maxAge = formatHousekeeping(hkForm.max_age_value, hkForm.max_age_unit, 'duration');

    if (interval === null || diskSpace === null || maxAge === null) {
      this.isSavingSettings = false;
      this.notificationService.showError(
        'Housekeeping values must be at least one second (or one byte for disk space).',
      );
      return;
    }

    const reconstructedHousekeeping = {
      interval: interval,
      disk_space: diskSpace,
      max_age: maxAge,
    };

    const payload: DatabaseUpdatePayload = {
      name: formValue.name, // NEW: Include name in payload
      n_max_queued: formValue.n_max_queued,
      config: config,
      housekeeping: reconstructedHousekeeping,
    };

    const targetDbId = this.currentDb.id;
    const seq = ++this.saveRequestSeq;

    this.databaseService
      .updateDatabase(targetDbId, payload) // UPDATED: Use ULID
      // FE-043: the request must not outlive the component.
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => {
          if (seq === this.saveRequestSeq) {
            this.isSavingSettings = false;
          }
        }),
      )
      .subscribe({
        next: (updatedDb) => {
          if (seq !== this.saveRequestSeq || this.currentDb?.id !== targetDbId) {
            return;
          }
          if (updatedDb) {
            this.currentDb = updatedDb;
            this.applyDatabaseToForm(updatedDb);
            this.updatePermissions();
          } else {
            this.settingsForm.markAsPristine();
          }
        },
        error: () => {},
      });
  }

  onTriggerHousekeeping(): void {
    if (this.isRunningHousekeeping || !this.currentDb || !this.canDelete) return;
    this.isRunningHousekeeping = true;
    this.databaseService
      .triggerHousekeeping(this.currentDb.id) // UPDATED: Use ULID
      // FE-043: the request must not outlive the component.
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => (this.isRunningHousekeeping = false)),
      )
      .subscribe({
        error: () => {},
      });
  }

  onDeleteDatabase(): void {
    if (this.isDeletingDatabase || !this.currentDb || !this.isGlobalAdmin) return;

    const dbId = this.currentDb.id;
    const dbName = this.currentDb.name;

    const modalData: ConfirmationModalData = {
      title: 'Confirm Database Deletion',
      message: `This action will permanently delete the '${dbName}' database and all entries within it. This action cannot be undone.`,
    };

    this.modalService
      .confirm(modalData)
      .pipe(filter(Boolean), takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.isDeletingDatabase = true;
        this.databaseService
          .deleteDatabase(dbId)
          .pipe(
            takeUntilDestroyed(this.destroyRef),
            finalize(() => (this.isDeletingDatabase = false)),
          )
          .subscribe({
            error: () => {},
          });
      });
  }
}
