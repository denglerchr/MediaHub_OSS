// frontend/src/app/components/entry-list/entry-list.component.spec.ts
// Regression specs for FE-012: one failed request must never permanently brick the
// entry list — errors stay inside the inner stream and the list recovers on retry.
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NO_ERRORS_SCHEMA } from '@angular/core';
import { ActivatedRoute, convertToParamMap } from '@angular/router';
import { defer, merge, of, Subject, throwError } from 'rxjs';
import { EntryListComponent } from './entry-list.component';
import { PageHeaderComponent } from '../../../shared/components/page-header/page-header.component';
import { EntryFilterComponent } from '../entry-filter/entry-filter.component';
import { EntryGridComponent } from '../entry-grid/entry-grid.component';
import { EntryListViewComponent } from '../entry-list-view/entry-list-view.component';
import { FullscreenPlayerComponent } from '../fullscreen-player/fullscreen-player.component';
import { FileDragDropDirective } from '../../../shared/directives/file-drag-drop.directive';
import { DatabaseService } from '../../../core/services/database.service';
import { EntryService } from '../../../core/services/entry.service';
import { AuthService } from '../../../core/services/auth.service';
import { ModalService } from '../../../core/services/modal.service';
import { NotificationService } from '../../../core/services/notification.service';
import { UploadEntryModalComponent } from '../upload-entry-modal/upload-entry-modal.component';
import { Database, Entry, User } from '../../../core/models';

describe('EntryListComponent — FE-012/FE-015/FE-021 regressions', () => {
  let fixture: ComponentFixture<EntryListComponent>;
  let component: EntryListComponent;
  let searchSpy: jasmine.Spy;
  let selectDatabaseSpy: jasmine.Spy;
  let refreshListSpy: jasmine.Spy;
  let bulkDeleteSpy: jasmine.Spy;
  let bulkExportSpy: jasmine.Spy;
  let refreshRequiredSubject: Subject<void>;
  let entryCreatedSubject: Subject<Entry>;
  let entryUpdatedSubject: Subject<Entry>;
  let paramMapSubject: Subject<ReturnType<typeof convertToParamMap>>;
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
    bulkDeleteSpy = jasmine.createSpy('bulkDeleteEntries').and.returnValue(of({}));
    bulkExportSpy = jasmine
      .createSpy('bulkExportEntries')
      .and.returnValue(of(new Blob(['zip'], { type: 'application/zip' })));
    entryCreatedSubject = new Subject<Entry>();
    entryUpdatedSubject = new Subject<Entry>();
    refreshRequiredSubject = new Subject<void>();
    paramMapSubject = new Subject();

    const entryServiceMock = {
      searchEntries: searchSpy,
      refreshRequired$: refreshRequiredSubject.asObservable(),
      entryCreated$: entryCreatedSubject.asObservable(),
      entryUpdated$: entryUpdatedSubject.asObservable(),
      triggerImageListRefresh: refreshListSpy,
      bulkDeleteEntries: bulkDeleteSpy,
      bulkExportEntries: bulkExportSpy,
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
      imports: [EntryListComponent],
      providers: [
        {
          provide: ActivatedRoute,
          useValue: {
            paramMap: merge(of(convertToParamMap({ id: 'db1' })), paramMapSubject.asObservable()),
          },
        },
        { provide: DatabaseService, useValue: databaseServiceMock },
        { provide: EntryService, useValue: entryServiceMock },
        { provide: AuthService, useValue: authServiceMock },
        {
          provide: ModalService,
          useValue: jasmine.createSpyObj('ModalService', ['open', 'close', 'confirm']),
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
    })
      .overrideComponent(EntryListComponent, {
        remove: {
          imports: [
            PageHeaderComponent,
            EntryFilterComponent,
            EntryGridComponent,
            EntryListViewComponent,
            FullscreenPlayerComponent,
            FileDragDropDirective,
          ],
        },
        add: { schemas: [NO_ERRORS_SCHEMA] },
      })
      .compileComponents();

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

  it('renders the Retry button in the DOM when selectDatabase fails (even though canView is false)', () => {
    selectDatabaseSpy.and.returnValue(throwError(() => new Error('db fetch failed')));

    component.ngOnInit();
    fixture.detectChanges();

    expect(component.canView).toBeFalse();
    expect(component.loadError).toBeTrue();
    const retryBtn: HTMLButtonElement | null =
      Array.from(
        fixture.nativeElement.querySelectorAll('button') as NodeListOf<HTMLButtonElement>,
      ).find((b) => b.textContent?.includes('Retry')) ?? null;
    expect(retryBtn)
      .withContext('Retry button must be visible when selectDatabase fails')
      .toBeTruthy();

    selectDatabaseSpy.and.returnValue(of(db));
    searchSpy.and.returnValue(of([makeEntry(10)]));
    retryBtn!.click();
    fixture.detectChanges();

    expect(component.loadError).toBeFalse();
    expect(component.entriesToShow.map((e) => e.id)).toEqual([10]);
  });

  it('ignores entryUpdated$ events from a different database on entry ID collision', () => {
    searchSpy.and.returnValue(of([makeEntry(1)]));
    component.ngOnInit();

    entryUpdatedSubject.next({
      ...makeEntry(1),
      database_id: 'other-db',
      filename: 'hijacked.jpg',
    });

    expect(component.entriesToShow[0].filename).toBe('file-1.jpg');
  });

  it('configures status filter with type STATUS', () => {
    component.ngOnInit();

    const statusFilter = component.availableFilters.find((f) => f.name === 'status');
    expect(statusFilter).toEqual({ name: 'status', type: 'STATUS' });
  });

  it('opens fullscreen player synchronously when FullscreenSettingsModal closes with settings', () => {
    component.ngOnInit();
    const modalSpy = TestBed.inject(ModalService) as jasmine.SpyObj<ModalService>;
    modalSpy.open.and.returnValue(
      of({ delaySeconds: 10, entryLimit: 25, shuffle: false, repeat: true }),
    );

    component.openFullscreenSettingsModal();

    expect(component.showFullscreenPlayer).toBeTrue();
    expect(component.fullscreenSettings).toEqual({
      delaySeconds: 10,
      entryLimit: 25,
      shuffle: false,
      repeat: true,
    });
  });

  it('caps entriesToShow to imagesPerPage and sets hasNextPage when entryCreated$ overflows page 1', () => {
    component.imagesPerPage = 2;
    searchSpy.and.returnValue(of([makeEntry(2), makeEntry(1)]));
    component.ngOnInit();
    component.imagesPerPage = 2;

    entryCreatedSubject.next(makeEntry(3));

    expect(component.entriesToShow.map((e) => e.id)).toEqual([3, 2]);
    expect(component.hasNextPage).toBeTrue();
  });

  it('blocks bulk delete when a selected entry is still processing', () => {
    const processingEntry = { ...makeEntry(1), status: 'processing' };
    searchSpy.and.returnValue(of([processingEntry]));
    component.ngOnInit();
    component.selectedEntryIds = new Set([1]);

    const notifSpy = TestBed.inject(NotificationService) as jasmine.SpyObj<NotificationService>;
    const modalSpy = TestBed.inject(ModalService) as jasmine.SpyObj<ModalService>;

    component.onBulkDelete();

    expect(notifSpy.showError).toHaveBeenCalledWith('Cannot delete: entry 1 is still processing.');
    expect(modalSpy.confirm).not.toHaveBeenCalled();
    expect(modalSpy.open).not.toHaveBeenCalled();
  });

  it('does not show a duplicate error toast when bulkExportEntries fails', () => {
    bulkExportSpy.and.returnValue(throwError(() => new Error('Export failed')));
    searchSpy.and.returnValue(of([makeEntry(1)]));
    component.ngOnInit();
    component.selectedEntryIds = new Set([1]);

    const notifSpy = TestBed.inject(NotificationService) as jasmine.SpyObj<NotificationService>;
    component.onBulkDownload();

    expect(notifSpy.showError).not.toHaveBeenCalled();
    expect(component.isBulkProcessing).toBeFalse();
  });

  it('resets imagesPerPage to 200 when the route database id changes', () => {
    component.ngOnInit();
    component.imagesPerPage = 50;

    selectDatabaseSpy.and.returnValue(of({ ...db, id: 'db2', name: 'DB Two' } as Database));
    paramMapSubject.next(convertToParamMap({ id: 'db2' }));

    expect(component.imagesPerPage).toBe(200);
  });

  it('routes both onFabFileSelected and onFilesDropped through UploadEntryModalData { files }', () => {
    component.ngOnInit();
    const modalSpy = TestBed.inject(ModalService) as jasmine.SpyObj<ModalService>;
    const img1 = new File(['a'], 'a.jpg', { type: 'image/jpeg' });
    const img2 = new File(['b'], 'b.png', { type: 'image/png' });
    const bad = new File(['c'], 'c.txt', { type: 'text/plain' });

    const fileList = {
      0: img1,
      1: bad,
      length: 2,
      item: (i: number) => (i === 0 ? img1 : bad),
    } as unknown as FileList;
    component.onFabFileSelected({ target: { files: fileList } } as unknown as Event);
    expect(modalSpy.open).toHaveBeenCalledWith(UploadEntryModalComponent.MODAL_ID, {
      files: [img1],
    });

    modalSpy.open.calls.reset();
    component.onFilesDropped([img1, img2]);
    expect(modalSpy.open).toHaveBeenCalledWith(UploadEntryModalComponent.MODAL_ID, {
      files: [img1, img2],
    });
  });

  it('C2: tears down refreshRequired$ subscription when component is destroyed', () => {
    fixture.detectChanges();
    expect(searchSpy).toHaveBeenCalledTimes(1);
    searchSpy.calls.reset();

    fixture.destroy();
    refreshRequiredSubject.next();

    expect(searchSpy).not.toHaveBeenCalled();
  });

  it('H8: closes fullscreen player on route database switch and on null database id', () => {
    fixture.detectChanges();
    component.fullscreenSettings = {
      delaySeconds: 5,
      entryLimit: 10,
      shuffle: false,
      repeat: false,
    };
    component.showFullscreenPlayer = true;

    selectDatabaseSpy.and.returnValue(of({ ...db, id: 'db2', name: 'DB Two' } as Database));
    paramMapSubject.next(convertToParamMap({ id: 'db2' }));

    expect(component.showFullscreenPlayer).toBeFalse();
    expect(component.fullscreenSettings).toBeNull();

    // Also closes safely when id becomes null
    component.fullscreenSettings = {
      delaySeconds: 5,
      entryLimit: 10,
      shuffle: false,
      repeat: false,
    };
    component.showFullscreenPlayer = true;
    paramMapSubject.next(convertToParamMap({}));
    fixture.detectChanges();

    expect(component.showFullscreenPlayer).toBeFalse();
    expect(component.fullscreenSettings).toBeNull();
    expect(component.dbId).toBeNull();
    expect(component.currentDb).toBeNull();
    expect(fixture.nativeElement.querySelector('app-fullscreen-player')).toBeNull();
  });
});
