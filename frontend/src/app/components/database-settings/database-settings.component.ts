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
import {
  ConfirmationModalComponent,
  ConfirmationModalData,
} from '../confirmation-modal/confirmation-modal.component';
import { isValidCustomFieldName, isReservedCustomFieldName } from '../../utils/validation';

/** Kind of a housekeeping string: durations ("25h") or sizes ("1536M"). */
type HousekeepingKind = 'duration' | 'size';

@Component({
  selector: 'app-database-settings',
  templateUrl: './database-settings.component.html',
  styleUrls: ['./database-settings.component.css'],
  standalone: false,
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

  private destroy$ = new Subject<void>();

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
    // FE-002: as soon as the admin edits a housekeeping row, its raw pass-through
    // value is replaced by the value the admin entered.
    const housekeepingGroup = this.settingsForm.get('housekeeping');
    (['interval', 'disk_space', 'max_age'] as const).forEach((name) => {
      [`${name}_value`, `${name}_unit`].forEach((controlName) => {
        housekeepingGroup
          ?.get(controlName)
          ?.valueChanges.pipe(takeUntil(this.destroy$))
          .subscribe(() => {
            // FE-002: only a real user edit replaces the unreadable server value —
            // programmatic emissions while loading must not clear it.
            if (this.isApplyingServerValue) {
              return;
            }
            if (this.housekeepingRaw[name] !== undefined) {
              delete this.housekeepingRaw[name];
            }
          });
      });
    });

    this.route.paramMap
      .pipe(
        takeUntil(this.destroy$),
        switchMap((params) => {
          const id = params.get('id'); // UPDATED: Extract 'id' instead of 'name'
          this.settingsForm.reset();
          this.housekeepingRaw = {};
          this.isLoading = true;
          return id ? this.databaseService.selectDatabase(id) : of(null);
        }),
      )
      .subscribe();

    this.selectedDatabase$.pipe(takeUntil(this.destroy$)).subscribe((db) => {
      this.isLoading = false;
      if (db) {
        this.currentDb = db;

        // Extract numerical values and string units from the DB payload.
        // FE-002: the backend re-emits these strings through
        // internal/shared/strgen.go ("25h" comes back as "1d 1h", "1536M" as
        // "1.5G", "90m" as "1h 30min"), so parsing must be lenient and must
        // never silently coerce a non-empty value to 0.
        const interval = this.parseHousekeepingString(db.housekeeping.interval, 'h', 'duration');
        const diskSpace = this.parseHousekeepingString(db.housekeeping.disk_space, 'G', 'size');
        const maxAge = this.parseHousekeepingString(db.housekeeping.max_age, 'd', 'duration');

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
        } finally {
          this.isApplyingServerValue = false;
        }
      } else {
        this.currentDb = null;
        this.housekeepingRaw = {};
        this.settingsForm.reset();
      }
      this.updatePermissions();
    });

    this.currentUser$.pipe(takeUntil(this.destroy$)).subscribe(() => {
      this.updatePermissions();
    });
  }

  /**
   * FE-002: warning shown when a saved housekeeping value could not be parsed. Saving
   * is blocked while this is set: the backend silently zeroes any unparseable string
   * (internal/httpserver/databasehandler/utils.go HousekeepingPayload.toModel), so the
   * admin must enter a new value for the affected row(s) before saving.
   */
  get housekeepingParseWarning(): string | null {
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
    return parts.length > 0
      ? `Could not read the saved housekeeping setting(s): ${parts.join(', ')}. Enter a new value for the affected row(s) before saving — saving is blocked because the backend would silently reset them to 0.`
      : null;
  }

  /**
   * FE-002: true while a housekeeping row holds a server value the parser could not
   * read. Saving is disabled in this state (see housekeepingParseWarning).
   */
  get hasUnparseableHousekeeping(): boolean {
    return Object.keys(this.housekeepingRaw).length > 0;
  }

  // Unit factors in the smallest unit of each kind (seconds / bytes), matching the
  // units understood by the backend (internal/shared/parsers.go).
  private static readonly DURATION_FACTORS: Record<string, number> = {
    s: 1,
    m: 60,
    h: 3600,
    d: 86400,
  };

  private static readonly SIZE_FACTORS: Record<string, number> = {
    b: 1,
    k: 1024,
    m: 1024 ** 2,
    g: 1024 ** 3,
    t: 1024 ** 4,
    p: 1024 ** 5,
  };

  private static readonly DURATION_ALIASES: Record<string, string> = {
    s: 's',
    sec: 's',
    secs: 's',
    second: 's',
    seconds: 's',
    m: 'm',
    min: 'm',
    mins: 'm',
    minute: 'm',
    minutes: 'm',
    h: 'h',
    hr: 'h',
    hrs: 'h',
    hour: 'h',
    hours: 'h',
    d: 'd',
    day: 'd',
    days: 'd',
  };

  private static readonly SIZE_ALIASES: Record<string, string> = {
    b: 'b',
    byte: 'b',
    bytes: 'b',
    k: 'k',
    kb: 'k',
    m: 'm',
    mb: 'm',
    g: 'g',
    gb: 'g',
    t: 't',
    tb: 't',
    p: 'p',
    pb: 'p',
  };

  // Select options offered in the form — must match database-settings.component.html.
  private static readonly DURATION_UNITS = ['d', 'h', 'm', 's'];
  private static readonly SIZE_UNITS = ['T', 'G', 'M', 'K', 'B'];

  /**
   * Breaks apart housekeeping strings like "100G", "24h", but also the compound and
   * fractional strings the backend emits, like "1d 1h", "1h 30min" or "1.5G".
   * Returns null when a non-empty value cannot be parsed — the caller then records
   * the raw string and blocks saving, because the backend would zero the row (FE-002).
   */
  private parseHousekeepingString(
    val: string | number | undefined,
    defaultUnit: string,
    kind: HousekeepingKind,
  ): { value: number; unit: string } | null {
    if (val === undefined || val === null) {
      return { value: 0, unit: defaultUnit };
    }
    const str = String(val).trim();
    if (str === '' || str === '0') {
      return { value: 0, unit: defaultUnit };
    }

    const factors =
      kind === 'duration'
        ? DatabaseSettingsComponent.DURATION_FACTORS
        : DatabaseSettingsComponent.SIZE_FACTORS;
    const aliases =
      kind === 'duration'
        ? DatabaseSettingsComponent.DURATION_ALIASES
        : DatabaseSettingsComponent.SIZE_ALIASES;
    const displayUnits =
      kind === 'duration'
        ? DatabaseSettingsComponent.DURATION_UNITS
        : DatabaseSettingsComponent.SIZE_UNITS;

    // Sum every "number + unit" token: "1d 1h" -> 86400 + 3600, "1.5G" -> 1.5 * 2^30, ...
    let rest = str;
    let total = 0;
    let sawPositiveNumber = false;
    while (rest.length > 0) {
      const match = rest.match(/^(\d+(?:[.,]\d+)?)\s*([a-zA-Z]*)/);
      if (!match) {
        return null;
      }
      const canonical = aliases[(match[2] || defaultUnit).toLowerCase()];
      if (!canonical) {
        return null;
      }
      const num = parseFloat(match[1].replace(',', '.'));
      if (!Number.isFinite(num)) {
        return null;
      }
      sawPositiveNumber = sawPositiveNumber || num > 0;
      total += num * factors[canonical];
      rest = rest.slice(match[0].length).trim();
    }

    // BytesToString rounds to 0.1 of the emitted unit ("1.5G", "1.1G"), so totals may
    // be fractional; round to whole seconds/bytes here. Reconstructing a rounded
    // value like "1.1G" can drift from the stored value by up to ~5% of the unit —
    // that loss is inherent to the backend's string emitter, not to this parser.
    const baseTotal = Math.round(total);
    if (baseTotal === 0) {
      // A non-zero input must never silently become 0.
      return sawPositiveNumber ? null : { value: 0, unit: defaultUnit };
    }

    // Represent the value in the largest selectable unit that divides it evenly, so
    // saving again reproduces it exactly ("25h", "1536M", "90m").
    for (const unit of displayUnits) {
      const factor = factors[unit.toLowerCase()];
      if (baseTotal % factor === 0) {
        return { value: baseTotal / factor, unit };
      }
    }
    return { value: baseTotal, unit: displayUnits[displayUnits.length - 1] };
  }

  /**
   * Formats a form value + unit into the string sent to the backend, normalized to
   * an integer the backend can parse (internal/shared/parsers.go) and to the same
   * value the form displayed ("25h", "1536M", "90m"). Returns null when the value
   * is positive but too small to represent (smaller than one second / byte).
   */
  private formatHousekeeping(value: number, unit: string, kind: HousekeepingKind): string | null {
    if (!Number.isFinite(value) || value <= 0) {
      return '0'; // "0" disables the rule as per concept spec
    }
    const factors =
      kind === 'duration'
        ? DatabaseSettingsComponent.DURATION_FACTORS
        : DatabaseSettingsComponent.SIZE_FACTORS;
    const displayUnits =
      kind === 'duration'
        ? DatabaseSettingsComponent.DURATION_UNITS
        : DatabaseSettingsComponent.SIZE_UNITS;

    const factor = factors[unit.toLowerCase()] ?? 1;
    const baseTotal = Math.round(value * factor);
    if (baseTotal <= 0) {
      return null;
    }

    for (const displayUnit of displayUnits) {
      const displayFactor = factors[displayUnit.toLowerCase()];
      if (baseTotal % displayFactor === 0) {
        return `${baseTotal / displayFactor}${kind === 'duration' ? displayUnit.toLowerCase() : displayUnit}`;
      }
    }
    return `${baseTotal}${kind === 'duration' ? displayUnits[displayUnits.length - 1].toLowerCase() : displayUnits[displayUnits.length - 1]}`;
  }

  private updatePermissions(): void {
    const user = this.authService.getCurrentUser();

    if (!user || !this.currentDb) {
      this.canAdmin = false;
      this.canCreate = false;
      this.canDelete = false;
      this.isGlobalAdmin = false;
      // FE-002: never emit valueChanges here — enable()/disable() re-emit on every
      // child control and the edit-detection subscriptions would read that as user
      // edits, wiping the unparseable-value safety net right after loading.
      this.settingsForm.disable({ emitEvent: false });
      return;
    }

    this.isGlobalAdmin = user.is_admin;

    if (user.is_admin) {
      this.canAdmin = true;
      this.canCreate = true;
      this.canDelete = true;
    } else {
      // UPDATED: Match the permission's database_id against currentDb.id
      const dbPermission = user.permissions?.find((p) => p.database_id === this.currentDb!.id);
      this.canAdmin = dbPermission?.can_admin || false;
      this.canCreate = dbPermission?.can_create || false;
      this.canDelete = dbPermission?.can_delete || false;
    }

    if (this.canAdmin) {
      // FE-002: see the disable() above — must not emit valueChanges.
      this.settingsForm.enable({ emitEvent: false });
    } else {
      this.settingsForm.disable({ emitEvent: false });
    }
  }

  onSaveSettings(): void {
    if (this.settingsForm.invalid || !this.currentDb || !this.canAdmin) {
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
    this.isLoading = true;

    const formValue = this.settingsForm.value;
    const config: DatabaseConfig = {};

    if (['image', 'audio', 'video'].includes(this.currentDb.content_type)) {
      config.create_preview = formValue.create_preview;
      config.auto_conversion = formValue.auto_conversion;
    }

    // Reconstruct the strings (e.g. {value: 100, unit: "G"} => "100G").
    // If the value is 0, we send "0" to disable it as per concept spec.
    // FE-002: unreadable server values never reach this point (saving is blocked
    // above until the admin re-enters the row), so nothing is silently zeroed.
    const hkForm = formValue.housekeeping;
    const interval = this.formatHousekeeping(
      hkForm.interval_value,
      hkForm.interval_unit,
      'duration',
    );
    const diskSpace = this.formatHousekeeping(
      hkForm.disk_space_value,
      hkForm.disk_space_unit,
      'size',
    );
    const maxAge = this.formatHousekeeping(hkForm.max_age_value, hkForm.max_age_unit, 'duration');

    if (interval === null || diskSpace === null || maxAge === null) {
      this.isLoading = false;
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

    this.databaseService
      .updateDatabase(this.currentDb.id, payload) // UPDATED: Use ULID
      // FE-043: the request must not outlive the component.
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => (this.isLoading = false)),
      )
      .subscribe(() => {
        this.settingsForm.markAsPristine();
      });
  }

  onTriggerHousekeeping(): void {
    if (!this.currentDb || !this.canDelete) return;
    this.isLoading = true;
    this.databaseService
      .triggerHousekeeping(this.currentDb.id) // UPDATED: Use ULID
      // FE-043: the request must not outlive the component.
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => (this.isLoading = false)),
      )
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
        filter((confirmed) => confirmed === true),
      )
      .subscribe(() => {
        if (this.currentDb) {
          this.isLoading = true;
          this.databaseService
            .deleteDatabase(this.currentDb.id)
            .pipe(
              takeUntil(this.destroy$),
              finalize(() => (this.isLoading = false)),
            )
            .subscribe();
        }
      });
  }

  onAddField(): void {
    if (!this.currentDb || !this.canAdmin || !this.newFieldName) return;

    if (!isValidCustomFieldName(this.newFieldName)) {
      this.notificationService.showError(
        'Field name must start with a letter or underscore and contain only alphanumeric characters or underscores.',
      );
      return;
    }
    // FE-049: reserved names collide with standard/media fields and are rejected by
    // the backend (AddField) — surface the reason up front instead of a bare 400.
    if (isReservedCustomFieldName(this.newFieldName)) {
      this.notificationService.showError(
        `Field name '${this.newFieldName}' is reserved (standard/media field). Please choose another name.`,
      );
      return;
    }

    this.isLoading = true;
    const newField: CustomField = {
      name: this.newFieldName,
      type: this.newFieldType,
      is_indexed: this.newFieldIsIndexed,
    };

    this.databaseService
      .addCustomField(this.currentDb.id, newField)
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => (this.isLoading = false)),
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
      this.notificationService.showError(
        'Field name must start with a letter or underscore and contain only alphanumeric characters or underscores.',
      );
      return;
    }
    // FE-049: renames into a reserved standard/media field name are rejected by the
    // backend (UpdateField) — surface the reason up front.
    if (isReservedCustomFieldName(this.editingFieldName)) {
      this.notificationService.showError(
        `Field name '${this.editingFieldName}' is reserved (standard/media field). Please choose another name.`,
      );
      return;
    }

    this.isLoading = true;
    this.databaseService
      .updateCustomField(
        this.currentDb.id,
        fieldId,
        this.editingFieldName,
        this.editingFieldIsIndexed,
      )
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => {
          this.isLoading = false;
          this.editingFieldId = null;
        }),
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
        filter((confirmed) => confirmed === true),
      )
      .subscribe(() => {
        if (this.currentDb && field.id !== undefined) {
          this.isLoading = true;
          this.databaseService
            .deleteCustomField(this.currentDb.id, field.id)
            .pipe(
              takeUntil(this.destroy$),
              finalize(() => (this.isLoading = false)),
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
