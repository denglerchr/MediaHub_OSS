// frontend/src/app/services/entry.service.spec.ts
// Regression specs for FE-013 (poll termination): a poll must stop when the entry
// is gone (404/410) or after consecutive transport failures, keep polling without
// a duration cap while the entry is live (queued/processing), and must not outlive
// the session (a stale poll after logout can bounce a fresh login to /login).
import { fakeAsync, tick } from '@angular/core/testing';
import { TestBed } from '@angular/core/testing';
import { HttpClientTestingModule, HttpTestingController } from '@angular/common/http/testing';
import { BehaviorSubject, Observable } from 'rxjs';
import { EntryService } from './entry.service';
import { AuthService } from './auth.service';
import { NotificationService } from './notification.service';
import { Entry, User } from '../models';

/** The poll chain is service-internal; reach it without widening the public API. */
interface PollCapableService {
  pollForEntryStatus(dbId: string, entryId: number): Observable<void>;
}

describe('EntryService — FE-013 poll termination', () => {
  let service: EntryService;
  let httpMock: HttpTestingController;
  let currentUser$: BehaviorSubject<User | null>;

  const user = { id: 'u1', username: 'alice', is_admin: true, permissions: [] } as unknown as User;

  const pollUrl = (entryId: number) => `/api/database/db1/entry/${entryId}`;

  beforeEach(() => {
    currentUser$ = new BehaviorSubject<User | null>(user);

    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule],
      providers: [
        EntryService,
        {
          provide: AuthService,
          useValue: {
            currentUser$,
            getAccessToken: () => 'A1',
          },
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
});
