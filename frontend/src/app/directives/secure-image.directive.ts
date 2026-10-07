// frontend/src/app/directives/secure-image.directive.ts
import {
  Directive,
  ElementRef,
  Input,
  Output,
  EventEmitter,
  OnChanges,
  OnDestroy,
  SimpleChanges,
  Renderer2,
  HostListener,
} from '@angular/core';
import { Subscription, BehaviorSubject, of } from 'rxjs';
import { switchMap, filter, catchError, map } from 'rxjs/operators';
import { ImageCacheService } from '../services/image-cache.service';

/**
 * Directive to load images/media securely using ImageCacheService and JwtInterceptor.
 * Optimized to load images lazily via IntersectionObserver when entering the viewport.
 */
@Directive({
  selector: '[secureSrc]',
  standalone: true,
})
export class SecureImageDirective implements OnChanges, OnDestroy {
  @Input() secureSrc: string | null = null;
  @Output() imageError = new EventEmitter<void>();
  @Output() aspectLoaded = new EventEmitter<number>();

  private currentUrlSubject = new BehaviorSubject<string | null>(null);
  private subscription: Subscription;
  private observer: IntersectionObserver | null = null;
  private isVisible = false;

  // FE-025: source URLs whose blob this element currently uses (displayed or still
  // loading) — kept referenced so LRU eviction defers revocation.
  private retainedUrls = new Set<string>();
  private displayedUrl: string | null = null;
  private inFlightUrl: string | null = null;

  constructor(
    private el: ElementRef,
    private imageCacheService: ImageCacheService,
    private renderer: Renderer2,
  ) {
    this.subscription = this.currentUrlSubject
      .pipe(
        filter(() => this.isVisible),
        switchMap((url) => {
          // FE-026: `null` must reach switchMap so the pending fetch is cancelled —
          // dropping it (as the old `!!url` filter did) lets the stale fetch complete
          // and re-apply the previous entry's image.
          if (!url) {
            this.releaseInFlight();
            return of({ url: null, blobUrl: null });
          }

          this.releaseInFlight();
          this.inFlightUrl = url;
          this.retain(url);
          this.renderer.addClass(this.el.nativeElement, 'loading-image');
          return this.imageCacheService.getBlobUrl(url).pipe(
            map((blobUrl) => ({ url, blobUrl })),
            catchError((err) => {
              console.error('Error loading secure image:', err);
              this.renderer.removeClass(this.el.nativeElement, 'loading-image');
              this.imageError.emit();
              return of({ url, blobUrl: null });
            }),
          );
        }),
      )
      .subscribe({
        next: ({ url, blobUrl }) => {
          if (this.inFlightUrl === url) {
            this.inFlightUrl = null;
          }
          this.renderer.removeClass(this.el.nativeElement, 'loading-image');

          if (url && blobUrl) {
            const previousUrl = this.displayedUrl;
            this.renderer.setAttribute(this.el.nativeElement, 'src', blobUrl);
            this.displayedUrl = url;
            if (previousUrl && previousUrl !== url) {
              this.release(previousUrl);
            }
          } else {
            // Explicit null (entry switched away) or a failed load: never leave the
            // previous entry's image on screen.
            this.renderer.removeAttribute(this.el.nativeElement, 'src');
            if (url) {
              this.release(url);
            }
            if (this.displayedUrl) {
              this.release(this.displayedUrl);
              this.displayedUrl = null;
            }
          }
        },
      });

    this.setupIntersectionObserver();
  }

  private setupIntersectionObserver(): void {
    if (typeof IntersectionObserver !== 'undefined') {
      this.observer = new IntersectionObserver(
        (entries) => {
          const entry = entries[0];
          if (entry && entry.isIntersecting) {
            this.isVisible = true;
            if (this.secureSrc) {
              this.currentUrlSubject.next(this.secureSrc);
            }
            this.disconnectObserver();
          }
        },
        {
          rootMargin: '200px', // Start loading 200px before entering viewport
        },
      );
      this.observer.observe(this.el.nativeElement);
    } else {
      this.isVisible = true;
    }
  }

  private disconnectObserver(): void {
    if (this.observer) {
      this.observer.disconnect();
      this.observer = null;
    }
  }

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['secureSrc']) {
      if (!this.secureSrc) {
        this.renderer.removeAttribute(this.el.nativeElement, 'src');
      }

      if (this.isVisible) {
        this.currentUrlSubject.next(this.secureSrc);
      }
    }
  }

  ngOnDestroy(): void {
    this.subscription.unsubscribe();
    this.disconnectObserver();
    this.releaseInFlight();
    if (this.displayedUrl) {
      this.release(this.displayedUrl);
      this.displayedUrl = null;
    }
  }

  // --- FE-025: reference-counted use of the cached blob URLs ---

  private retain(url: string): void {
    if (this.retainedUrls.has(url)) {
      return;
    }
    this.retainedUrls.add(url);
    this.imageCacheService.retain(url);
  }

  private release(url: string | null): void {
    if (url && this.retainedUrls.delete(url)) {
      this.imageCacheService.release(url);
    }
  }

  private releaseInFlight(): void {
    if (this.inFlightUrl) {
      this.release(this.inFlightUrl);
      this.inFlightUrl = null;
    }
  }

  @HostListener('load')
  onLoad(): void {
    const img = this.el.nativeElement as HTMLImageElement;
    if (img && img.naturalWidth && img.naturalHeight) {
      const ar = img.naturalWidth / img.naturalHeight;
      this.aspectLoaded.emit(ar);
    }
  }
}
