// frontend/src/app/components/entry-list/entry-list.component.spec.ts
// Regression specs for FE-012: one failed request must never permanently brick the
// entry list — errors stay inside the inner stream and the list recovers on retry.
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NO_ERRORS_SCHEMA } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute, convertToParamMap } from '@angular/router';
import { defer, of, Subject, throwError } from 'rxjs';
import { EntryListComponent } from './entry-list.component';
import { DatabaseService } from '../../services/database.service';
import { EntryService } from '../../services/entry.service';
import { AuthService } from '../../services/auth.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import { Database, Entry, User } from '../../models';

describe('EntryListComponent — FE-012/FE-015/FE-021 regressions', () => {
  let fixture: ComponentFixture<EntryListComponent>;
  let component: EntryListComponent;
  let searchSpy: jasmine.Spy;
  let selectDatabaseSpy: jasmine.Spy;
  let refreshListSpy: jasmine.Spy;
  let entryCreatedSubject: Subject<Entry>;
  let user: User;

  const db = {
    id: 'db1',
    name: 'DB One',
    content_type: 'image',
    custom_fields: [],
  } as unknown as Database;

  const makeEntry = (id: number): Entry =>
    ({
      id,
      timestamp: id * 1000,
      created_at: id * 1000,
      updated_at: id * 1000,
      status: 'ready',
      database_id: 'db1',
      filename: `file-${id}.jpg`,
    }) as Entry;

  // Default: an admin (full access). Tests may reassign `user` before ngOnInit().
  const makeUser = (): User =>
    ({ id: 'u1', username: 'alice', is_admin: true, permissions: [] }) as unknown as User;

  beforeEach(async () => {
    user = makeUser();
    searchSpy = jasmine.createSpy('searchEntries').and.returnValue(of([] as Entry[]));
    selectDatabaseSpy = jasmine.createSpy('selectDatabase').and.returnValue(of(db));
    refreshListSpy = jasmine.createSpy('triggerImageListRefresh');
    entryCreatedSubject = new Subject<Entry>();

    const entryServiceMock = {
      searchEntries: searchSpy,
      refreshRequired$: new Subject<void>(),
      entryCreated$: entryCreatedSubject.asObservable(),
      entryUpdated$: new Subject<Entry>(),
      triggerImageListRefresh: refreshListSpy,
    };

    const databaseServiceMock = {
      selectDatabase: selectDatabaseSpy,
      selectedDatabase$: of(db),
    };

    // defer: the tests below reassign `user` before ngOnInit() subscribes.
    const authServiceMock = {
      currentUser$: defer(() => of(user)),
    };

    await TestBed.configureTestingModule({
      declarations: [EntryListComponent],
      imports: [CommonModule],
      schemas: [NO_ERRORS_SCHEMA], // child components are not under test
      providers: [
        { provide: ActivatedRoute, useValue: { paramMap: of(convertToParamMap({ id: 'db1' })) } },
        { provide: DatabaseService, useValue: databaseServiceMock },
        { provide: EntryService, useValue: entryServiceMock },
        { provide: AuthService, useValue: authServiceMock },
        {
          provide: ModalService,
          useValue: jasmine.createSpyObj('ModalService', ['open', 'close']),
        },
        {
          provide: NotificationService,
          useValue: jasmine.createSpyObj('NotificationService', [
            'showError',
            'showSuccess',
            'showInfo',
            'showGlobalError',
            'clearGlobalError',
          ]),
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(EntryListComponent);
    component = fixture.componentInstance;
  });

  it('FE-012: a failed search sets loadError (not "no entries") and the list recovers on retry', () => {
    searchSpy.and.returnValue(throwError(() => new Error('network down')));

    component.ngOnInit();

    expect(component.loadError).toBeTrue(); // error, not an empty result
    expect(component.isLoading).toBeFalse();
    expect(component.entriesToShow).toEqual([]);

    // The route subscription must still be alive: a retry re-runs the search.
    searchSpy.and.returnValue(of([makeEntry(1), makeEntry(2)]));
    component.retryLoad();

    expect(component.loadError).toBeFalse();
    expect(component.entriesToShow.length).toBe(2);
    expect(component.isLoading).toBeFalse();
  });

  it('FE-012: a failed search followed by another failure keeps the error state (no silent empty list)', () => {
    searchSpy.and.returnValue(throwError(() => new Error('network down')));
    component.ngOnInit();
    expect(component.loadError).toBeTrue();

    searchSpy.and.returnValue(throwError(() => new Error('still down')));
    component.retryLoad();

    expect(component.loadError).toBeTrue();
    expect(component.entriesToShow).toEqual([]);
    expect(component.isLoading).toBeFalse();
  });

  it('FE-012: a successful empty result is NOT reported as a load error', () => {
    searchSpy.and.returnValue(of([] as Entry[]));
    component.ngOnInit();

    expect(component.loadError).toBeFalse();
    expect(component.entriesToShow).toEqual([]);
    expect(component.isLoading).toBeFalse();
  });

  it('FE-012: a failed database lookup does not brick the route pipeline and recovers on retry', () => {
    selectDatabaseSpy.and.returnValue(throwError(() => new Error('db fetch failed')));

    component.ngOnInit();

    expect(component.loadError).toBeTrue();
    expect(component.isLoading).toBeFalse();

    // Recovery: the database is reachable again — retry re-runs the whole chain.
    selectDatabaseSpy.and.returnValue(of(db));
    searchSpy.and.returnValue(of([makeEntry(3)]));
    component.retryLoad();

    expect(selectDatabaseSpy).toHaveBeenCalledTimes(2);
    expect(component.loadError).toBeFalse();
    expect(component.entriesToShow.length).toBe(1);
  });

  // --- FE-021: entryCreated$ injection rules --------------------------------

  it('FE-021: a new entry is injected on page 1 of an unfiltered view of its own database', () => {
    searchSpy.and.returnValue(of([makeEntry(1)]));
    component.ngOnInit();
    expect(component.entriesToShow.map((e) => e.id)).toEqual([1]);

    entryCreatedSubject.next(makeEntry(2)); // newer timestamp -> sorts to the top

    expect(component.entriesToShow.map((e) => e.id))
      .withContext('the fresh entry must be spliced into the visible first page')
      .toEqual([2, 1]);
    expect(refreshListSpy).not.toHaveBeenCalled();
  });

  it('FE-021: on page > 1 the entry is NOT spliced in (offset pagination) — a silent refetch runs instead', () => {
    searchSpy.and.returnValue(of([makeEntry(1)]));
    component.ngOnInit();
    component.currentPage = 2;

    entryCreatedSubject.next(makeEntry(2));

    expect(component.entriesToShow.map((e) => e.id))
      .withContext('splicing into page > 1 shifts rows and shows duplicates on Next/Previous')
      .toEqual([1]);
    expect(refreshListSpy)
      .withContext('the server-side view must be refreshed instead')
      .toHaveBeenCalledTimes(1);
  });

  it('FE-021: with an active filter the entry is NOT spliced in — a silent refetch runs instead', () => {
    searchSpy.and.returnValue(of([makeEntry(1)]));
    component.ngOnInit();
    component.onFilterApplied({ filter: { operator: 'and', conditions: [] }, limit: 200 });

    entryCreatedSubject.next(makeEntry(2));

    expect(component.entriesToShow.map((e) => e.id))
      .withContext('the injected entry may not even match the filter')
      .toEqual([1]);
    expect(refreshListSpy).toHaveBeenCalledTimes(1);
  });

  it('FE-021: an entry from another database is ignored entirely', () => {
    searchSpy.and.returnValue(of([makeEntry(1)]));
    component.ngOnInit();

    entryCreatedSubject.next({ ...makeEntry(9), database_id: 'other-db' });

    expect(component.entriesToShow.map((e) => e.id)).toEqual([1]);
    expect(refreshListSpy).not.toHaveBeenCalled();
  });

  // --- FE-015: page affordances are gated by permission ---------------------

  it('FE-015: a can_create-only user gets the upload UI and no entries search (list requires can_view)', () => {
    user = {
      id: 'u2',
      username: 'up-only',
      is_admin: false,
      account_type: 'local',
      permissions: [
        {
          database_id: 'db1',
          can_view: false,
          can_create: true,
          can_edit: false,
          can_delete: false,
          can_admin: false,
        },
      ],
    } as unknown as User;

    component.ngOnInit();
    fixture.detectChanges();

    expect(searchSpy)
      .withContext('searching entries requires can_view — it must not even be attempted')
      .not.toHaveBeenCalled();
    expect(component.canView).toBeFalse();
    expect(component.canCreate).toBeTrue();

    const fab: HTMLElement | null = fixture.nativeElement.querySelector('button.upload-fab');
    expect(fab).withContext('the upload FAB must be reachable with can_create only').toBeTruthy();
    expect(fixture.nativeElement.textContent)
      .withContext('the gated list must explain itself')
      .toContain('You do not have permission to view entries');

    const modalSpy = TestBed.inject(ModalService) as jasmine.SpyObj<ModalService>;
    component.openUploadModal();
    expect(modalSpy.open)
      .withContext('the upload UI must actually open for a can_create-only user')
      .toHaveBeenCalledWith('uploadEntryModal');
  });
});
