// frontend/src/app/services/image-cache.service.spec.ts
import { TestBed } from '@angular/core/testing';
import { HttpClientTestingModule, HttpTestingController } from '@angular/common/http/testing';
import { ImageCacheService } from './image-cache.service';

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
});
