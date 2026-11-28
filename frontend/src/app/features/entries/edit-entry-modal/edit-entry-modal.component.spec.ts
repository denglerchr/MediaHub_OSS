// Regression specs for FE-003 / FE-004 / FE-005 (AI/Reports/20261006_frontend_review.md):
//   FE-003 — Edit modal writes stale custom-field values into the next entry (silent corruption)
//   FE-004 — Editing an entry always returns 400 when a numeric custom field is unset
//   FE-005 — Every entry edit truncates the timestamp to whole minutes
//
// The modal instance persists across opens (ModalService keeps it mounted), so form state
// leaks from one entry to the next unless the form is fully reset. The backend PATCH merges
// custom_fields key-by-key and only replaces `timestamp` when it is present in the payload
// (internal/httpserver/entryhandler/entries.go PatchEntry), so whatever this modal puts in
// the payload is what ends up in the database.

import { TestBed, ComponentFixture } from '@angular/core/testing';
import { CUSTOM_ELEMENTS_SCHEMA } from '@angular/core';
import { BehaviorSubject, EMPTY, Subject, of, throwError } from 'rxjs';
import { EditEntryModalComponent } from './edit-entry-modal.component';
import { ModalComponent } from '../../../shared/components/modal/modal.component';
import { EntryService } from '../../../core/services/entry.service';
import { DatabaseService } from '../../../core/services/database.service';
import { ModalService } from '../../../core/services/modal.service';
import { ContentType, Database, Entry } from '../../../core/models';

describe('EditEntryModalComponent — FE-003/FE-004/FE-005 regressions', () => {
  let fixture: ComponentFixture<EditEntryModalComponent>;
  let component: EditEntryModalComponent;
  let dbSubject: BehaviorSubject<Database | null>;
  let entrySubject: BehaviorSubject<Entry | null>;
  let updateEntrySpy: jasmine.Spy;

  // 14:30:47.123 UTC — seconds (and milliseconds) must survive an unrelated edit.
  const EXACT_TIMESTAMP = Date.UTC(2026, 9, 6, 14, 30, 47, 123);

  const musicDb: Database = {
    id: 'db1',
    name: 'Music',
    content_type: ContentType.Audio,
    n_max_queued: 0,
    config: { create_preview: true, auto_conversion: '' },
    housekeeping: { interval: '1h', disk_space: '100G', max_age: '0' },
    custom_fields: [
      { id: 0, name: 'artist', type: 'TEXT' },
      { id: 1, name: 'rating', type: 'INTEGER' },
    ],
  };

  function makeEntry(id: number, customFields: Record<string, unknown>): Entry {
    return {
      id,
      timestamp: EXACT_TIMESTAMP,
      created_at: EXACT_TIMESTAMP,
      updated_at: EXACT_TIMESTAMP,
      status: 'completed',
      filename: `entry-${id}.mp3`,
      custom_fields: customFields,
    };
  }

  const field = (name: string) => component.editForm.get(['custom_fields', name]);

  beforeEach(async () => {
    dbSubject = new BehaviorSubject<Database | null>(null);
    entrySubject = new BehaviorSubject<Entry | null>(null);

    const entrySpy = jasmine.createSpyObj<EntryService>('EntryService', ['updateEntry']);
    entrySpy.selectedEntry$ = entrySubject.asObservable();
    updateEntrySpy = entrySpy.updateEntry.and.callFake(() => of(makeEntry(1, {})));

    const databaseSpy = jasmine.createSpyObj<DatabaseService>('DatabaseService', [
      'selectDatabase',
    ]);
    databaseSpy.selectedDatabase$ = dbSubject.asObservable();

    const modalSpy = jasmine.createSpyObj<ModalService>('ModalService', [
      'close',
      'registerCloseGuard',
      'unregisterCloseGuard',
      'getModalEvents',
    ]);
    modalSpy.getModalEvents.and.returnValue(EMPTY);

    await TestBed.configureTestingModule({
      imports: [EditEntryModalComponent],
      providers: [
        { provide: EntryService, useValue: entrySpy },
        { provide: DatabaseService, useValue: databaseSpy },
        { provide: ModalService, useValue: modalSpy },
      ],
    })
      .overrideComponent(EditEntryModalComponent, {
        remove: { imports: [ModalComponent] },
        add: { schemas: [CUSTOM_ELEMENTS_SCHEMA] },
      })
      .compileComponents();

    fixture = TestBed.createComponent(EditEntryModalComponent);
    component = fixture.componentInstance;
    dbSubject.next(musicDb);
    component.ngOnInit(); // builds the schema-driven form; no entry selected yet
  });

  it("FE-003: no stale custom field across entries (entry B must not inherit entry A's artist)", () => {
    const entryA = makeEntry(1, { artist: 'X' });
    const entryB = makeEntry(2, {}); // artist unset

    entrySubject.next(entryA); // admin edits entry A, saves, closes the modal
    entrySubject.next(entryB); // admin opens entry B

    expect(field('artist')?.value)
      .withContext("opening entry B must not show entry A's artist")
      .toBe('');

    component.onSubmit();

    expect(updateEntrySpy).toHaveBeenCalledTimes(1);
    const payload = updateEntrySpy.calls.mostRecent().args[2] as Partial<Entry>;
    expect(payload.custom_fields?.['artist'])
      .withContext("saving entry B must not write entry A's artist into it")
      .not.toBe('X');
  });

  it('FE-004: blank numeric custom field is omitted from the PATCH, not sent as ""', () => {
    const entry = makeEntry(1, { artist: 'X' }); // rating left unset

    entrySubject.next(entry);
    field('rating')?.setValue(''); // user leaves the numeric field blank
    component.editForm.get('filename')?.setValue('renamed.mp3');

    component.onSubmit();

    expect(updateEntrySpy).toHaveBeenCalledTimes(1);
    const payload = updateEntrySpy.calls.mostRecent().args[2] as Partial<Entry>;
    expect(payload.filename).toBe('renamed.mp3');
    expect(payload.custom_fields?.['rating'])
      .withContext(
        'a blank INTEGER field must be omitted ("rating": "" makes the backend return 400)',
      )
      .toBeUndefined();
    expect(Object.values(payload.custom_fields ?? {}))
      .withContext('no custom field may be sent as an empty string')
      .not.toContain('');
  });

  it('FE-005: timestamp seconds are preserved when only the filename is edited', () => {
    const entry = makeEntry(1, { artist: 'X' });

    entrySubject.next(entry);

    expect(String(component.editForm.get('timestamp')?.value))
      .withContext('the form must keep the seconds of the capture time')
      .toContain(':47');

    component.editForm.get('filename')?.setValue('renamed.mp3'); // unrelated edit
    component.onSubmit();

    expect(updateEntrySpy).toHaveBeenCalledTimes(1);
    const payload = updateEntrySpy.calls.mostRecent().args[2] as Partial<Entry>;
    // If the timestamp is included it must be exact; if omitted the backend keeps the original.
    const effectiveTimestamp =
      payload.timestamp === undefined ? entry.timestamp : payload.timestamp;
    expect(effectiveTimestamp)
      .withContext('an unrelated filename edit must not truncate the timestamp to whole minutes')
      .toBe(entry.timestamp);
  });

  it('FE-005: a dirty timestamp is sent in full second precision', () => {
    const entry = makeEntry(1, {});

    entrySubject.next(entry);

    const ctrl = component.editForm.get('timestamp');
    const formValue = String(ctrl?.value);
    expect(formValue)
      .withContext('the form must keep the seconds of the capture time')
      .toContain(':47');

    // The admin re-picks the capture time (same second): the control becomes dirty and
    // is therefore included in the PATCH.
    ctrl?.markAsDirty();
    component.editForm.get('filename')?.setValue('renamed.mp3');

    component.onSubmit();

    expect(updateEntrySpy).toHaveBeenCalledTimes(1);
    const payload = updateEntrySpy.calls.mostRecent().args[2] as Partial<Entry>;
    expect(payload.timestamp)
      .withContext('a dirty timestamp must be sent as new Date(formValue).getTime()')
      .toBe(new Date(formValue).getTime());
    expect(new Date(payload.timestamp as number).getSeconds())
      .withContext('the seconds must survive into the payload')
      .toBe(47);
  });

  it("FE-003: no stale numeric custom field across entries (entry B must not inherit entry A's rating)", () => {
    const entryA = makeEntry(1, { rating: 5 });
    const entryB = makeEntry(2, {}); // rating unset

    entrySubject.next(entryA); // admin edits entry A, saves, closes the modal
    entrySubject.next(entryB); // admin opens entry B

    expect(field('rating')?.value)
      .withContext("opening entry B must show a blank numeric field, not entry A's rating")
      .toBeNull();

    component.onSubmit();

    expect(updateEntrySpy).toHaveBeenCalledTimes(1);
    const payload = updateEntrySpy.calls.mostRecent().args[2] as Partial<Entry>;
    expect(payload.custom_fields?.['rating'])
      .withContext("saving entry B must not write entry A's rating into it")
      .toBeUndefined();
  });

  it('shows saved values after an update: a re-published entry re-patches the form (reopen is not stale)', () => {
    entrySubject.next(makeEntry(1, { artist: 'old value', rating: 1 }));
    expect(field('artist')?.value).toBe('old value');

    // EntryService.updateEntry() re-publishes the PATCH response for this entry after
    // a successful save — the form must follow, otherwise re-opening Edit shows the
    // pre-edit values (entry-modal staleness bug).
    entrySubject.next(makeEntry(1, { artist: 'saved value', rating: 2 }));

    expect(field('artist')?.value)
      .withContext('re-opening the edit modal must show the saved metadata')
      .toBe('saved value');
    expect(field('rating')?.value).toBe(2);
  });

  it('populates a 1950-... datetime string and preserves pre-1970 timestamps on edit', () => {
    const PRE_1970_TIMESTAMP = Date.UTC(1950, 5, 15, 10, 30, 45);
    const entry: Entry = {
      id: 77,
      timestamp: PRE_1970_TIMESTAMP,
      created_at: PRE_1970_TIMESTAMP,
      updated_at: PRE_1970_TIMESTAMP,
      status: 'ready',
      filename: 'vintage-1950.mp3',
      custom_fields: {},
    };

    entrySubject.next(entry);

    const formValue = String(component.editForm.get('timestamp')?.value);
    expect(formValue.startsWith('1950-'))
      .withContext(`expected a 1950-... datetime string, got ${formValue}`)
      .toBeTrue();
    expect(formValue.startsWith('0001-'))
      .withContext('pre-1970 timestamp must never become year 0001')
      .toBeFalse();
    expect(formValue).toContain(':45');

    // Unrelated edit omits/preserves the pre-1970 timestamp
    component.editForm.get('filename')?.setValue('vintage-renamed.mp3');
    component.onSubmit();

    expect(updateEntrySpy).toHaveBeenCalledTimes(1);
    const firstPayload = updateEntrySpy.calls.mostRecent().args[2] as Partial<Entry>;
    const effectiveTimestamp =
      firstPayload.timestamp === undefined ? entry.timestamp : firstPayload.timestamp;
    expect(effectiveTimestamp).toBe(PRE_1970_TIMESTAMP);

    // Dirty timestamp also round-trips back to the exact pre-1970 epoch ms
    component.editForm.get('timestamp')?.markAsDirty();
    component.onSubmit();

    expect(updateEntrySpy).toHaveBeenCalledTimes(2);
    const secondPayload = updateEntrySpy.calls.mostRecent().args[2] as Partial<Entry>;
    expect(secondPayload.timestamp).toBe(PRE_1970_TIMESTAMP);
  });

  it('clears currentEntry when selectedEntry$ emits null or a different database_id', () => {
    entrySubject.next({ ...makeEntry(1, { artist: 'A' }), database_id: 'db1' });
    expect(component.currentEntry?.id).toBe(1);

    entrySubject.next(null);
    expect(component.currentEntry).toBeNull();

    entrySubject.next({ ...makeEntry(2, { artist: 'B' }), database_id: 'db2' });
    expect(component.currentEntry).toBeNull();
  });

  it('clears currentEntry when selectedDatabase$ switches away from currentEntry.database_id', () => {
    entrySubject.next({ ...makeEntry(1, { artist: 'A' }), database_id: 'db1' });
    expect(component.currentEntry?.id).toBe(1);

    const otherDb: Database = {
      ...musicDb,
      id: 'db2',
      name: 'Photos',
      content_type: ContentType.Image,
      custom_fields: [{ id: 10, name: 'camera', type: 'TEXT' }],
    };
    dbSubject.next(otherDb);

    expect(component.currentEntry).toBeNull();
    expect(component.editForm.get(['custom_fields', 'camera'])).toBeTruthy();
    expect(component.editForm.get(['custom_fields', 'artist'])).toBeNull();
  });

  it('sends explicit null when clearing an existing INTEGER/REAL custom field value', () => {
    const entry = makeEntry(1, { artist: 'X', rating: 5 });
    entrySubject.next(entry);

    field('rating')?.setValue('');
    component.onSubmit();

    expect(updateEntrySpy).toHaveBeenCalledTimes(1);
    const payload = updateEntrySpy.calls.mostRecent().args[2] as Partial<Entry>;
    expect('rating' in (payload.custom_fields ?? {})).toBeTrue();
    expect(payload.custom_fields?.['rating']).toBeNull();
  });

  it('validates INTEGER pattern and complete COORDINATE pairs', () => {
    const geoDb: Database = {
      ...musicDb,
      id: 'db-geo',
      custom_fields: [
        { id: 1, name: 'rating', type: 'INTEGER' },
        { id: 2, name: 'location', type: 'COORDINATE' },
      ],
    };
    dbSubject.next(geoDb);
    entrySubject.next(makeEntry(10, {}));

    field('rating')?.setValue(3.5);
    expect(field('rating')?.invalid).toBeTrue();
    expect(component.editForm.invalid).toBeTrue();

    field('rating')?.setValue(-4);
    expect(field('rating')?.valid).toBeTrue();

    const coordGroup = field('location');
    coordGroup?.patchValue({ latitude: 48.1, longitude: null });
    expect(coordGroup?.invalid).toBeTrue();
    expect(coordGroup?.hasError('incompleteCoordinate')).toBeTrue();
    expect(component.editForm.invalid).toBeTrue();

    coordGroup?.patchValue({ latitude: 48.1, longitude: 11.5 });
    expect(coordGroup?.valid).toBeTrue();
    expect(component.editForm.valid).toBeTrue();

    coordGroup?.patchValue({ latitude: null, longitude: null });
    expect(coordGroup?.valid).toBeTrue();
  });

  it('returns empty timestamp string instead of throwing on invalid Date', () => {
    const invalidEntry = { ...makeEntry(99, {}), timestamp: Number.NaN };
    expect(() => entrySubject.next(invalidEntry)).not.toThrow();
    expect(component.editForm.get('timestamp')?.value).toBe('');
  });

  it('resets isLoading on updateEntry error without closing the modal', () => {
    const modalSpy = TestBed.inject(ModalService) as jasmine.SpyObj<ModalService>;
    updateEntrySpy.and.returnValue(throwError(() => new Error('update failed')));

    entrySubject.next(makeEntry(1, { artist: 'X' }));
    component.onSubmit();

    expect(component.isLoading).toBeFalse();
    expect(modalSpy.close).not.toHaveBeenCalled();
  });
});

describe('EditEntryModalComponent — M6 close guard & session generation', () => {
  let fixture: ComponentFixture<EditEntryModalComponent>;
  let component: EditEntryModalComponent;
  let modalService: ModalService;
  let dbSubject: BehaviorSubject<Database | null>;
  let entrySubject: BehaviorSubject<Entry | null>;
  let update$: Subject<Entry>;

  const db: Database = {
    id: 'db1',
    name: 'Music',
    content_type: ContentType.Audio,
    n_max_queued: 0,
    config: { create_preview: true, auto_conversion: '' },
    housekeeping: { interval: '1h', disk_space: '100G', max_age: '0' },
    custom_fields: [],
  };

  const entry: Entry = {
    id: 1,
    timestamp: Date.UTC(2026, 9, 6, 12, 0, 0),
    created_at: Date.UTC(2026, 9, 6, 12, 0, 0),
    updated_at: Date.UTC(2026, 9, 6, 12, 0, 0),
    status: 'ready',
    filename: 'track.mp3',
    custom_fields: {},
  };

  beforeEach(async () => {
    dbSubject = new BehaviorSubject<Database | null>(db);
    entrySubject = new BehaviorSubject<Entry | null>(entry);
    update$ = new Subject<Entry>();

    const entrySpy = jasmine.createSpyObj<EntryService>('EntryService', ['updateEntry']);
    entrySpy.selectedEntry$ = entrySubject.asObservable();
    entrySpy.updateEntry.and.returnValue(update$.asObservable());

    const databaseSpy = jasmine.createSpyObj<DatabaseService>('DatabaseService', [
      'selectDatabase',
    ]);
    databaseSpy.selectedDatabase$ = dbSubject.asObservable();

    await TestBed.configureTestingModule({
      imports: [EditEntryModalComponent],
      providers: [
        { provide: EntryService, useValue: entrySpy },
        { provide: DatabaseService, useValue: databaseSpy },
        { provide: ModalService, useValue: new ModalService() },
      ],
    })
      .overrideComponent(EditEntryModalComponent, {
        remove: { imports: [ModalComponent] },
        add: { schemas: [CUSTOM_ELEMENTS_SCHEMA] },
      })
      .compileComponents();

    fixture = TestBed.createComponent(EditEntryModalComponent);
    component = fixture.componentInstance;
    modalService = TestBed.inject(ModalService);
    fixture.detectChanges();
  });

  it('blocks ModalService.close via close guard while isLoading and ignores stale PATCH completion across re-open', () => {
    const events: string[] = [];
    const sub = modalService
      .getModalEvents(EditEntryModalComponent.MODAL_ID)
      .subscribe((e) => events.push(e.action));

    modalService.open(EditEntryModalComponent.MODAL_ID);
    component.onSubmit();
    expect(component.isLoading).toBeTrue();

    events.length = 0;
    modalService.close(EditEntryModalComponent.MODAL_ID);
    expect(events).not.toContain('close');

    // Re-open starts a new modal session and clears isLoading
    modalService.open(EditEntryModalComponent.MODAL_ID);
    expect(component.isLoading).toBeFalse();

    const closeSpy = spyOn(modalService, 'close').and.callThrough();

    // Stale PATCH from session 1 completes
    update$.next(entry);
    update$.complete();

    expect(closeSpy).not.toHaveBeenCalled();
    expect(component.isLoading).toBeFalse();

    // Unregisters close guard on destroy
    component.isLoading = true;
    fixture.destroy();
    events.length = 0;
    modalService.close(EditEntryModalComponent.MODAL_ID);
    expect(events).toContain('close');

    sub.unsubscribe();
  });
});
