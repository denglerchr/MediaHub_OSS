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
import { ReactiveFormsModule, FormsModule } from '@angular/forms';
import { BehaviorSubject, of } from 'rxjs';
import { EditEntryModalComponent } from './edit-entry-modal.component';
import { EntryService } from '../../services/entry.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { ContentType, Database, Entry } from '../../models';

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

    const modalSpy = jasmine.createSpyObj<ModalService>('ModalService', ['close']);

    await TestBed.configureTestingModule({
      declarations: [EditEntryModalComponent],
      imports: [ReactiveFormsModule, FormsModule],
      schemas: [CUSTOM_ELEMENTS_SCHEMA], // <app-modal> wrapper is not under test
      providers: [
        { provide: EntryService, useValue: entrySpy },
        { provide: DatabaseService, useValue: databaseSpy },
        { provide: ModalService, useValue: modalSpy },
      ],
    }).compileComponents();

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
});
