// frontend/src/app/directives/secure-image.directive.spec.ts
import { Component, DebugElement } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { HttpClientTestingModule, HttpTestingController } from '@angular/common/http/testing';
import { SecureImageDirective } from './secure-image.directive';

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
});
