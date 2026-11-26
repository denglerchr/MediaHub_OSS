import {
  Component,
  OnInit,
  OnDestroy,
  Input,
  Output,
  EventEmitter,
  ElementRef,
  ViewChild,
  HostListener,
  ChangeDetectorRef,
} from '@angular/core';
import { Subject, Subscription, of, timer } from 'rxjs';
import { switchMap, takeUntil, catchError, filter, take } from 'rxjs/operators';
import { Entry, SearchRequest } from '../../models';
import { EntryService } from '../../services/entry.service';
import { AuthService } from '../../services/auth.service';
import { NotificationService } from '../../services/notification.service';
import { FullscreenSettings } from '../fullscreen-settings-modal/fullscreen-settings-modal.component';
import { isMimeTypeStreamable } from '../../utils/mime-types';

@Component({
  selector: 'app-fullscreen-player',
  templateUrl: './fullscreen-player.component.html',
  styleUrls: ['./fullscreen-player.component.css'],
  standalone: false,
})
export class FullscreenPlayerComponent implements OnInit, OnDestroy {
  @Input() settings!: FullscreenSettings;
  @Input() dbId!: string; // UPDATED: Changed from dbName to dbId
  @Input() contentType!: string;

  @Output() exit = new EventEmitter<void>();

  // Use ViewChild to get access to the main container for the Fullscreen API
  @ViewChild('playerContainer', { static: true }) playerContainer!: ElementRef;

  // To access native video/audio elements to play/pause programmatically
  @ViewChild('mediaElement') mediaElement?: ElementRef<HTMLMediaElement>;

  public currentEntry: Entry | null = null;
  public mediaUrl: string | null = null;
  public isLoading = true;

  private playlist: Entry[] = [];
  private currentIndex: number = -1;
  private knownEntryIds = new Set<number>();

  private delayTimerSub?: Subscription;
  private pollingSub?: Subscription;
  private imageLoadSub?: Subscription;
  private currentBlobUrl: string | null = null;
  private pendingResumeTime: number | null = null;
  private destroy$ = new Subject<void>();

  // FE-011: tokenized media URLs expire mid-presentation — see onMediaError().
  private static readonly MEDIA_URL_MAX_RETRIES = 2;
  private mediaUrlRetries = 0;

  constructor(
    private entryService: EntryService,
    private authService: AuthService,
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    this.requestNativeFullscreen();
    this.startPolling();
  }

  /**
   * Listens for the browser's native fullscreen exit event (e.g., user presses ESC)
   */
  @HostListener('document:fullscreenchange')
  onFullscreenChange(): void {
    if (!document.fullscreenElement) {
      this.closePlayer();
    }
  }

  @HostListener('document:keydown.escape')
  onEscapeKey(): void {
    this.closePlayer();
  }

  /**
   * Requests the browser to take the container full screen.
   */
  private requestNativeFullscreen(): void {
    const el = this.playerContainer.nativeElement;
    if (el.requestFullscreen) {
      el.requestFullscreen().catch((err: unknown) => {
        console.error('Error attempting to enable fullscreen:', err);
        this.notificationService.showError(
          'Fullscreen API is blocked or not supported by your browser.',
        );
      });
    }
  }

  /**
   * Starts a background polling loop to fetch the latest N entries.
   */
  private startPolling(): void {
    // Poll every 5 seconds to check for new entries
    this.pollingSub = timer(0, 5000)
      .pipe(
        takeUntil(this.destroy$),
        switchMap(() => {
          const searchPayload: SearchRequest = {
            pagination: { limit: this.settings.entryLimit, offset: 0 },
            sort: { field: 'timestamp', direction: 'desc' },
          };
          // FE-014: keep the poll alive across transient failures — a single error must
          // not silently stop the presentation from discovering new entries (and must
          // not pop a global toast every 5 s either).
          return this.entryService
            .searchEntries(this.dbId, searchPayload, { silent: true })
            .pipe(catchError(() => of(null)));
        }),
        filter((entries): entries is Entry[] => entries !== null),
      )
      .subscribe({
        next: (entries) => this.handleFetchedEntries(entries),
      });
  }

  /**
   * Compares newly fetched entries against the current queue and updates if necessary.
   */
  private handleFetchedEntries(entries: Entry[]): void {
    // N6: drop playlist items that vanished from the fetched window (deleted) or
    // stopped being `ready` (failed / re-processed) mid-presentation.
    this.prunePlaylist(entries);

    // Filter out entries that aren't fully processed yet to prevent broken media
    const readyEntries = entries.filter((e) => e.status === 'ready');

    if (readyEntries.length === 0) {
      this.isLoading = false;
      this.cdr.markForCheck();
      return;
    }

    if (this.playlist.length === 0) {
      // First load: the whole result set becomes the playlist.
      readyEntries.forEach((e) => this.knownEntryIds.add(e.id));
      this.playlist = [...readyEntries];
      if (this.settings.shuffle) {
        this.shuffleArray(this.playlist);
      }
      this.currentIndex = -1;
      this.playNext();
      this.isLoading = false;
      return;
    }

    // Check if we have any truly *new* entries that we haven't seen before
    const newEntries = readyEntries.filter((e) => !this.knownEntryIds.has(e.id));
    if (newEntries.length === 0) {
      this.isLoading = false;
      return;
    }

    if (this.settings.entryLimit === 1 && !this.settings.repeat) {
      const newest = newEntries[0];
      this.knownEntryIds.add(newest.id);
      this.playlist = [newest];
      this.currentIndex = -1;
      this.playNext();
      this.isLoading = false;
      return;
    }

    // FE-014: never replace or re-shuffle the playlist mid-presentation. Track the
    // current entry by id and append the new entries to the tail.
    newEntries.forEach((e) => this.knownEntryIds.add(e.id));
    this.playlist = [...this.playlist, ...newEntries];

    const currentIndex = this.currentEntry
      ? this.playlist.findIndex((e) => e.id === this.currentEntry!.id)
      : -1;
    if (currentIndex !== -1) {
      this.currentIndex = currentIndex;
    }

    if (this.settings.shuffle) {
      // Shuffle only the not-yet-played tail so the visible slide cannot change.
      const played = this.playlist.slice(0, this.currentIndex + 1);
      const pending = this.playlist.slice(this.currentIndex + 1);
      this.shuffleArray(pending);
      this.playlist = [...played, ...pending];
    }

    // If this is the very first load, start playback
    if (this.currentEntry === null) {
      this.playNext();
    }
    this.isLoading = false;
  }

  /**
   * N6: prunes the playlist against the freshly fetched window.
   *
   * A playlist item is dropped when it is inside the fetched window
   * (`timestamp >= window floor`) but either no longer comes back from the search
   * (deleted) or is no longer `ready`. Entries older than the fetched window cannot
   * be verified (the search returns only the latest N) and are kept. If the visible
   * entry itself disappears, the presentation advances to the next one instead of
   * showing a dead slide.
   */
  private prunePlaylist(entries: Entry[]): void {
    if (this.playlist.length === 0) {
      return;
    }

    if (entries.length === 0) {
      this.playlist = [];
      this.knownEntryIds.clear();
      this.releaseMedia();
      this.currentEntry = null;
      this.currentIndex = -1;
      this.isLoading = false;
      this.cdr.markForCheck();
      return;
    }

    const fetchedById = new Map(entries.map((e) => [e.id, e] as const));
    const windowFloor = entries.reduce(
      (min, e) => Math.min(min, e.timestamp || 0),
      Number.MAX_SAFE_INTEGER,
    );

    const kept = this.playlist.filter((item) => {
      const fetched = fetchedById.get(item.id);
      if (fetched) {
        return fetched.status === 'ready';
      }
      return (item.timestamp || 0) < windowFloor;
    });

    if (kept.length === this.playlist.length) {
      return;
    }

    const currentId = this.currentEntry?.id ?? null;
    this.playlist = kept;
    this.knownEntryIds = new Set(kept.map((e) => e.id));

    if (kept.length === 0) {
      // Everything vanished (e.g. the whole page of entries was deleted).
      this.releaseMedia();
      this.currentEntry = null;
      this.currentIndex = -1;
      this.isLoading = false;
      this.cdr.markForCheck();
      return;
    }

    if (currentId !== null) {
      const idx = this.playlist.findIndex((e) => e.id === currentId);
      if (idx !== -1) {
        this.currentIndex = idx;
      } else {
        // The visible entry was deleted mid-presentation — advance to its successor.
        this.currentIndex = Math.min(this.currentIndex, this.playlist.length) - 1;
        this.playNext();
      }
    } else if (this.currentIndex >= this.playlist.length) {
      this.currentIndex = this.playlist.length - 1;
    }
  }

  /**
   * Advances the playlist.
   */
  private playNext(): void {
    this.clearDelayTimer();

    if (this.playlist.length === 0) return;

    this.currentIndex++;

    // Handle reaching the end of the playlist
    if (this.currentIndex >= this.playlist.length) {
      if (this.settings.repeat) {
        this.currentIndex = 0; // Loop back to start
        if (this.settings.shuffle) {
          this.shuffleArray(this.playlist);
        }
      } else {
        if (this.settings.entryLimit === 1) {
          this.currentIndex = 0;
          if (this.contentType !== 'image') {
            return;
          }
        } else {
          this.notificationService.showInfo('Presentation finished.');
          this.closePlayer();
          return;
        }
      }
    }

    this.currentEntry = this.playlist[this.currentIndex];
    this.loadMedia(this.currentEntry);
  }

  private loadMedia(entry: Entry): void {
    // FE-011: fresh entry → fresh media-URL retry budget.
    this.mediaUrlRetries = 0;
    this.pendingResumeTime = null;
    this.cancelImageLoad();
    this.revokeBlobUrl();

    const mime = entry.mime_type || 'file';
    const isStreamable = isMimeTypeStreamable(mime);

    if (this.contentType === 'image' && !isStreamable) {
      this.mediaUrl = null;
      this.cdr.detectChanges();

      const entryId = entry.id;
      this.imageLoadSub = this.entryService
        .getEntryFileBlob(this.dbId, entryId)
        .pipe(takeUntil(this.destroy$))
        .subscribe({
          next: (blob) => {
            if (!this.currentEntry || this.currentEntry.id !== entryId) {
              return;
            }
            this.revokeBlobUrl();
            this.currentBlobUrl = URL.createObjectURL(blob);
            this.mediaUrl = this.currentBlobUrl;
            this.cdr.detectChanges();
            this.startDelayTimer();
          },
          error: () => {
            if (!this.currentEntry || this.currentEntry.id !== entryId) {
              return;
            }
            this.mediaUrl = null;
            this.cdr.detectChanges();
            this.startDelayTimer();
          },
        });
      return;
    }

    if (isStreamable) {
      const nextUrl = this.entryService.getEntryFileUrl(this.dbId, entry.id); // UPDATED: Pass dbId
      const el = this.mediaElement?.nativeElement;
      if (this.settings.repeat && this.playlist.length === 1 && el && this.mediaUrl === nextUrl) {
        el.currentTime = 0;
        el.play()?.catch(() => {});
        return;
      }
      this.mediaUrl = nextUrl;
    } else {
      this.mediaUrl = null;
    }

    // Force the screen to redraw with the new URL!
    this.cdr.detectChanges();

    if (this.contentType === 'image' || !this.mediaUrl) {
      this.startDelayTimer();
    }
  }

  /**
   * Called by the (ended) event of the <video> or <audio> tags in the HTML template.
   */
  public onMediaEnded(): void {
    // Video/Audio finished. Now we wait the user-defined delay before switching.
    this.startDelayTimer();
  }

  /**
   * Restores the playback position after a streamable media element reloads with a refreshed token.
   */
  public onMediaLoadedMetadata(): void {
    if (this.pendingResumeTime !== null && this.pendingResumeTime > 0) {
      const el = this.mediaElement?.nativeElement;
      if (el) {
        el.currentTime = this.pendingResumeTime;
      }
    }
    this.pendingResumeTime = null;
  }

  /**
   * FE-011: the media element failed (typically a 401 after the token embedded in
   * the URL expired). Refresh the access token first — the media element's 401
   * never reaches the interceptor, so nothing else has refreshed the *expired*
   * token the failed URL carried — then re-generate the URL with the current
   * access token and let the element retry; give up with a visible error instead
   * of looping.
   */
  public onMediaError(): void {
    if (!this.currentEntry || !this.mediaUrl) {
      return;
    }
    const entryId = this.currentEntry.id;

    if (this.currentBlobUrl && this.mediaUrl === this.currentBlobUrl) {
      this.notificationService.showError('Media playback failed — the file could not be loaded.');
      this.startDelayTimer();
      return;
    }

    const resumeTime = this.mediaElement?.nativeElement.currentTime || 0;

    this.authService
      .forceAccessTokenRefresh()
      .pipe(take(1), takeUntil(this.destroy$))
      .subscribe(() => {
        // The playlist may have advanced while the refresh was in flight — never
        // overwrite another entry's media URL.
        if (!this.currentEntry || this.currentEntry.id !== entryId || !this.mediaUrl) {
          return;
        }
        const freshUrl = this.entryService.getEntryFileUrl(this.dbId, entryId);
        if (
          this.mediaUrlRetries >= FullscreenPlayerComponent.MEDIA_URL_MAX_RETRIES ||
          freshUrl === this.mediaUrl
        ) {
          this.notificationService.showError(
            'Media playback failed — the file could not be loaded.',
          );
          this.startDelayTimer();
          return;
        }
        this.mediaUrlRetries++;
        this.pendingResumeTime = resumeTime > 0 ? resumeTime : null;
        this.mediaUrl = freshUrl;
        this.cdr.detectChanges();
      });
  }

  /**
   * Starts the countdown to the next slide.
   */
  private startDelayTimer(): void {
    this.clearDelayTimer();
    // Convert seconds to milliseconds
    const delayMs = this.settings.delaySeconds * 1000;

    this.delayTimerSub = timer(delayMs).subscribe(() => {
      this.playNext();
    });
  }

  private clearDelayTimer(): void {
    if (this.delayTimerSub) {
      this.delayTimerSub.unsubscribe();
      this.delayTimerSub = undefined;
    }
  }

  /**
   * Standard Fisher-Yates shuffle algorithm.
   */
  private shuffleArray<T>(array: T[]): void {
    for (let i = array.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [array[i], array[j]] = [array[j], array[i]];
    }
  }

  private cancelImageLoad(): void {
    if (this.imageLoadSub) {
      this.imageLoadSub.unsubscribe();
      this.imageLoadSub = undefined;
    }
  }

  private revokeBlobUrl(): void {
    if (this.currentBlobUrl) {
      URL.revokeObjectURL(this.currentBlobUrl);
      this.currentBlobUrl = null;
    }
  }

  /**
   * FE-023: stop playback and detach the media element (the declared-but-unused
   * `mediaElement` ViewChild exists exactly for this).
   */
  private releaseMedia(): void {
    this.cancelImageLoad();
    this.pendingResumeTime = null;
    const el = this.mediaElement?.nativeElement;
    if (el) {
      el.pause();
      el.removeAttribute('src');
      el.load();
    }
    this.revokeBlobUrl();
    this.mediaUrl = null;
  }

  /**
   * Cleans up fullscreen and notifies the parent to destroy this component.
   */
  public closePlayer(): void {
    // FE-023: pause immediately — ESC exits fullscreen and audio must stop right away,
    // not after (or despite) component destruction.
    this.releaseMedia();
    if (document.fullscreenElement) {
      document.exitFullscreen().catch((err) => console.error(err));
    }
    this.exit.emit();
  }

  ngOnDestroy(): void {
    this.releaseMedia();
    this.clearDelayTimer();
    if (this.pollingSub) {
      this.pollingSub.unsubscribe();
    }
    this.destroy$.next();
    this.destroy$.complete();
  }
}
