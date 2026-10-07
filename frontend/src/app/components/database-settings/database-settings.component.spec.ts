// Regression spec for FE-002 (AI/Reports/20261006_frontend_review.md):
// "Housekeeping settings silently reset to 0 on save (data loss)".
//
// The backend does not echo the saved strings back: on every GET it re-emits the parsed
// values through internal/shared/strgen.go (DurationToString / BytesToString). So the value
// the user saved as "25h" comes back as "1d 1h", "1536M" as "1.5G" and "90m" as "1h 30min".
// The old /^(\d+)([a-zA-Z]*)$/ parser could not read any of those and fell back to {value: 0},
// so the next save of *any* setting on the page wrote "0" back and disabled the rule.
//
// Second spec (Phase B, review §7 item 1): the "never coerce a non-empty server string
// to 0" safety net itself. enable()/disable() in updatePermissions() re-emits
// valueChanges on every child and used to wipe the unparseable-value state right after
// loading (the warning vanished and the save silently zeroed the row). Since the backend
// zeroes unparseable strings anyway (internal/httpserver/databasehandler/utils.go
// HousekeepingPayload.toModel), the honest behaviour is to warn and block the save until
// the admin re-enters the row.

import { CommonModule } from '@angular/common';
import { NO_ERRORS_SCHEMA } from '@angular/core';
import { TestBed, ComponentFixture } from '@angular/core/testing';
import { ReactiveFormsModule, FormsModule } from '@angular/forms';
import { ActivatedRoute, convertToParamMap } from '@angular/router';
import { BehaviorSubject, of } from 'rxjs';
import { DatabaseSettingsComponent } from './database-settings.component';
import { AuthService } from '../../services/auth.service';
import { DatabaseService, DatabaseUpdatePayload } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import { ContentType, Database, User } from '../../models';

/** Spec-local parser: sums "number + unit" tokens, mirroring the backend emitters' output. */
function parseDurationToSeconds(str: string): number {
  const factors: Record<string, number> = { s: 1, min: 60, m: 60, h: 3600, d: 86400 };
  let total = 0;
  for (const match of str.matchAll(/(\d+(?:\.\d+)?)\s*([a-zA-Z]+)/g)) {
    total += parseFloat(match[1]) * factors[match[2].toLowerCase()];
  }
  return total;
}

/** Spec-local parser for BytesToString output ("1.5G", "1536M", "512K", ...). */
function parseSizeToBytes(str: string): number {
  const factors: Record<string, number> = {
    b: 1,
    k: 1024,
    m: 1024 ** 2,
    g: 1024 ** 3,
    t: 1024 ** 4,
  };
  let total = 0;
  for (const match of str.matchAll(/(\d+(?:\.\d+)?)\s*([a-zA-Z]+)/g)) {
    total += parseFloat(match[1]) * factors[match[2].toLowerCase()];
  }
  return total;
}

describe('DatabaseSettingsComponent — FE-002 regression', () => {
  let fixture: ComponentFixture<DatabaseSettingsComponent>;
  let component: DatabaseSettingsComponent;
  let dbSubject: BehaviorSubject<Database | null>;
  let updateDatabaseSpy: jasmine.Spy;
  let notificationSpy: jasmine.SpyObj<NotificationService>;

  // What internal/shared/strgen.go emits for the saved values "25h" / "1536M" / "90m":
  const serverHousekeeping = {
    interval: '1d 1h', // DurationToString(25h)
    disk_space: '1.5G', // BytesToString(1536M)
    max_age: '1h 30min', // DurationToString(90m)
  };

  const musicDb: Database = {
    id: 'db1',
    name: 'Music',
    content_type: ContentType.Audio,
    n_max_queued: 0,
    config: { create_preview: true, auto_conversion: '' },
    housekeeping: serverHousekeeping,
    custom_fields: [],
  };

  const adminUser: User = {
    id: 'u1',
    username: 'admin',
    is_admin: true,
    account_type: 'local',
    permissions: [],
  };

  beforeEach(async () => {
    dbSubject = new BehaviorSubject<Database | null>(null);

    const databaseSpy = jasmine.createSpyObj<DatabaseService>('DatabaseService', [
      'selectDatabase',
      'updateDatabase',
      'triggerHousekeeping',
      'deleteDatabase',
      'addCustomField',
      'updateCustomField',
      'deleteCustomField',
    ]);
    databaseSpy.selectedDatabase$ = dbSubject.asObservable();
    databaseSpy.selectDatabase.and.callFake(() => {
      dbSubject.next(musicDb);
      return of(musicDb);
    });
    updateDatabaseSpy = databaseSpy.updateDatabase.and.returnValue(of(musicDb));

    const authSpy = jasmine.createSpyObj<AuthService>('AuthService', ['getCurrentUser'], {
      currentUser$: of(adminUser),
    });
    authSpy.getCurrentUser.and.returnValue(adminUser);

    const modalSpy = jasmine.createSpyObj<ModalService>('ModalService', ['open', 'close']);
    notificationSpy = jasmine.createSpyObj<NotificationService>('NotificationService', [
      'showSuccess',
      'showError',
    ]);

    await TestBed.configureTestingModule({
      declarations: [DatabaseSettingsComponent],
      imports: [CommonModule, ReactiveFormsModule, FormsModule],
      // routerLink bindings belong to the router and are not under test (the
      // formatBytes pipe is only reached when db.stats is set — never in these fixtures).
      schemas: [NO_ERRORS_SCHEMA],
      providers: [
        { provide: DatabaseService, useValue: databaseSpy },
        { provide: AuthService, useValue: authSpy },
        { provide: ModalService, useValue: modalSpy },
        { provide: NotificationService, useValue: notificationSpy },
        { provide: ActivatedRoute, useValue: { paramMap: of(convertToParamMap({ id: 'db1' })) } },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(DatabaseSettingsComponent);
    component = fixture.componentInstance;
    // Renders the template and runs ngOnInit once (the mocked load is synchronous).
    fixture.detectChanges();
  });

  it('FE-002: housekeeping round-trip 25h/1536M/90m must not become 0 on the next save', () => {
    // The form is populated from the backend-emitted strings ("1d 1h" / "1.5G" / "1h 30min").
    // Save without touching anything else on the page (the reported failure sequence).
    component.onSaveSettings();

    expect(updateDatabaseSpy).toHaveBeenCalledTimes(1);
    const payload = updateDatabaseSpy.calls.mostRecent().args[1] as DatabaseUpdatePayload;
    const hk = payload.housekeeping as { interval: string; disk_space: string; max_age: string };

    expect(hk.interval).not.toBe('0');
    expect(hk.disk_space).not.toBe('0');
    expect(hk.max_age).not.toBe('0');

    expect(parseDurationToSeconds(hk.interval))
      .withContext('interval: "25h" must survive the round-trip')
      .toBe(25 * 3600);
    expect(parseSizeToBytes(hk.disk_space))
      .withContext('disk_space: "1536M" must survive the round-trip')
      .toBe(1536 * 1024 * 1024);
    expect(parseDurationToSeconds(hk.max_age))
      .withContext('max_age: "90m" must survive the round-trip')
      .toBe(90 * 60);
  });

  it('FE-002: an unparseable server value must warn after load and block saving (the backend would silently zero it)', () => {
    // "1d 1h xyz" is not something strgen emits, but any non-empty value the parser
    // cannot read must never be silently coerced to "0" (the FE-002 data loss).
    const brokenDb: Database = {
      ...musicDb,
      housekeeping: { interval: '1d 1h xyz', disk_space: '1.5G', max_age: '0' },
    };
    dbSubject.next(brokenDb); // the load under test
    fixture.detectChanges();

    expect(component.housekeepingParseWarning)
      .withContext(
        'the parse warning must survive the load (enable()/disable() re-emitting valueChanges used to wipe it)',
      )
      .not.toBeNull();
    expect(component.housekeepingParseWarning).toContain('"1d 1h xyz"');
    expect(component.hasUnparseableHousekeeping)
      .withContext('the Save button binding must disable saving while a row is unreadable')
      .toBeTrue();

    const banner = (
      Array.from(fixture.nativeElement.querySelectorAll('.form-error')) as HTMLElement[]
    ).find((el) => (el.textContent ?? '').includes('Could not read'));
    expect(banner).withContext('the warning banner must be rendered after the load').toBeTruthy();

    // Honest behaviour: saving is blocked outright instead of writing the raw string
    // back — the backend would silently zero it (databasehandler/utils.go toModel).
    component.onSaveSettings();
    expect(updateDatabaseSpy)
      .withContext(
        'saving must be blocked while a housekeeping row holds an unreadable server value',
      )
      .not.toHaveBeenCalled();
    expect(notificationSpy.showError)
      .withContext('the admin must be told why saving is blocked')
      .toHaveBeenCalled();

    // Once the admin re-enters the row (a real user edit), its raw value is gone and
    // saving proceeds with a value the backend can parse.
    const hkGroup = component.settingsForm.get('housekeeping');
    hkGroup?.get('interval_value')?.setValue(25);
    hkGroup?.get('interval_unit')?.setValue('h');
    expect(component.housekeepingParseWarning)
      .withContext('a retyped row must clear its raw value')
      .toBeNull();

    component.onSaveSettings();
    expect(updateDatabaseSpy).toHaveBeenCalledTimes(1);
    const payload = updateDatabaseSpy.calls.mostRecent().args[1] as DatabaseUpdatePayload;
    const hk = payload.housekeeping as { interval: string; disk_space: string; max_age: string };
    expect(hk.interval).toBe('25h');
    expect(hk.disk_space).toBe('1536M');
    expect(hk.max_age).toBe('0');
  });
});
