// frontend/src/app/components/entry-detail-modal/entry-detail-modal.component.spec.ts
// Regression specs for FE-012 (one failed request must not brick the detail modal),
// FE-023 (media keeps playing after the modal closes) and FE-024 (object URL leak on
// ESC/X/overlay dismissal).
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NO_ERRORS_SCHEMA } from '@angular/core';
import { CommonModule } from '@angular/common';
import { BehaviorSubject, of, throwError } from 'rxjs';
import { EntryDetailModalComponent } from './entry-detail-modal.component';
import { EntryService } from '../../services/entry.service';
import { DatabaseService } from '../../services/database.service';
import { AuthService } from '../../services/auth.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import { FormatBytesPipe } from '../../pipes/format-bytes.pipe';
import { Database, Entry, User } from '../../models';

describe('EntryDetailModalComponent — FE-012/FE-023/FE-024 regressions', () => {
  let fixture: ComponentFixture<EntryDetailModalComponent>;
  let component: EntryDetailModalComponent;
  let modalService: ModalService;
  let selectedEntry$: BehaviorSubject<Entry | null>;
  let getEntryMetaSpy: jasmine.Spy;
  let getEntryFileBlobSpy: jasmine.Spy;
  let getEntryFileUrlSpy: jasmine.Spy;
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
      deleteEntry: jasmine.createSpy('deleteEntry').and.returnValue(of({ message: 'ok' })),
    };

    await TestBed.configureTestingModule({
      declarations: [EntryDetailModalComponent],
      imports: [CommonModule, FormatBytesPipe],
      schemas: [NO_ERRORS_SCHEMA], // <app-modal> wrapper and [secureSrc] are not under test
      providers: [
        { provide: EntryService, useValue: entryServiceMock },
        { provide: DatabaseService, useValue: { selectedDatabase$: of(db) } },
        {
          provide: AuthService,
          useValue: { currentUser$: of(user), forceAccessTokenRefresh: forceAccessTokenRefreshSpy },
        },
        { provide: ModalService, useValue: new ModalService() },
        { provide: NotificationService, useValue: notificationService },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(EntryDetailModalComponent);
    component = fixture.componentInstance;
    modalService = TestBed.inject(ModalService);
    fixture.detectChanges(); // runs ngOnInit + renders the template
  });

  it('FE-024: closing via the modal service (ESC/X/overlay path) revokes the object URL and resets state', () => {
    const createSpy = spyOn(URL, 'createObjectURL').and.returnValue('blob:fake-image');
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    getEntryMetaSpy.and.returnValue(of(makeEntry('image/jpeg')));
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
});
