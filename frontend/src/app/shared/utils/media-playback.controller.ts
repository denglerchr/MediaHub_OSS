// frontend/src/app/utils/media-playback.controller.ts
import { Observable, Subject, Subscription } from 'rxjs';
import { finalize, map, take, takeUntil } from 'rxjs/operators';
import { EntryService } from '../../core/services/entry.service';
import { AuthService } from '../../core/services/auth.service';
import { NotificationService } from '../../core/services/notification.service';

export interface MediaErrorOptions {
  dbId: string;
  entryId: number;
  currentUrl: string | null;
  mediaElement?: HTMLMediaElement | null;
  isStillActive: () => boolean;
  onRetryUrl: (freshUrl: string) => void;
  onFatalError?: () => void;
}

/**
 * Coordinates media streaming/blob lifecycles, object URL revocation, media element
 * detachment, and 401 token-refresh retries with playback position restoration.
 */
export class MediaPlaybackController {
  public static readonly MEDIA_URL_MAX_RETRIES = 2;

  private currentObjectUrl: string | null = null;
  private mediaUrlRetries = 0;
  private pendingResumeTime: number | null = null;
  private blobLoadSub?: Subscription;
  private errorRetrySub?: Subscription;
  private destroy$ = new Subject<void>();

  constructor(
    private entryService: EntryService,
    private authService: AuthService,
    private notificationService: NotificationService,
  ) {}

  public resetForNewEntry(): void {
    this.mediaUrlRetries = 0;
    this.pendingResumeTime = null;
    this.cancelBlobLoad();
    this.cancelErrorRetry();
    this.revokeObjectUrl();
  }

  public createStreamUrl(dbId: string, entryId: number): string {
    return this.entryService.getEntryFileUrl(dbId, entryId);
  }

  public loadImageBlob(dbId: string, entryId: number): Observable<string> {
    return this.entryService.getEntryFileBlob(dbId, entryId).pipe(
      takeUntil(this.destroy$),
      map((blob) => {
        this.revokeObjectUrl();
        this.currentObjectUrl = URL.createObjectURL(blob);
        return this.currentObjectUrl;
      }),
    );
  }

  public trackBlobSubscription(sub: Subscription): void {
    this.cancelBlobLoad();
    this.blobLoadSub = sub;
  }

  public cancelBlobLoad(): void {
    if (this.blobLoadSub) {
      this.blobLoadSub.unsubscribe();
      this.blobLoadSub = undefined;
    }
  }

  private cancelErrorRetry(): void {
    if (this.errorRetrySub) {
      this.errorRetrySub.unsubscribe();
      this.errorRetrySub = undefined;
    }
  }

  public isCurrentObjectUrl(url: string | null): boolean {
    return !!this.currentObjectUrl && url === this.currentObjectUrl;
  }

  public revokeObjectUrl(): void {
    if (this.currentObjectUrl) {
      URL.revokeObjectURL(this.currentObjectUrl);
      this.currentObjectUrl = null;
    }
  }

  public detachMediaElements(
    elements: Iterable<HTMLMediaElement> | HTMLMediaElement | null | undefined,
  ): void {
    if (!elements) {
      return;
    }
    const list: Iterable<HTMLMediaElement> =
      Symbol.iterator in Object(elements)
        ? (elements as Iterable<HTMLMediaElement>)
        : [elements as HTMLMediaElement];
    for (const el of list) {
      el.pause();
      el.removeAttribute('src');
      el.load();
    }
  }

  public teardown(elements?: Iterable<HTMLMediaElement> | HTMLMediaElement | null): void {
    this.cancelBlobLoad();
    this.cancelErrorRetry();
    this.pendingResumeTime = null;
    this.mediaUrlRetries = 0;
    this.detachMediaElements(elements);
    this.revokeObjectUrl();
  }

  public destroy(elements?: Iterable<HTMLMediaElement> | HTMLMediaElement | null): void {
    this.destroy$.next();
    this.destroy$.complete();
    this.teardown(elements);
  }

  public handleMediaError(options: MediaErrorOptions): void {
    if (!options.currentUrl) {
      return;
    }

    if (this.isCurrentObjectUrl(options.currentUrl)) {
      this.notificationService.showError('Media playback failed — the file could not be loaded.');
      options.onFatalError?.();
      return;
    }

    if (this.errorRetrySub && !this.errorRetrySub.closed) {
      return;
    }

    const el = options.mediaElement;
    const resumeTime =
      el && typeof el.currentTime === 'number' && el.currentTime > 0 ? el.currentTime : null;

    this.errorRetrySub = this.authService
      .forceAccessTokenRefresh()
      .pipe(
        take(1),
        takeUntil(this.destroy$),
        finalize(() => {
          this.errorRetrySub = undefined;
        }),
      )
      .subscribe(() => {
        if (!options.isStillActive()) {
          return;
        }
        const freshUrl = this.entryService.getEntryFileUrl(options.dbId, options.entryId);
        if (
          this.mediaUrlRetries >= MediaPlaybackController.MEDIA_URL_MAX_RETRIES ||
          freshUrl === options.currentUrl
        ) {
          this.notificationService.showError(
            'Media playback failed — the file could not be loaded.',
          );
          options.onFatalError?.();
          return;
        }
        this.pendingResumeTime = resumeTime;
        this.mediaUrlRetries++;
        options.onRetryUrl(freshUrl);
      });
  }

  public restorePlaybackPosition(el: HTMLMediaElement | null | undefined): void {
    if (el && this.pendingResumeTime !== null && this.pendingResumeTime > 0) {
      try {
        el.currentTime = this.pendingResumeTime;
      } catch {
        // Some engines refuse the seek — fall back to playing from the start.
      }
    }
    this.pendingResumeTime = null;
  }
}
