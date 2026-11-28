// frontend/src/app/components/entry-detail-modal/entry-detail-modal.component.spec.ts
// Regression specs for FE-012 (one failed request must not brick the detail modal),
// FE-023 (media keeps playing after the modal closes) and FE-024 (object URL leak on
// ESC/X/overlay dismissal).
import { ComponentFixture, TestBed, fakeAsync, tick } from '@angular/core/testing';
import { NO_ERRORS_SCHEMA } from '@angular/core';
import { BehaviorSubject, Observable, Subject, of, throwError } from 'rxjs';
import { EntryDetailModalComponent } from './entry-detail-modal.component';
import { ModalComponent } from '../../../shared/components/modal/modal.component';
import { SecureImageDirective } from '../../../shared/directives/secure-image.directive';
import { EntryService } from '../../../core/services/entry.service';
import { DatabaseService } from '../../../core/services/database.service';
import { AuthService } from '../../../core/services/auth.service';
import { ModalService } from '../../../core/services/modal.service';
import { NotificationService } from '../../../core/services/notification.service';
import { Database, Entry, User } from '../../../core/models';

describe('EntryDetailModalComponent — FE-012/FE-023/FE-024 regressions', () => {
  let fixture: ComponentFixture<EntryDetailModalComponent>;
  let component: EntryDetailModalComponent;
  let modalService: ModalService;
  let selectedEntry$: BehaviorSubject<Entry | null>;
  let selectedDatabase$: BehaviorSubject<Database | null>;
  let getEntryMetaSpy: jasmine.Spy;
  let getEntryFileBlobSpy: jasmine.Spy;
  let getEntryFileUrlSpy: jasmine.Spy;
  let deleteEntrySpy: jasmine.Spy;
  let forceAccessTokenRefreshSpy: jasmine.Spy;
  let clearSelectedEntrySpy: jasmine.Spy;
  let notificationService: jasmine.SpyObj<NotificationService>;

  const db = {
    id: 'db1',
    name: 'DB One',
    content_type: 'audio',
    custom_fields: [],
  } as unknown as Database;

  const user = { id: 'u1', username: 'alice', is_admin: true, permissions: [] } as unknown as User;

  const makeEntry = (mime: string): Entry =>
    ({
      id: 7,
      timestamp: 1000,
      created_at: 1000,
      updated_at: 1000,
      status: 'ready',
      database_id: 'db1',
      mime_type: mime,
      filename: 'file.bin',
    }) as Entry;

  beforeEach(async () => {
    selectedEntry$ = new BehaviorSubject<Entry | null>(null);
    selectedDatabase$ = new BehaviorSubject<Database | null>(db);
    getEntryMetaSpy = jasmine.createSpy('getEntryMeta');
    getEntryFileBlobSpy = jasmine
      .createSpy('getEntryFileBlob')
      .and.returnValue(of(new Blob(['x'])));
    getEntryFileUrlSpy = jasmine
      .createSpy('getEntryFileUrl')
      .and.returnValue('/api/database/db1/entry/7/file');
    // FE-011/N-D1: the media error path must force a token refresh before it
    // regenerates the URL — the spy rotates a fake access token on refresh.
    forceAccessTokenRefreshSpy = jasmine
      .createSpy('forceAccessTokenRefresh')
      .and.callFake(() => of(undefined));
    clearSelectedEntrySpy = jasmine
      .createSpy('clearSelectedEntry')
      .and.callFake(() => selectedEntry$.next(null));
    deleteEntrySpy = jasmine.createSpy('deleteEntry').and.returnValue(of({ message: 'ok' }));
    notificationService = jasmine.createSpyObj('NotificationService', [
      'showError',
      'showSuccess',
      'showInfo',
      'showGlobalError',
      'clearGlobalError',
    ]);

    const entryServiceMock = {
      selectedEntry$,
      getEntryMeta: getEntryMetaSpy,
      getEntryFileBlob: getEntryFileBlobSpy,
      getEntryFileUrl: getEntryFileUrlSpy,
      getEntryPreviewUrl: jasmine
        .createSpy('getEntryPreviewUrl')
        .and.returnValue('/api/database/db1/entry/7/preview'),
      selectEntry: jasmine
        .createSpy('selectEntry')
        .and.callFake((e: Entry) => selectedEntry$.next(e)),
      clearSelectedEntry: clearSelectedEntrySpy,
      deleteEntry: deleteEntrySpy,
    };

    await TestBed.configureTestingModule({
      imports: [EntryDetailModalComponent],
      providers: [
        { provide: EntryService, useValue: entryServiceMock },
        { provide: DatabaseService, useValue: { selectedDatabase$ } },
        {
          provide: AuthService,
          useValue: { currentUser$: of(user), forceAccessTokenRefresh: forceAccessTokenRefreshSpy },
        },
        { provide: ModalService, useValue: new ModalService() },
        { provide: NotificationService, useValue: notificationService },
      ],
    })
      .overrideComponent(EntryDetailModalComponent, {
        remove: { imports: [ModalComponent, SecureImageDirective] },
        add: { schemas: [NO_ERRORS_SCHEMA] },
      })
      .compileComponents();

    fixture = TestBed.createComponent(EntryDetailModalComponent);
    component = fixture.componentInstance;
    modalService = TestBed.inject(ModalService);
    fixture.detectChanges(); // runs ngOnInit + renders the template
  });

  it('FE-024: closing via the modal service (ESC/X/overlay path) revokes the object URL and resets state', () => {
    const createSpy = spyOn(URL, 'createObjectURL').and.returnValue('blob:fake-image');
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    getEntryMetaSpy.and.returnValue(of(makeEntry('image/jpeg')));
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('image/jpeg'));
    fixture.detectChanges();

    expect(createSpy).toHaveBeenCalled();
    expect(component.fileUrl).toBeTruthy();

    // ESC / X / overlay close through ModalComponent -> ModalService.close(id).
    modalService.close(EntryDetailModalComponent.MODAL_ID);
    fixture.detectChanges();

    expect(revokeSpy).toHaveBeenCalledWith('blob:fake-image');
    expect(component.fileUrl).toBeNull();
    expect(component.entryForMetadata).toBeNull();
    expect(clearSelectedEntrySpy).toHaveBeenCalled();
  });

  it('FE-023: closing the modal stops audio playback and detaches the media source', () => {
    const pauseSpy = spyOn(HTMLMediaElement.prototype, 'pause');

    getEntryMetaSpy.and.returnValue(of(makeEntry('audio/mpeg')));
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('audio/mpeg'));
    fixture.detectChanges();

    const audioEl: HTMLAudioElement | null = fixture.nativeElement.querySelector('audio');
    expect(audioEl).toBeTruthy();

    modalService.close(EntryDetailModalComponent.MODAL_ID);
    fixture.detectChanges();

    expect(pauseSpy).toHaveBeenCalled();
    expect(audioEl!.getAttribute('src')).toBeNull();
  });

  it('FE-012: a failed meta load shows the error state and retry recovers (the modal is not bricked)', () => {
    getEntryMetaSpy.and.returnValue(throwError(() => new Error('network down')));

    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('video/mp4'));
    fixture.detectChanges();

    expect(component.loadError).toBeTrue();
    expect(component.isLoadingFile).toBeFalse();

    // The pipeline is still alive: retry re-requests the same entry.
    getEntryMetaSpy.and.returnValue(of(makeEntry('video/mp4')));
    component.retryLoad();
    fixture.detectChanges();

    expect(component.loadError).toBeFalse();
    expect(component.entryForMetadata?.id).toBe(7);
  });

  it('FE-012: a failed image blob load does not leave the modal spinning forever', () => {
    getEntryMetaSpy.and.returnValue(of(makeEntry('image/jpeg')));
    getEntryFileBlobSpy.and.returnValue(throwError(() => new Error('blob fetch failed')));
    spyOn(console, 'error');

    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('image/jpeg'));
    fixture.detectChanges();

    expect(component.isLoadingFile).toBeFalse();
    // A subsequent entry must still load fine (pipeline alive).
    getEntryFileBlobSpy.and.returnValue(of(new Blob(['y'])));
    getEntryMetaSpy.and.returnValue(of(makeEntry('image/jpeg')));
    component.retryLoad();
    fixture.detectChanges();

    expect(component.loadError).toBeFalse();
    expect(component.fileUrl).toBeTruthy();
  });

  // --- FE-011/N-D1: token expiry during streaming ---

  const loadStreamableEntry = (): HTMLAudioElement => {
    getEntryMetaSpy.and.returnValue(of(makeEntry('audio/mpeg')));
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('audio/mpeg'));
    fixture.detectChanges();
    const audioEl: HTMLAudioElement | null = fixture.nativeElement.querySelector('audio');
    expect(audioEl).toBeTruthy();
    return audioEl!;
  };

  it('FE-011/N-D1: a media error forces a token refresh and retries with the renewed URL', () => {
    let token = 'expired-token';
    getEntryFileUrlSpy.and.callFake(() => `/api/database/db1/entry/7/file?token=${token}`);
    forceAccessTokenRefreshSpy.and.callFake(() => {
      token = 'fresh-token';
      return of(undefined);
    });

    const audioEl = loadStreamableEntry();
    expect(audioEl.getAttribute('src')).toContain('token=expired-token');

    // The 401 on the Range request never passed through the interceptor — the
    // stored token is still the expired one the URL was generated with.
    component.onMediaError(new Event('error'));
    fixture.detectChanges();

    expect(forceAccessTokenRefreshSpy).toHaveBeenCalled();
    expect(getEntryFileUrlSpy).toHaveBeenCalledTimes(2);
    expect(audioEl.getAttribute('src')).toContain('token=fresh-token');
    expect(notificationService.showError).not.toHaveBeenCalled();
  });

  it('FE-011: the failure is reported (and not retried) when the refresh yields no new URL', () => {
    // The refresh cannot renew the session (no refresh token / dead session): the
    // regenerated URL stays identical *after* the refresh — that, and only that,
    // is the fatal case.
    const audioEl = loadStreamableEntry();
    component.onMediaError(new Event('error'));
    fixture.detectChanges();

    expect(forceAccessTokenRefreshSpy).toHaveBeenCalled();
    expect(notificationService.showError).toHaveBeenCalledTimes(1);
    // The element keeps the old URL — there was nothing new to retry with.
    expect(audioEl.getAttribute('src')).toBe('/api/database/db1/entry/7/file');
  });

  it('FE-011: the toast appears only after the two real retries are spent', () => {
    let n = 0;
    getEntryFileUrlSpy.and.callFake(() => `/api/database/db1/entry/7/file?token=t${++n}`);
    forceAccessTokenRefreshSpy.and.returnValue(of(undefined));

    loadStreamableEntry();

    component.onMediaError(new Event('error'));
    component.onMediaError(new Event('error'));
    fixture.detectChanges();
    expect(notificationService.showError).not.toHaveBeenCalled();

    component.onMediaError(new Event('error'));
    fixture.detectChanges();
    expect(notificationService.showError).toHaveBeenCalledTimes(1);
  });

  // --- entry-modal staleness fix: metadata updates must be visible immediately ---

  it('shows updated metadata immediately when the entry is re-published, without reloading the media', () => {
    spyOn(URL, 'createObjectURL').and.returnValue('blob:fake-image');
    spyOn(URL, 'revokeObjectURL');

    getEntryMetaSpy.and.returnValue(
      of({
        ...makeEntry('image/jpeg'),
        filename: 'old-name.jpg',
        custom_fields: { artist: 'Old Artist' },
      } as Entry),
    );
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('image/jpeg'));
    fixture.detectChanges();

    expect(component.entryForMetadata?.filename).toBe('old-name.jpg');
    const metaCallsAfterLoad = getEntryMetaSpy.calls.count();
    const blobCallsAfterLoad = getEntryFileBlobSpy.calls.count();
    const fileUrlAfterLoad = component.fileUrl;

    // EntryService.updateEntry() re-publishes the PATCH response for the selected
    // entry after a successful save in the edit modal.
    selectedEntry$.next({
      ...makeEntry('image/jpeg'),
      filename: 'new-name.jpg',
      custom_fields: { artist: 'New Artist' },
    } as Entry);
    fixture.detectChanges();

    expect(component.entryForMetadata?.filename)
      .withContext('the modal must show the saved filename right away')
      .toBe('new-name.jpg');
    expect(component.entryForMetadata?.custom_fields?.['artist']).toBe('New Artist');

    const rendered = fixture.nativeElement.textContent as string;
    expect(rendered).toContain('new-name.jpg');

    // Metadata-only: the media is neither re-fetched nor re-created.
    expect(getEntryMetaSpy.calls.count()).toBe(metaCallsAfterLoad);
    expect(getEntryFileBlobSpy.calls.count()).toBe(blobCallsAfterLoad);
    expect(component.fileUrl).toBe(fileUrlAfterLoad);
    expect(URL.revokeObjectURL).not.toHaveBeenCalled();
  });

  it('a re-published DIFFERENT entry still goes through the full load path', () => {
    getEntryMetaSpy.and.returnValue(of(makeEntry('image/jpeg')));
    getEntryFileUrlSpy.and.returnValue('/api/database/db1/entry/7/file');
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('image/jpeg'));
    fixture.detectChanges();

    const metaCallsAfterLoad = getEntryMetaSpy.calls.count();

    getEntryMetaSpy.and.returnValue(
      of({ ...makeEntry('image/jpeg'), id: 8, filename: 'b.jpg' } as Entry),
    );
    getEntryFileUrlSpy.and.returnValue('/api/database/db1/entry/8/file');
    selectedEntry$.next({ ...makeEntry('image/jpeg'), id: 8, filename: 'b.jpg' } as Entry);
    fixture.detectChanges();

    expect(getEntryMetaSpy.calls.count()).toBeGreaterThan(metaCallsAfterLoad);
    expect(component.entryForMetadata?.id).toBe(8);
  });

  it('does NOT take the metadata-only shortcut when same entry ID belongs to a different database', () => {
    spyOn(URL, 'createObjectURL').and.returnValue('blob:fake-image');
    getEntryMetaSpy.and.returnValue(of(makeEntry('image/jpeg')));
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('image/jpeg'));
    fixture.detectChanges();

    const metaCallsAfterLoad = getEntryMetaSpy.calls.count();

    const db2 = { ...db, id: 'db2', name: 'DB Two' } as unknown as Database;
    const db2Entry = { ...makeEntry('image/jpeg'), database_id: 'db2' } as Entry;
    getEntryMetaSpy.and.returnValue(of(db2Entry));

    selectedDatabase$.next(db2);
    selectedEntry$.next(db2Entry);
    fixture.detectChanges();

    expect(getEntryMetaSpy.calls.count()).toBeGreaterThan(metaCallsAfterLoad);
    expect(component.entryForMetadata?.database_id).toBe('db2');
  });

  it('onDelete captures dbId and entryId before opening confirmation modal and handles delete errors', () => {
    getEntryMetaSpy.and.returnValue(of(makeEntry('audio/mpeg')));
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('audio/mpeg'));
    fixture.detectChanges();

    const confirm$ = new Subject<boolean>();
    spyOn(modalService, 'open').and.returnValue(confirm$.asObservable());
    const closeSpy = spyOn(modalService, 'close');
    deleteEntrySpy.and.returnValue(throwError(() => new Error('delete failed')));

    component.onDelete();
    // Mutate modal state before user confirms
    component.currentDatabase = null;
    component.entryForMetadata = null;

    confirm$.next(true);
    confirm$.complete();

    expect(deleteEntrySpy).toHaveBeenCalledWith('db1', 7);
    expect(component.isLoadingFile).toBeFalse();
    expect(closeSpy).not.toHaveBeenCalled();
  });

  it('onDelete cancels subscription on destroy', () => {
    getEntryMetaSpy.and.returnValue(of(makeEntry('audio/mpeg')));
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('audio/mpeg'));
    fixture.detectChanges();

    const confirm$ = new Subject<boolean>();
    spyOn(modalService, 'open').and.returnValue(confirm$.asObservable());

    component.onDelete();
    fixture.destroy();

    confirm$.next(true);
    expect(deleteEntrySpy).not.toHaveBeenCalled();
  });

  it('onDownload defers object URL revocation, resets isDownloading on error/teardown', fakeAsync(() => {
    getEntryMetaSpy.and.returnValue(of(makeEntry('audio/mpeg')));
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('audio/mpeg'));
    fixture.detectChanges();

    const createSpy = spyOn(URL, 'createObjectURL').and.returnValue('blob:download-1');
    const revokeSpy = spyOn(URL, 'revokeObjectURL');
    const anchor = document.createElement('a');
    const clickSpy = spyOn(anchor, 'click');
    const originalCreateElement = document.createElement.bind(document);
    spyOn(document, 'createElement').and.callFake((tagName: string) =>
      tagName === 'a' ? anchor : originalCreateElement(tagName),
    );

    component.onDownload();

    expect(createSpy).toHaveBeenCalled();
    expect(clickSpy).toHaveBeenCalled();
    expect(component.isDownloading).toBeFalse();
    expect(revokeSpy).not.toHaveBeenCalledWith('blob:download-1');

    tick(30_000);
    expect(revokeSpy).toHaveBeenCalledWith('blob:download-1');

    // Error path resets isDownloading without throwing
    getEntryFileBlobSpy.and.returnValue(throwError(() => new Error('download error')));
    expect(() => component.onDownload()).not.toThrow();
    expect(component.isDownloading).toBeFalse();

    // Teardown resets stuck isDownloading flag
    component.isDownloading = true;
    modalService.close(EntryDetailModalComponent.MODAL_ID);
    expect(component.isDownloading).toBeFalse();
  }));

  it('renders loading text when entryForMetadata is null and isLoadingFile is true', () => {
    component.entryForMetadata = null;
    component.isLoadingFile = true;
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).toContain('Loading entry details...');

    component.isLoadingFile = false;
    fixture.detectChanges();
    expect(fixture.nativeElement.textContent).not.toContain('Loading entry details...');
  });

  it('H1: emitting selectedEntry$ while the modal is closed does NOT call getEntryMeta, getEntryFileBlob, or URL.createObjectURL', () => {
    const createSpy = spyOn(URL, 'createObjectURL').and.returnValue('blob:unused');
    getEntryMetaSpy.and.returnValue(of(makeEntry('image/jpeg')));

    // Modal is closed (e.g. List View Edit flow sets selectedEntry$ without opening detail modal)
    selectedEntry$.next(makeEntry('image/jpeg'));
    fixture.detectChanges();

    expect(getEntryMetaSpy).not.toHaveBeenCalled();
    expect(getEntryFileBlobSpy).not.toHaveBeenCalled();
    expect(createSpy).not.toHaveBeenCalled();

    // Also remains gated after an open -> close cycle
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    fixture.detectChanges();
    expect(getEntryMetaSpy).toHaveBeenCalledTimes(1);
    expect(getEntryFileBlobSpy).toHaveBeenCalledTimes(1);

    modalService.close(EntryDetailModalComponent.MODAL_ID);
    fixture.detectChanges();
    getEntryMetaSpy.calls.reset();
    getEntryFileBlobSpy.calls.reset();
    createSpy.calls.reset();

    selectedEntry$.next(makeEntry('image/jpeg'));
    fixture.detectChanges();

    expect(getEntryMetaSpy).not.toHaveBeenCalled();
    expect(getEntryFileBlobSpy).not.toHaveBeenCalled();
    expect(createSpy).not.toHaveBeenCalled();
  });

  it('H1: closing the modal while getEntryMeta or getEntryFileBlob is in flight unsubscribes it immediately', () => {
    let metaUnsubscribed = false;
    getEntryMetaSpy.and.returnValue(
      new Observable(() => () => {
        metaUnsubscribed = true;
      }),
    );

    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('image/jpeg'));
    fixture.detectChanges();

    expect(getEntryMetaSpy).toHaveBeenCalledTimes(1);
    expect(metaUnsubscribed).toBeFalse();

    modalService.close(EntryDetailModalComponent.MODAL_ID);
    fixture.detectChanges();
    expect(metaUnsubscribed).toBeTrue();

    let blobUnsubscribed = false;
    getEntryMetaSpy.and.returnValue(of(makeEntry('image/jpeg')));
    getEntryFileBlobSpy.and.returnValue(
      new Observable(() => () => {
        blobUnsubscribed = true;
      }),
    );

    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('image/jpeg'));
    fixture.detectChanges();

    expect(getEntryFileBlobSpy).toHaveBeenCalledTimes(1);
    expect(blobUnsubscribed).toBeFalse();

    modalService.close(EntryDetailModalComponent.MODAL_ID);
    fixture.detectChanges();
    expect(blobUnsubscribed).toBeTrue();
  });

  it('H4: onMediaError ignores a stale refresh completion after switching to a different dbId with the same entryId', () => {
    const refresh$ = new Subject<void>();
    getEntryFileUrlSpy.and.returnValue('/api/database/db1/entry/7/file?token=expired');
    forceAccessTokenRefreshSpy.and.returnValue(refresh$);

    const audioEl = loadStreamableEntry();
    expect(audioEl.getAttribute('src')).toContain('token=expired');

    component.onMediaError(new Event('error'));
    expect(forceAccessTokenRefreshSpy).toHaveBeenCalledTimes(1);

    // Switch to another database that happens to have an entry with the same numeric ID (7)
    const db2 = { ...db, id: 'db2', name: 'DB Two' } as unknown as Database;
    const db2Entry = { ...makeEntry('audio/mpeg'), id: 7, database_id: 'db2' } as Entry;
    getEntryMetaSpy.and.returnValue(of(db2Entry));
    getEntryFileUrlSpy.and.returnValue('/api/database/db2/entry/7/file?token=db2-token');
    selectedDatabase$.next(db2);
    selectedEntry$.next(db2Entry);
    fixture.detectChanges();

    const urlCallsAfterSwitch = getEntryFileUrlSpy.calls.count();

    // Now the stale db1 refresh completes
    refresh$.next();
    refresh$.complete();
    fixture.detectChanges();

    expect(getEntryFileUrlSpy.calls.count()).toBe(urlCallsAfterSwitch);
    expect(audioEl.getAttribute('src')).toContain('token=db2-token');
    expect(notificationService.showError).not.toHaveBeenCalled();
  });

  it('renders <audio> preview for application/ogg instead of fallback', () => {
    getEntryMetaSpy.and.returnValue(of(makeEntry('application/ogg')));
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('application/ogg'));
    fixture.detectChanges();

    expect(component.mediaKind).toBe('audio');
    const audioEl: HTMLAudioElement | null = fixture.nativeElement.querySelector('audio');
    expect(audioEl).toBeTruthy();
    expect(fixture.nativeElement.querySelector('.generic-file-preview')).toBeNull();
  });

  it('M6: stale onDelete() completion from a closed session does not close a re-opened modal', () => {
    getEntryMetaSpy.and.returnValue(of(makeEntry('audio/mpeg')));
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('audio/mpeg'));
    fixture.detectChanges();

    const confirm$ = new Subject<boolean>();
    const delete$ = new Subject<{ message: string }>();
    spyOn(modalService, 'open').and.callFake((id: string) => {
      if (id === 'confirmationModal') {
        return confirm$.asObservable();
      }
      return of(null);
    });
    deleteEntrySpy.and.returnValue(delete$.asObservable());

    component.onDelete();
    confirm$.next(true);
    confirm$.complete();
    expect(component.isLoadingFile).toBeTrue();

    // Close and re-open the detail modal before delete finishes
    modalService.close(EntryDetailModalComponent.MODAL_ID);
    const closeSpy = spyOn(modalService, 'close').and.callThrough();

    // Open new session via ModalService.open directly (since modalService.open was spied for confirmationModal)
    (modalService.open as jasmine.Spy).and.callThrough();
    modalService.open(EntryDetailModalComponent.MODAL_ID);
    selectedEntry$.next(makeEntry('audio/mpeg'));
    fixture.detectChanges();
    expect(component.isLoadingFile).toBeFalse();

    // Stale delete completes
    delete$.next({ message: 'deleted' });
    delete$.complete();

    expect(closeSpy).not.toHaveBeenCalled();
    expect(component.isLoadingFile).toBeFalse();
  });
});
