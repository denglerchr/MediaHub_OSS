// frontend/src/app/directives/secure-image.directive.spec.ts
import { Component, DebugElement } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { HttpClientTestingModule, HttpTestingController } from '@angular/common/http/testing';
import { SecureImageDirective } from './secure-image.directive';
import { ImageCacheService } from '../../core/services/image-cache.service';

// Dummy component to host the directive
@Component({
  template: `<img [secureSrc]="src" (imageError)="onError()" />`,
  imports: [SecureImageDirective],
  standalone: true,
})
class TestHostComponent {
  src: string | null = '/api/test/image';
  onError = jasmine.createSpy('onError');
}

describe('SecureImageDirective', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let component: TestHostComponent;
  let httpMock: HttpTestingController;
  let imgEl: DebugElement;
  const originalIntersectionObserver = window.IntersectionObserver;

  beforeEach(() => {
    (window as any).IntersectionObserver = class {
      private callback: IntersectionObserverCallback;
      constructor(callback: IntersectionObserverCallback) {
        this.callback = callback;
      }
      observe(target: Element) {
        this.callback([{ isIntersecting: true, target } as IntersectionObserverEntry], this as any);
      }
      disconnect() {}
      unobserve() {}
    };

    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule, SecureImageDirective, TestHostComponent],
    });
    fixture = TestBed.createComponent(TestHostComponent);
    component = fixture.componentInstance;
    httpMock = TestBed.inject(HttpTestingController);
    imgEl = fixture.debugElement.query(By.directive(SecureImageDirective));
    fixture.detectChanges();
  });

  afterEach(() => {
    httpMock.verify();
    (window as any).IntersectionObserver = originalIntersectionObserver;
  });

  it('should request the image blob and set src on success', () => {
    const req = httpMock.expectOne('/api/test/image');
    expect(req.request.method).toBe('GET');
    expect(req.request.responseType).toBe('blob');

    // Create a dummy blob
    const blob = new Blob(['fake image data'], { type: 'image/jpeg' });
    req.flush(blob);

    fixture.detectChanges();

    // The src should now start with blob:
    expect(imgEl.nativeElement.src).toContain('blob:');
    expect(component.onError).not.toHaveBeenCalled();
  });

  it('should emit imageError and remove loading class on failure', () => {
    const req = httpMock.expectOne('/api/test/image');

    // UPDATED: Spy on console.error to prevent the expected error from cluttering the test output
    spyOn(console, 'error');

    // Simulate 404
    req.flush(null, { status: 404, statusText: 'Not Found' });

    fixture.detectChanges();

    // Should emit error
    expect(component.onError).toHaveBeenCalled();
    // Should not have loading class
    expect(imgEl.nativeElement.classList.contains('loading-image')).toBeFalse();
    // Verify the error was actually logged (internally), but it won't show in the terminal now
    expect(console.error).toHaveBeenCalled();
  });

  it('should update image src when input src changes', () => {
    // 1. Initial Load
    const req1 = httpMock.expectOne('/api/test/image');
    req1.flush(new Blob(['data1'], { type: 'image/jpeg' }));
    fixture.detectChanges();

    const url1 = imgEl.nativeElement.src;
    expect(url1).toContain('blob:');

    // 2. Change Src
    component.src = '/api/test/image2';
    fixture.detectChanges();

    // 3. New Request
    const req2 = httpMock.expectOne('/api/test/image2');
    req2.flush(new Blob(['data2'], { type: 'image/jpeg' }));
    fixture.detectChanges();

    const url2 = imgEl.nativeElement.src;
    expect(url2).toContain('blob:');
    expect(url2).not.toEqual(url1);
  });

  // --- FE-026: nulling the input must cancel the in-flight fetch ---

  it('FE-026: nulling the input cancels the in-flight fetch (no stale image reappears)', () => {
    const req = httpMock.expectOne('/api/test/image');

    // The entry is switched away before the response arrives.
    component.src = null;
    fixture.detectChanges();
    expect(imgEl.nativeElement.getAttribute('src')).toBeNull();

    // The stale response must NOT be applied any more (the old `filter` dropped the
    // null, switchMap never cancelled, and this late response re-set the previous
    // entry's image).
    req.flush(new Blob(['data1'], { type: 'image/jpeg' }));
    fixture.detectChanges();
    expect(imgEl.nativeElement.getAttribute('src')).toBeNull();
  });

  it('FE-026: switching to another URL cancels the previous in-flight fetch', () => {
    const req1 = httpMock.expectOne('/api/test/image');

    component.src = '/api/test/image2';
    fixture.detectChanges();

    // The stale first response must not overwrite the image while the new one loads.
    req1.flush(new Blob(['data1'], { type: 'image/jpeg' }));
    fixture.detectChanges();
    expect(imgEl.nativeElement.getAttribute('src')).toBeNull();

    const req2 = httpMock.expectOne('/api/test/image2');
    req2.flush(new Blob(['data2'], { type: 'image/jpeg' }));
    fixture.detectChanges();

    expect(imgEl.nativeElement.src).toContain('blob:');
  });

  it('FE-026: a failed load clears the previous image instead of leaving it stale', () => {
    const req1 = httpMock.expectOne('/api/test/image');
    req1.flush(new Blob(['data1'], { type: 'image/jpeg' }));
    fixture.detectChanges();
    expect(imgEl.nativeElement.src).toContain('blob:');

    spyOn(console, 'error');
    component.src = '/api/test/image2';
    fixture.detectChanges();
    httpMock.expectOne('/api/test/image2').flush(null, { status: 404, statusText: 'Not Found' });
    fixture.detectChanges();

    expect(imgEl.nativeElement.getAttribute('src')).toBeNull();
  });

  it('releaseInFlight does not release displayedUrl when inFlightUrl matches displayedUrl', () => {
    const cacheService = TestBed.inject(ImageCacheService);
    const releaseSpy = spyOn(cacheService, 'release').and.callThrough();

    // 1. Display /api/test/image
    httpMock.expectOne('/api/test/image').flush(new Blob(['data1'], { type: 'image/jpeg' }));
    fixture.detectChanges();
    expect(imgEl.nativeElement.src).toContain('blob:');

    // 2. Switch to /api/test/image2 (in-flight), then switch back to /api/test/image (now both inFlight and displayed)
    component.src = '/api/test/image2';
    fixture.detectChanges();
    const req2 = httpMock.expectOne('/api/test/image2');
    component.src = '/api/test/image';
    fixture.detectChanges();

    // 3. Switch to /api/test/image3 while /api/test/image is both inFlightUrl and displayedUrl:
    // releaseInFlight() must NOT release /api/test/image while it is still displayed on screen.
    releaseSpy.calls.reset();
    component.src = '/api/test/image3';
    fixture.detectChanges();

    expect(releaseSpy).not.toHaveBeenCalledWith('/api/test/image');

    // Once /api/test/image3 resolves and replaces displayedUrl, /api/test/image is released.
    req2.flush(new Blob(['data2'], { type: 'image/jpeg' }));
    httpMock.expectOne('/api/test/image3').flush(new Blob(['data3'], { type: 'image/jpeg' }));
    fixture.detectChanges();
    expect(releaseSpy).toHaveBeenCalledWith('/api/test/image');
  });

  it('emits imageError and releases displayedUrl when the native img emits an error event', () => {
    const cacheService = TestBed.inject(ImageCacheService);
    const releaseSpy = spyOn(cacheService, 'release').and.callThrough();

    httpMock.expectOne('/api/test/image').flush(new Blob(['corrupt'], { type: 'image/jpeg' }));
    fixture.detectChanges();
    expect(imgEl.nativeElement.src).toContain('blob:');

    component.onError.calls.reset();
    releaseSpy.calls.reset();

    imgEl.nativeElement.dispatchEvent(new Event('error'));
    fixture.detectChanges();

    expect(component.onError).toHaveBeenCalledTimes(1);
    expect(releaseSpy).toHaveBeenCalledWith('/api/test/image');
    expect(imgEl.nativeElement.getAttribute('src')).toBeNull();
    expect(imgEl.nativeElement.classList.contains('loading-image')).toBeFalse();

    // Subsequent error events while nothing is displayed are ignored.
    component.onError.calls.reset();
    imgEl.nativeElement.dispatchEvent(new Event('error'));
    expect(component.onError).not.toHaveBeenCalled();
  });
});

@Component({
  template: `
    <img class="img-a" [secureSrc]="srcA" (imageError)="onErrorA()" />
    <img class="img-b" [secureSrc]="srcB" (imageError)="onErrorB()" />
  `,
  imports: [SecureImageDirective],
  standalone: true,
})
class MultiImageHostComponent {
  srcA: string | null = '/api/test/a';
  srcB: string | null = '/api/test/b';
  onErrorA = jasmine.createSpy('onErrorA');
  onErrorB = jasmine.createSpy('onErrorB');
}

describe('SecureImageDirective shared IntersectionObserver', () => {
  let fixture: ComponentFixture<MultiImageHostComponent>;
  let httpMock: HttpTestingController;
  let observerCallback: IntersectionObserverCallback;
  let constructorCount: number;
  let observedElements: Element[];
  let unobservedElements: Element[];
  let disconnectCount: number;
  const originalIntersectionObserver = window.IntersectionObserver;

  beforeEach(() => {
    constructorCount = 0;
    observedElements = [];
    unobservedElements = [];
    disconnectCount = 0;

    (window as any).IntersectionObserver = class {
      constructor(callback: IntersectionObserverCallback) {
        constructorCount++;
        observerCallback = callback;
      }
      observe(target: Element) {
        observedElements.push(target);
      }
      unobserve(target: Element) {
        unobservedElements.push(target);
      }
      disconnect() {
        disconnectCount++;
      }
    };

    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule, SecureImageDirective, MultiImageHostComponent],
    });
    fixture = TestBed.createComponent(MultiImageHostComponent);
    httpMock = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    httpMock.verify();
    (window as any).IntersectionObserver = originalIntersectionObserver;
  });

  it('reuses a single shared observer across multiple <img> hosts and handles batched [false, true] entries', () => {
    const imgA = fixture.nativeElement.querySelector('.img-a') as HTMLImageElement;
    const imgB = fixture.nativeElement.querySelector('.img-b') as HTMLImageElement;

    expect(constructorCount).toBe(1);
    expect(observedElements).toEqual([imgA, imgB]);

    observerCallback(
      [
        { isIntersecting: false, target: imgA } as unknown as IntersectionObserverEntry,
        { isIntersecting: true, target: imgB } as unknown as IntersectionObserverEntry,
      ],
      {} as IntersectionObserver,
    );

    expect(unobservedElements).toEqual([imgB]);
    expect(disconnectCount).toBe(0);

    httpMock.expectNone('/api/test/a');
    const reqB = httpMock.expectOne('/api/test/b');
    reqB.flush(new Blob(['imgB'], { type: 'image/jpeg' }));
    fixture.detectChanges();
    expect(imgB.src).toContain('blob:');
    expect(imgA.getAttribute('src')).toBeNull();

    observerCallback(
      [{ isIntersecting: true, target: imgA } as unknown as IntersectionObserverEntry],
      {} as IntersectionObserver,
    );

    expect(unobservedElements).toEqual([imgB, imgA]);
    expect(disconnectCount).toBe(1);

    const reqA = httpMock.expectOne('/api/test/a');
    reqA.flush(new Blob(['imgA'], { type: 'image/jpeg' }));
    fixture.detectChanges();
    expect(imgA.src).toContain('blob:');
  });
});
