// frontend/src/app/services/entry.service.spec.ts
// Regression specs for FE-013 (poll termination): a poll must stop when the entry
// is gone (404/410) or after consecutive transport failures, keep polling without
// a duration cap while the entry is live (queued/processing), and must not outlive
// the session (a stale poll after logout can bounce a fresh login to /login).
import { fakeAsync, tick } from '@angular/core/testing';
import { TestBed } from '@angular/core/testing';
import { HttpErrorResponse } from '@angular/common/http';
import { HttpClientTestingModule, HttpTestingController } from '@angular/common/http/testing';
import { BehaviorSubject, Observable } from 'rxjs';
import { filter, map, scan } from 'rxjs/operators';
import { EntryService } from './entry.service';
import { AuthService } from './auth.service';
import { ImageCacheService } from './image-cache.service';
import { NotificationService } from './notification.service';
import { Entry, SearchRequest, User } from '../models';

/** The poll chain is service-internal; reach it without widening the public API. */
interface PollCapableService {
  pollForEntryStatus(dbId: string, entryId: number): Observable<void>;
}

describe('EntryService — FE-013 poll termination', () => {
  let service: EntryService;
  let httpMock: HttpTestingController;
  let currentUser$: BehaviorSubject<User | null>;
  let hasDatabasePermissionSpy: jasmine.Spy;
  let imageCacheSpy: jasmine.SpyObj<ImageCacheService>;
  let notificationSpy: jasmine.SpyObj<NotificationService>;

  const user = { id: 'u1', username: 'alice', is_admin: true, permissions: [] } as unknown as User;

  const pollUrl = (entryId: number, dbId = 'db1') => `/api/database/${dbId}/entry/${entryId}`;

  beforeEach(() => {
    currentUser$ = new BehaviorSubject<User | null>(user);
    const sessionCleared$ = currentUser$.pipe(
      scan(
        (acc: { lastUserId: string | null; cleared: boolean }, u: User | null) => {
          const nextUserId = u?.id ?? null;
          const cleared = u === null || (acc.lastUserId !== null && nextUserId !== acc.lastUserId);
          return { lastUserId: nextUserId, cleared };
        },
        { lastUserId: null, cleared: false },
      ),
      filter((state) => state.cleared),
      map(() => undefined),
    );
    hasDatabasePermissionSpy = jasmine.createSpy('hasDatabasePermission').and.returnValue(true);
    imageCacheSpy = jasmine.createSpyObj('ImageCacheService', ['invalidate']);
    notificationSpy = jasmine.createSpyObj('NotificationService', [
      'showError',
      'showSuccess',
      'showInfo',
      'showGlobalError',
      'clearGlobalError',
    ]);

    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule],
      providers: [
        EntryService,
        {
          provide: AuthService,
          useValue: {
            currentUser$,
            sessionCleared$,
            getAccessToken: () => 'A1',
            hasDatabasePermission: hasDatabasePermissionSpy,
          },
        },
        {
          provide: ImageCacheService,
          useValue: imageCacheSpy,
        },
        {
          provide: NotificationService,
          useValue: notificationSpy,
        },
      ],
    });
    service = TestBed.inject(EntryService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  /** Upload an entry the backend accepts for async processing (starts a poll). */
  function startProcessingUpload(
    entryId: number,
    status: 'queued' | 'processing' = 'processing',
  ): void {
    service
      .uploadEntry(
        'db1',
        { timestamp: 1000, filename: 'a.jpg', custom_fields: {} },
        new File(['x'], 'a.jpg', { type: 'image/jpeg' }),
        { silentSuccess: true },
      )
      .subscribe();

    httpMock.expectOne('/api/database/db1/entry').flush(
      {
        id: entryId,
        timestamp: 1000,
        created_at: 1000,
        updated_at: 1000,
        database_id: 'db1',
        status,
      },
      { status: 201, statusText: 'Created' },
    );
  }

  function flushPoll(entryId: number, status: string): void {
    httpMock.expectOne(pollUrl(entryId)).flush({
      id: entryId,
      timestamp: 1000,
      created_at: 1000,
      updated_at: 1000,
      database_id: 'db1',
      status,
    });
  }

  it('FE-013: a 404 on the poll is terminal — no further HTTP and the id leaves processingEntries$', fakeAsync(() => {
    startProcessingUpload(42);

    let processing: number[] = [];
    service.processingEntries$.subscribe((ids) => (processing = ids));
    expect(processing).toEqual([42]);

    const updates: Entry[] = [];
    service.entryUpdated$.subscribe((e) => updates.push(e));
    let listRefreshes = 0;
    service.refreshRequired$.subscribe(() => listRefreshes++);

    tick(2000);
    httpMock.expectOne(pollUrl(42)).flush(null, { status: 404, statusText: 'Not Found' });

    // the entry is gone: the spinner is gone and views were told to drop the card
    expect(processing).toEqual([]);
    expect(updates.some((e) => e.id === 42 && e.status === 'error')).toBeTrue();
    expect(listRefreshes).toBe(1);

    // follow-up ticks issue no HTTP request at all
    tick(120000);
    httpMock.expectNone(pollUrl(42));
  }));

  it('FE-013: the poll observable completes when the entry turns out to be gone', fakeAsync(() => {
    let completed = false;
    (service as unknown as PollCapableService)
      .pollForEntryStatus('db1', 42)
      .subscribe({ complete: () => (completed = true) });

    tick(2000);
    httpMock.expectOne(pollUrl(42)).flush(null, { status: 404, statusText: 'Not Found' });

    expect(completed).toBeTrue();
  }));

  it('FE-013: consecutive transport failures are capped — the poll stops and the id is removed', fakeAsync(() => {
    startProcessingUpload(42);

    let processing: number[] = [];
    service.processingEntries$.subscribe((ids) => (processing = ids));
    expect(processing).toEqual([42]);

    // 10 consecutive failures (backoff: 2s start, doubling to the 30s cap)
    tick(2000);
    httpMock.expectOne(pollUrl(42)).flush(null, { status: 500, statusText: 'Server Error' });
    for (const delay of [4000, 8000, 16000, 30000, 30000, 30000, 30000, 30000, 30000]) {
      tick(delay);
      httpMock.expectOne(pollUrl(42)).flush(null, { status: 500, statusText: 'Server Error' });
    }

    expect(processing).toEqual([]);

    // the chain is dead — no request is ever issued again
    tick(120000);
    httpMock.expectNone(pollUrl(42));
  }));

  it('FE-013: live conversions are NOT capped — polling continues past 60 s', fakeAsync(() => {
    startProcessingUpload(42);

    // 2s + 2s + 4s + 8s + 16s + 30s = 62s of continuous "processing" answers
    tick(2000);
    flushPoll(42, 'processing');
    for (const delay of [2000, 4000, 8000, 16000, 30000]) {
      tick(delay);
      flushPoll(42, 'processing');
    }

    // well past the old 60 s hard cap the poll is still alive and asking
    tick(30000);
    httpMock.expectOne(pollUrl(42)).flush({
      id: 42,
      timestamp: 1000,
      created_at: 1000,
      updated_at: 1000,
      database_id: 'db1',
      status: 'ready',
    });

    let processing: number[] = [42];
    service.processingEntries$.subscribe((ids) => (processing = ids));
    expect(processing).toEqual([]);
  }));

  it('FE-013: QUEUED→PROCESSING still emits entryUpdated$ (and ready stays terminal)', fakeAsync(() => {
    startProcessingUpload(42, 'queued');

    const updates: Entry[] = [];
    service.entryUpdated$.subscribe((e) => updates.push(e));

    tick(2000);
    flushPoll(42, 'queued');
    tick(2000);
    flushPoll(42, 'processing');
    tick(2000);
    flushPoll(42, 'ready');

    expect(updates.map((e) => e.status)).toEqual(['queued', 'processing', 'ready']);

    let processing: number[] = [42];
    service.processingEntries$.subscribe((ids) => (processing = ids));
    expect(processing).toEqual([]);
  }));

  it('FE-013: service-owned polls stop when the session ends (logout)', fakeAsync(() => {
    startProcessingUpload(42);

    // clearSessionAndRedirect() clears the current user — the poll must die with it
    currentUser$.next(null);

    tick(120000);
    httpMock.expectNone(pollUrl(42));

    let processing: number[] = [42];
    service.processingEntries$.subscribe((ids) => (processing = ids));
    expect(processing).toEqual([]);
  }));

  describe('updateEntry propagation (entry modal shows updated metadata after save)', () => {
    const updatedEntry = (id: number, filename: string): Entry =>
      ({
        id,
        database_id: 'db1',
        filename,
        timestamp: 1000,
        created_at: 1000,
        updated_at: 2000,
        status: 'ready',
      }) as Entry;

    it('emits entryUpdated$ with the PATCH response body (list rows merge the new values)', () => {
      const emissions: Entry[] = [];
      service.entryUpdated$.subscribe((e) => emissions.push(e));

      service.updateEntry('db1', 5, { filename: 'new.mp3' }).subscribe();
      httpMock.expectOne('/api/database/db1/entry/5').flush(updatedEntry(5, 'new.mp3'));

      expect(emissions.length).withContext('entryUpdated$ must fire for metadata updates').toBe(1);
      expect(emissions[0].filename).toBe('new.mp3');
      expect(emissions[0].updated_at).toBe(2000);
    });

    it('re-publishes the selected entry so the open entry modal sees the saved values', () => {
      service.selectEntry(updatedEntry(5, 'old.mp3'));

      const seen: (Entry | null)[] = [];
      service.selectedEntry$.subscribe((e) => seen.push(e));

      service.updateEntry('db1', 5, { filename: 'new.mp3' }).subscribe();
      httpMock.expectOne('/api/database/db1/entry/5').flush(updatedEntry(5, 'new.mp3'));

      const last = seen[seen.length - 1];
      expect(last?.id).toBe(5);
      expect(last?.filename)
        .withContext('the selected entry must carry the updated metadata')
        .toBe('new.mp3');
    });

    it('does not clobber a different selected entry', () => {
      service.selectEntry(updatedEntry(9, 'other.mp3'));

      const seen: (Entry | null)[] = [];
      service.selectedEntry$.subscribe((e) => seen.push(e));

      service.updateEntry('db1', 5, { filename: 'new.mp3' }).subscribe();
      httpMock.expectOne('/api/database/db1/entry/5').flush(updatedEntry(5, 'new.mp3'));

      const last = seen[seen.length - 1];
      expect(last?.id).withContext('entry 9 must stay selected').toBe(9);
      expect(last?.filename).toBe('other.mp3');
    });
  });

  describe('202 async upload timestamp preservation', () => {
    it('preserves pre-1970 timestamps (< 0) on 202 async uploads', fakeAsync(() => {
      const pre1970 = Date.UTC(1950, 0, 1, 12, 0, 0);
      const created: Entry[] = [];
      service.entryCreated$.subscribe((e) => created.push(e));

      service
        .uploadEntry(
          'db1',
          { timestamp: pre1970, filename: 'vintage.mp4', custom_fields: {} },
          new File(['x'], 'vintage.mp4', { type: 'video/mp4' }),
          { silentSuccess: true },
        )
        .subscribe();

      httpMock.expectOne('/api/database/db1/entry').flush(
        {
          id: 101,
          timestamp: pre1970,
          created_at: pre1970,
          updated_at: pre1970,
          database_id: 'db1',
          status: 'processing',
        },
        { status: 202, statusText: 'Accepted' },
      );

      expect(created.length).toBe(1);
      expect(created[0].timestamp).toBe(pre1970);
      expect(created[0].created_at).toBe(pre1970);
      expect(created[0].updated_at).toBe(pre1970);

      currentUser$.next(null);
    }));

    it('preserves exact Unix epoch (0) timestamps on 202 async uploads without falling back to Date.now()', fakeAsync(() => {
      const created: Entry[] = [];
      service.entryCreated$.subscribe((e) => created.push(e));

      service
        .uploadEntry(
          'db1',
          { timestamp: 0, filename: 'epoch-a.mp4', custom_fields: {} },
          new File(['x'], 'epoch-a.mp4', { type: 'video/mp4' }),
          { silentSuccess: true },
        )
        .subscribe();

      httpMock.expectOne('/api/database/db1/entry').flush(
        {
          id: 102,
          timestamp: 0,
          created_at: 0,
          updated_at: 0,
          database_id: 'db1',
          status: 'queued',
        },
        { status: 202, statusText: 'Accepted' },
      );

      // Also verify fallback from missing partialEntry.timestamp to metadata.timestamp === 0
      service
        .uploadEntry(
          'db1',
          { timestamp: 0, filename: 'epoch-b.mp4', custom_fields: {} },
          new File(['x'], 'epoch-b.mp4', { type: 'video/mp4' }),
          { silentSuccess: true },
        )
        .subscribe();

      httpMock.expectOne('/api/database/db1/entry').flush(
        {
          id: 103,
          created_at: 0,
          updated_at: 0,
          database_id: 'db1',
          status: 'processing',
        },
        { status: 202, statusText: 'Accepted' },
      );

      expect(created.length).toBe(2);
      for (const entry of created) {
        expect(entry.timestamp).toBe(0);
        expect(entry.created_at).toBe(0);
        expect(entry.updated_at).toBe(0);
      }

      currentUser$.next(null);
    }));
  });

  describe('session transitions, deletion cache invalidation, and search limit clamping', () => {
    const makeEntry = (id: number, dbId = 'db1'): Entry =>
      ({
        id,
        database_id: dbId,
        filename: `item-${id}.jpg`,
        timestamp: 1000,
        created_at: 1000,
        updated_at: 1000,
        status: 'ready',
      }) as Entry;

    it('clears selectedEntry$ and stops polls when user switches directly to another user', fakeAsync(() => {
      startProcessingUpload(42);
      service.selectEntry(makeEntry(7));

      let selected: Entry | null = makeEntry(7);
      service.selectedEntry$.subscribe((e) => (selected = e));

      currentUser$.next({
        id: 'u2',
        username: 'bob',
        is_admin: false,
        permissions: [],
      } as unknown as User);

      expect(selected).toBeNull();
      tick(120000);
      httpMock.expectNone(pollUrl(42));
    }));

    it('deleteEntry invalidates preview cache and clears matching selectedEntry$', () => {
      service.selectEntry(makeEntry(5, 'db1'));
      let selected: Entry | null = null;
      service.selectedEntry$.subscribe((e) => (selected = e));

      service.deleteEntry('db1', 5).subscribe();
      httpMock.expectOne('/api/database/db1/entry/5').flush({ message: 'Deleted' });

      expect(imageCacheSpy.invalidate).toHaveBeenCalledWith('/api/database/db1/entry/5/preview');
      expect(selected).toBeNull();
    });

    it('deleteEntry does not clear selectedEntry$ from a different database on ID collision', () => {
      service.selectEntry(makeEntry(5, 'db2'));
      let selected: Entry | null = null as Entry | null;
      service.selectedEntry$.subscribe((e) => (selected = e));

      service.deleteEntry('db1', 5).subscribe();
      httpMock.expectOne('/api/database/db1/entry/5').flush({ message: 'Deleted' });

      expect(imageCacheSpy.invalidate).toHaveBeenCalledWith('/api/database/db1/entry/5/preview');
      expect(selected?.database_id).toBe('db2');
    });

    it('bulkDeleteEntries invalidates preview caches and clears matching selectedEntry$', () => {
      service.selectEntry(makeEntry(6, 'db1'));
      let selected: Entry | null = null;
      service.selectedEntry$.subscribe((e) => (selected = e));

      service.bulkDeleteEntries('db1', [5, 6]).subscribe();
      httpMock.expectOne('/api/database/db1/entries/delete').flush({
        database_id: 'db1',
        deleted_count: 2,
        space_freed_bytes: 100,
        message: 'ok',
        errors: '',
      });

      expect(imageCacheSpy.invalidate).toHaveBeenCalledWith('/api/database/db1/entry/5/preview');
      expect(imageCacheSpy.invalidate).toHaveBeenCalledWith('/api/database/db1/entry/6/preview');
      expect(selected).toBeNull();
    });

    it('searchEntries clamps pagination.limit into [1, 1000] on a cloned payload', () => {
      const highPayload: SearchRequest = { pagination: { limit: 1500, offset: 0 } };
      service.searchEntries('db1', highPayload).subscribe();
      const reqHigh = httpMock.expectOne('/api/database/db1/entries/search');
      expect(reqHigh.request.body.pagination.limit).toBe(1000);
      expect(highPayload.pagination.limit).toBe(1500);
      reqHigh.flush([]);

      const lowPayload: SearchRequest = { pagination: { limit: 0, offset: 0 } };
      service.searchEntries('db1', lowPayload).subscribe();
      const reqLow = httpMock.expectOne('/api/database/db1/entries/search');
      expect(reqLow.request.body.pagination.limit).toBe(1);
      expect(lowPayload.pagination.limit).toBe(0);
      reqLow.flush([]);
    });

    it('uses composite poll keys so two DBs with the same entry ID do not cancel each other', fakeAsync(() => {
      for (const dbId of ['db1', 'db2']) {
        service
          .uploadEntry(
            dbId,
            { timestamp: 1000, filename: `${dbId}.jpg`, custom_fields: {} },
            new File(['x'], `${dbId}.jpg`, { type: 'image/jpeg' }),
            { silentSuccess: true },
          )
          .subscribe();
        httpMock.expectOne(`/api/database/${dbId}/entry`).flush(
          {
            id: 42,
            timestamp: 1000,
            created_at: 1000,
            updated_at: 1000,
            database_id: dbId,
            status: 'processing',
          },
          { status: 201, statusText: 'Created' },
        );
      }

      let processing: number[] = [];
      service.processingEntries$.subscribe((ids) => (processing = ids));
      expect(processing).toEqual([42]);

      tick(2000);
      httpMock.expectOne(pollUrl(42, 'db1')).flush({
        id: 42,
        timestamp: 1000,
        created_at: 1000,
        updated_at: 1000,
        database_id: 'db1',
        status: 'ready',
      });
      httpMock.expectOne(pollUrl(42, 'db2')).flush({
        id: 42,
        timestamp: 1000,
        created_at: 1000,
        updated_at: 1000,
        database_id: 'db2',
        status: 'processing',
      });

      // db2 is still processing so 42 remains in processingEntries$
      expect(processing).toEqual([42]);

      tick(2000);
      httpMock.expectNone(pollUrl(42, 'db1'));
      httpMock.expectOne(pollUrl(42, 'db2')).flush({
        id: 42,
        timestamp: 1000,
        created_at: 1000,
        updated_at: 1000,
        database_id: 'db2',
        status: 'ready',
      });

      expect(processing).toEqual([]);
    }));

    it('skips polling and processingEntries$ tracking when user lacks can_view permission', fakeAsync(() => {
      hasDatabasePermissionSpy.and.returnValue(false);
      const created: Entry[] = [];
      service.entryCreated$.subscribe((e) => created.push(e));

      let processing: number[] = [];
      service.processingEntries$.subscribe((ids) => (processing = ids));

      service
        .uploadEntry(
          'db1',
          { timestamp: 1000, filename: 'clip.mp4', custom_fields: { tag: 'v1' } },
          new File(['x'], 'clip.mp4', { type: 'video/mp4' }),
          { silentSuccess: true },
        )
        .subscribe();

      httpMock.expectOne('/api/database/db1/entry').flush(
        {
          id: 77,
          timestamp: 1000,
          created_at: 1000,
          updated_at: 1000,
          database_id: 'db1',
          mime_type: 'video/mp4',
          status: 'queued',
        },
        { status: 202, statusText: 'Accepted' },
      );

      expect(created.length).toBe(1);
      expect(created[0].mime_type).toBe('video/mp4');
      expect(created[0].custom_fields).toEqual({ tag: 'v1' });
      expect(processing).toEqual([]);

      tick(10000);
      httpMock.expectNone(pollUrl(77, 'db1'));
    }));

    it('terminates polling on 401/403 without calling handleEntryGone', fakeAsync(() => {
      startProcessingUpload(42);

      let processing: number[] = [];
      service.processingEntries$.subscribe((ids) => (processing = ids));
      const updates: Entry[] = [];
      service.entryUpdated$.subscribe((e) => updates.push(e));
      let refreshes = 0;
      service.refreshRequired$.subscribe(() => refreshes++);

      tick(2000);
      httpMock.expectOne(pollUrl(42)).flush(null, { status: 403, statusText: 'Forbidden' });

      expect(processing).toEqual([]);
      expect(updates).toEqual([]);
      expect(refreshes).toBe(0);

      tick(120000);
      httpMock.expectNone(pollUrl(42));
    }));

    it('parses JSON error messages from Blob responses via catchApiError', (done) => {
      const errorBlob = new Blob([JSON.stringify({ error: 'Export limit exceeded' })], {
        type: 'application/json',
      });

      service.bulkExportEntries('db1', [1, 2]).subscribe({
        next: () => fail('expected error'),
        error: (err: HttpErrorResponse) => {
          expect(err instanceof HttpErrorResponse).toBeTrue();
          expect(err.status).toBe(400);
          expect(notificationSpy.showError).toHaveBeenCalledWith('Export limit exceeded');
          done();
        },
      });

      httpMock
        .expectOne('/api/database/db1/entries/export')
        .flush(errorBlob, { status: 400, statusText: 'Bad Request' });
    });

    it('preserves mime_type and custom_fields on 202 async upload and ignores null body', () => {
      const created: Entry[] = [];
      service.entryCreated$.subscribe((e) => created.push(e));

      service
        .uploadEntry(
          'db1',
          { timestamp: 1234, filename: 'a.mp4', custom_fields: { source: 'cam1' } },
          new File(['x'], 'a.mp4', { type: 'video/mp4' }),
          { silentSuccess: true },
        )
        .subscribe();

      httpMock.expectOne('/api/database/db1/entry').flush(
        {
          id: 88,
          timestamp: 1234,
          created_at: 1234,
          updated_at: 1234,
          database_id: 'db1',
          mime_type: 'video/webm',
          status: 'processing',
          custom_fields: { source: 'server' },
        },
        { status: 202, statusText: 'Accepted' },
      );

      expect(created.length).toBe(1);
      expect(created[0].mime_type).toBe('video/webm');
      expect(created[0].custom_fields).toEqual({ source: 'server' });

      // Null 202 body must not throw
      service
        .uploadEntry(
          'db1',
          { timestamp: 1234, filename: 'b.mp4', custom_fields: {} },
          new File(['x'], 'b.mp4', { type: 'video/mp4' }),
          { silentSuccess: true },
        )
        .subscribe();
      httpMock
        .expectOne('/api/database/db1/entry')
        .flush(null, { status: 202, statusText: 'Accepted' });
      expect(created.length).toBe(1);

      currentUser$.next(null);
    });
  });
});
