// frontend/src/app/services/image-cache.service.spec.ts
import { TestBed } from '@angular/core/testing';
import { HttpClientTestingModule, HttpTestingController } from '@angular/common/http/testing';
import { BehaviorSubject } from 'rxjs';
import { filter, map, scan } from 'rxjs/operators';
import { ImageCacheService } from './image-cache.service';
import { AuthService } from './auth.service';
import { User } from '../models';

describe('ImageCacheService', () => {
  let service: ImageCacheService;
  let httpMock: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule],
      providers: [ImageCacheService],
    });
    service = TestBed.inject(ImageCacheService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('should be created', () => {
    expect(service).toBeTruthy();
  });

  it('should fetch blob and return object URL', (done) => {
    const testUrl = '/api/media/1/preview';
    const fakeBlob = new Blob(['image-bytes'], { type: 'image/jpeg' });

    service.getBlobUrl(testUrl).subscribe((blobUrl) => {
      expect(blobUrl).toContain('blob:');
      done();
    });

    const req = httpMock.expectOne(testUrl);
    expect(req.request.method).toBe('GET');
    expect(req.request.responseType).toBe('blob');
    req.flush(fakeBlob);
  });

  it('should share the same Observable and blob URL for identical URL requests', (done) => {
    const testUrl = '/api/media/1/preview';
    const fakeBlob = new Blob(['image-bytes'], { type: 'image/jpeg' });

    let firstResult = '';
    let secondResult = '';

    service.getBlobUrl(testUrl).subscribe((url) => {
      firstResult = url;
    });

    service.getBlobUrl(testUrl).subscribe((url) => {
      secondResult = url;
      expect(secondResult).toBe(firstResult);
      expect(secondResult).toContain('blob:');
      done();
    });

    const req = httpMock.expectOne(testUrl);
    req.flush(fakeBlob);
  });

  it('should revoke blob URL on invalidate', (done) => {
    const testUrl = '/api/media/1/preview';
    const fakeBlob = new Blob(['image-bytes'], { type: 'image/jpeg' });
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    service.getBlobUrl(testUrl).subscribe((blobUrl) => {
      service.invalidate(testUrl);
      expect(revokeSpy).toHaveBeenCalledWith(blobUrl);
      done();
    });

    const req = httpMock.expectOne(testUrl);
    req.flush(fakeBlob);
  });

  it('should revoke all blob URLs on clearAll', (done) => {
    const testUrl1 = '/api/media/1/preview';
    const testUrl2 = '/api/media/2/preview';
    const fakeBlob1 = new Blob(['image-bytes-1'], { type: 'image/jpeg' });
    const fakeBlob2 = new Blob(['image-bytes-2'], { type: 'image/jpeg' });
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    let url1 = '';
    let url2 = '';

    service.getBlobUrl(testUrl1).subscribe((bUrl) => {
      url1 = bUrl;
      service.getBlobUrl(testUrl2).subscribe((bUrl2) => {
        url2 = bUrl2;
        service.clearAll();
        expect(revokeSpy).toHaveBeenCalledWith(url1);
        expect(revokeSpy).toHaveBeenCalledWith(url2);
        done();
      });
    });

    httpMock.expectOne(testUrl1).flush(fakeBlob1);
    httpMock.expectOne(testUrl2).flush(fakeBlob2);
  });

  it('should remove from cache on HTTP error', (done) => {
    const testUrl = '/api/media/invalid/preview';

    service.getBlobUrl(testUrl).subscribe({
      next: () => fail('Should have failed with 404'),
      error: (err) => {
        expect(err.status).toBe(404);

        // Next request should attempt a new HTTP call
        service.getBlobUrl(testUrl).subscribe({
          next: () => fail('Should have failed with 404 again'),
          error: (err2) => {
            expect(err2.status).toBe(404);
            done();
          },
        });

        const req2 = httpMock.expectOne(testUrl);
        req2.flush(null, { status: 404, statusText: 'Not Found' });
      },
    });

    const req1 = httpMock.expectOne(testUrl);
    req1.flush(null, { status: 404, statusText: 'Not Found' });
  });

  // --- FE-025: LRU eviction must not revoke blob URLs that are still on screen ---

  /** Push the given URL out of the LRU cache without issuing any HTTP requests. */
  function evictViaCachePressure(): void {
    // MAX_CACHE_SIZE (1024) inserts past the cap evict the oldest entry. These
    // fillers are never subscribed, so no HTTP requests are made.
    for (let i = 0; i < 1024; i++) {
      service.getBlobUrl(`/api/filler/${i}`);
    }
  }

  it('FE-025: LRU eviction revokes an unreferenced blob URL', (done) => {
    const testUrl = '/api/media/1/preview';
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    service.getBlobUrl(testUrl).subscribe((blobUrl) => {
      evictViaCachePressure();
      expect(revokeSpy).toHaveBeenCalledWith(blobUrl);
      done();
    });

    httpMock.expectOne(testUrl).flush(new Blob(['image-bytes'], { type: 'image/jpeg' }));
  });

  it('FE-025: LRU eviction DEFERS revocation of a referenced blob URL until release', (done) => {
    const testUrl = '/api/media/1/preview';
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    service.getBlobUrl(testUrl).subscribe((blobUrl) => {
      service.retain(testUrl); // a rendered <img> is using it

      evictViaCachePressure();
      expect(revokeSpy).not.toHaveBeenCalled(); // still on screen → must survive

      service.release(testUrl); // the <img> released it
      expect(revokeSpy).toHaveBeenCalledWith(blobUrl);
      done();
    });

    httpMock.expectOne(testUrl).flush(new Blob(['image-bytes'], { type: 'image/jpeg' }));
  });

  it('FE-025: release() of a still-cached URL does not revoke it', (done) => {
    const testUrl = '/api/media/1/preview';
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    service.getBlobUrl(testUrl).subscribe(() => {
      service.retain(testUrl);
      service.release(testUrl);
      expect(revokeSpy).not.toHaveBeenCalled();

      // The cached URL still works for later consumers.
      expect(service.getBlobUrl(testUrl)).toBeTruthy();
      done();
    });

    httpMock.expectOne(testUrl).flush(new Blob(['image-bytes'], { type: 'image/jpeg' }));
  });

  it('N5: a re-fetch of an evicted-but-referenced URL revokes BOTH blob URLs exactly once on release', (done) => {
    const testUrl = '/api/media/1/preview';
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    service.getBlobUrl(testUrl).subscribe((blobUrl1) => {
      service.retain(testUrl); // a rendered <img> is still showing blobUrl1
      evictViaCachePressure(); // evicted while referenced → revocation deferred

      // The same source URL is fetched again (e.g. a second consumer) → a new blob.
      service.getBlobUrl(testUrl).subscribe((blobUrl2) => {
        expect(blobUrl2).not.toBe(blobUrl1);

        service.release(testUrl); // the <img> is gone

        // Both the superseded and the current blob URL must be revoked, each
        // exactly once — the old one must not be orphaned by the overwrite.
        expect(revokeSpy).toHaveBeenCalledWith(blobUrl1);
        expect(revokeSpy).toHaveBeenCalledWith(blobUrl2);
        expect(revokeSpy).toHaveBeenCalledTimes(2);
        done();
      });

      httpMock.expectOne(testUrl).flush(new Blob(['second'], { type: 'image/jpeg' }));
    });

    httpMock.expectOne(testUrl).flush(new Blob(['first'], { type: 'image/jpeg' }));
  });

  it('revokes in-flight blob immediately when invalidated while unreferenced', () => {
    const testUrl = '/api/media/inflight-invalidate/preview';
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    service.getBlobUrl(testUrl).subscribe();
    const req = httpMock.expectOne(testUrl);

    service.invalidate(testUrl);
    req.flush(new Blob(['late'], { type: 'image/jpeg' }));

    expect(revokeSpy).toHaveBeenCalledTimes(1);
    expect(revokeSpy.calls.mostRecent().args[0]).toContain('blob:');
  });

  it('revokes in-flight blob immediately on clearAll() when unreferenced, and defers when referenced', () => {
    const unrefUrl = '/api/media/inflight-unref/preview';
    const refUrl = '/api/media/inflight-ref/preview';
    const revokeSpy = spyOn(URL, 'revokeObjectURL');

    service.getBlobUrl(unrefUrl).subscribe();
    service.retain(refUrl);
    service.getBlobUrl(refUrl).subscribe();

    const unrefReq = httpMock.expectOne(unrefUrl);
    const refReq = httpMock.expectOne(refUrl);

    service.clearAll();

    // Unreferenced in-flight entry revokes as soon as it materializes.
    unrefReq.flush(new Blob(['unref'], { type: 'image/jpeg' }));
    expect(revokeSpy).toHaveBeenCalledTimes(1);

    // Referenced in-flight entry stays alive until release().
    refReq.flush(new Blob(['ref'], { type: 'image/jpeg' }));
    expect(revokeSpy).toHaveBeenCalledTimes(1);

    // Release drops refCount to 0 and revokes the deferred blob.
    service.release(refUrl);
    expect(revokeSpy).toHaveBeenCalledTimes(2);
    const materializedBlob = revokeSpy.calls.mostRecent().args[0];
    expect(materializedBlob).toContain('blob:');
  });

  it('does not delete a newer cache entry if an invalidated in-flight request errors later', () => {
    const testUrl = '/api/media/race-error/preview';

    let firstErrored = false;
    service.getBlobUrl(testUrl).subscribe({ error: () => (firstErrored = true) });
    const req1 = httpMock.expectOne(testUrl);

    service.invalidate(testUrl);

    let secondBlob = '';
    service.getBlobUrl(testUrl).subscribe((b) => (secondBlob = b));
    const req2 = httpMock.expectOne(testUrl);

    req1.flush(null, { status: 500, statusText: 'Server Error' });
    expect(firstErrored).toBeTrue();

    // Newer entry must still be cached and resolve normally.
    const cached$ = service.getBlobUrl(testUrl);
    req2.flush(new Blob(['ok'], { type: 'image/jpeg' }));
    expect(secondBlob).toContain('blob:');

    let replayBlob = '';
    cached$.subscribe((b) => (replayBlob = b));
    expect(replayBlob).toBe(secondBlob);
    httpMock.expectNone(testUrl);
  });
});

describe('ImageCacheService — AuthService user lifecycle clearing', () => {
  let currentUser$: BehaviorSubject<User | null>;
  let service: ImageCacheService;
  let httpMock: HttpTestingController;

  const user1 = { id: 'u1', username: 'alice' } as unknown as User;
  const user2 = { id: 'u2', username: 'bob' } as unknown as User;

  beforeEach(() => {
    currentUser$ = new BehaviorSubject<User | null>(user1);
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
    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule],
      providers: [
        ImageCacheService,
        {
          provide: AuthService,
          useValue: { currentUser$: currentUser$.asObservable(), sessionCleared$ },
        },
      ],
    });
    service = TestBed.inject(ImageCacheService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('clears cached blobs on logout (null) and user switch, but keeps them when same user re-emits', () => {
    const revokeSpy = spyOn(URL, 'revokeObjectURL');
    let blobUrl = '';
    service.getBlobUrl('/api/media/u1/preview').subscribe((b) => (blobUrl = b));
    httpMock.expectOne('/api/media/u1/preview').flush(new Blob(['u1'], { type: 'image/jpeg' }));

    // Re-emitting the same user does not clear the cache.
    currentUser$.next({ ...user1 });
    expect(revokeSpy).not.toHaveBeenCalled();

    // Switching to another user clears the cache.
    currentUser$.next(user2);
    expect(revokeSpy).toHaveBeenCalledWith(blobUrl);

    // Fetch for user2, then log out (null) -> clears cache.
    let blobUrl2 = '';
    service.getBlobUrl('/api/media/u2/preview').subscribe((b) => (blobUrl2 = b));
    httpMock.expectOne('/api/media/u2/preview').flush(new Blob(['u2'], { type: 'image/jpeg' }));

    currentUser$.next(null);
    expect(revokeSpy).toHaveBeenCalledWith(blobUrl2);
  });
});
