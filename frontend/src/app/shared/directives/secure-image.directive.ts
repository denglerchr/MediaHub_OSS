// frontend/src/app/directives/secure-image.directive.ts
import {
  Directive,
  ElementRef,
  Input,
  Output,
  EventEmitter,
  Injectable,
  NgZone,
  OnChanges,
  OnDestroy,
  SimpleChanges,
  Renderer2,
  HostListener,
} from '@angular/core';
import { Subscription, BehaviorSubject, of } from 'rxjs';
import { switchMap, filter, catchError, map } from 'rxjs/operators';
import { ImageCacheService } from '../../core/services/image-cache.service';

/**
 * Shared IntersectionObserver manager running outside NgZone to avoid creating
 * one observer per image element and unnecessary zone change-detection ticks.
 */
@Injectable({
  providedIn: 'root',
})
export class SecureImageObserverService implements OnDestroy {
  private observer: IntersectionObserver | null = null;
  private callbacks = new Map<Element, () => void>();

  constructor(private ngZone: NgZone) {}

  observe(el: Element, cb: () => void): void {
    if (typeof IntersectionObserver === 'undefined') {
      cb();
      return;
    }

    const observer = this.ensureObserver();
    this.callbacks.set(el, cb);
    observer.observe(el);
  }

  unobserve(el: Element): void {
    if (!this.callbacks.delete(el)) {
      return;
    }

    if (this.observer) {
      this.observer.unobserve(el);
      if (this.callbacks.size === 0) {
        this.observer.disconnect();
        this.observer = null;
      }
    }
  }

  ngOnDestroy(): void {
    if (this.observer) {
      this.observer.disconnect();
      this.observer = null;
    }
    this.callbacks.clear();
  }

  private ensureObserver(): IntersectionObserver {
    if (!this.observer) {
      this.observer = this.ngZone.runOutsideAngular(
        () =>
          new IntersectionObserver(
            (entries) => {
              for (const entry of entries) {
                if (!entry.isIntersecting) {
                  continue;
                }
                const cb = this.callbacks.get(entry.target);
                if (cb) {
                  this.unobserve(entry.target);
                  this.ngZone.run(cb);
                }
              }
            },
            {
              rootMargin: '200px', // Start loading 200px before entering viewport
            },
          ),
      );
    }
    return this.observer;
  }
}

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
    private observerService: SecureImageObserverService,
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
      this.observerService.observe(this.el.nativeElement, () => {
        this.isVisible = true;
        if (this.secureSrc) {
          this.currentUrlSubject.next(this.secureSrc);
        }
      });
    } else {
      this.isVisible = true;
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
    this.observerService.unobserve(this.el.nativeElement);
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
      if (this.inFlightUrl !== this.displayedUrl) {
        this.release(this.inFlightUrl);
      }
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

  @HostListener('error')
  onImageError(): void {
    if (!this.displayedUrl) {
      return;
    }
    this.renderer.removeAttribute(this.el.nativeElement, 'src');
    this.release(this.displayedUrl);
    this.displayedUrl = null;
    this.renderer.removeClass(this.el.nativeElement, 'loading-image');
    this.imageError.emit();
  }
}
