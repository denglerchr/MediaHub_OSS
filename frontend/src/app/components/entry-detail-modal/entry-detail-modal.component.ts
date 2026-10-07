import { Component, OnDestroy, OnInit, ViewChildren, QueryList, ElementRef } from '@angular/core';
import { Subject, Observable, of, combineLatest } from 'rxjs';
import {
  switchMap,
  takeUntil,
  map,
  take,
  filter,
  tap,
  withLatestFrom,
  catchError,
  finalize,
} from 'rxjs/operators';
import { Database, Entry, User } from '../../models';
import { EntryService } from '../../services/entry.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { DomSanitizer, SafeUrl } from '@angular/platform-browser';
import { AuthService } from '../../services/auth.service';
import { NotificationService } from '../../services/notification.service';
import {
  ConfirmationModalComponent,
  ConfirmationModalData,
} from '../confirmation-modal/confirmation-modal.component';
import { EditEntryModalComponent } from '../edit-entry-modal/edit-entry-modal.component';
import { isMimeTypeStreamable } from '../../utils/mime-types';

@Component({
  selector: 'app-entry-detail-modal',
  templateUrl: './entry-detail-modal.component.html',
  styleUrls: ['./entry-detail-modal.component.css'],
  standalone: false,
})
export class EntryDetailModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'entryDetailModal';

  public entryForMetadata: Entry | null = null;
  public fileUrl: SafeUrl | null = null;
  public isLoadingFile: boolean = true;
  // FE-012: distinguishes "the load failed" from "still loading"/"no preview".
  public loadError = false;

  public currentUser$: Observable<User | null>;
  public previewUrl: string | null = null;

  public currentDatabase: Database | null = null;

  public canEdit = false;
  public canDelete = false;

  // FE-023: the native media elements that must be stopped when the modal closes.
  @ViewChildren('detailMedia') mediaElements?: QueryList<ElementRef<HTMLMediaElement>>;

  private currentObjectUrl: string | null = null;
  private pendingEntry: Entry | null = null;
  private destroy$ = new Subject<void>();

  // FE-011: `getEntryFileUrl` embeds a *snapshot* of the short-lived access token
  // in the query string (the browser's media engine cannot send an Authorization
  // header). A stream that outlives the token (~5 min) starts failing its Range
  // requests with 401 — see onMediaError(), which re-generates the URL with a
  // fresh token. Backend follow-up: signed per-resource URLs would remove the
  // token from the URL entirely.
  private static readonly MEDIA_URL_MAX_RETRIES = 2;
  private mediaUrlRetries = 0;
  private currentStreamUrl: string | null = null;
  private pendingResumeTime: number | null = null;
  public isDownloading = false;

  constructor(
    private modalService: ModalService,
    private entryService: EntryService,
    private databaseService: DatabaseService,
    private sanitizer: DomSanitizer,
    private authService: AuthService,
    private notificationService: NotificationService,
  ) {
    this.currentUser$ = this.authService.currentUser$;
  }

  ngOnInit(): void {
    // Keep internal state updated
    combineLatest([this.databaseService.selectedDatabase$, this.currentUser$])
      .pipe(takeUntil(this.destroy$))
      .subscribe(([db, user]) => {
        this.currentDatabase = db;
        this.updatePermissions(user, db);
      });

    this.entryService.selectedEntry$
      .pipe(
        takeUntil(this.destroy$),
        withLatestFrom(this.databaseService.selectedDatabase$),
        switchMap(([entry, currentDb]) => {
          // Metadata-only refresh: the currently displayed entry is re-published with
          // new metadata (EntryService.updateEntry after a save in the edit modal).
          // Show the new values but do NOT tear down or reload the media — a playing
          // video must not restart because someone edited its filename. Only applies
          // while the media is actually attached: after a failed load (fileUrl null)
          // a re-publication must fall through to the full load path, otherwise
          // retryLoad() could never recover.
          if (
            entry &&
            currentDb &&
            this.entryForMetadata &&
            entry.id === this.entryForMetadata.id &&
            this.fileUrl &&
            !this.loadError
          ) {
            this.pendingEntry = entry;
            this.entryForMetadata = { ...this.entryForMetadata, ...entry };
            return of(null);
          }

          this.revokeFileObjectUrl();

          this.fileUrl = null;
          this.previewUrl = null;
          this.isLoadingFile = true;
          this.loadError = false;
          // FE-011: fresh entry → fresh media-URL retry budget.
          this.mediaUrlRetries = 0;
          this.currentStreamUrl = null;
          this.pendingResumeTime = null;
          this.pendingEntry = entry;

          // Pre-fill the metadata with the shallow entry from the list.
          // This gives the HTML template immediate access to 'entry.mime_type',
          this.entryForMetadata = entry;

          if (entry && currentDb) {
            // UPDATED: Pass currentDb.id instead of currentDb.name
            return this.entryService.getEntryMeta(currentDb.id, entry.id).pipe(
              switchMap((metaEntry) => {
                // The HTTP request finished! Overwrite the shallow entry with the full metadata
                this.entryForMetadata = metaEntry;
                this.previewUrl = this.getPreviewUrl(currentDb.id, metaEntry.id);

                // 1. Check our central utility to see if the browser supports it
                const mime = metaEntry.mime_type || 'file';
                const isStreamable = isMimeTypeStreamable(mime);

                if (isStreamable) {
                  // STREAMING SUPPORTED: Provide direct URL
                  const streamUrl = this.entryService.getEntryFileUrl(currentDb.id, entry.id);
                  this.currentStreamUrl = streamUrl;
                  this.fileUrl = this.sanitizer.bypassSecurityTrustUrl(streamUrl);
                  this.isLoadingFile = false;

                  return of('streaming_ready');
                } else if (mime.startsWith('image/')) {
                  // IMAGES: Fall back to Blob download
                  return this.entryService.getEntryFileBlob(currentDb.id, entry.id).pipe(
                    map((blob) => {
                      this.currentObjectUrl = URL.createObjectURL(blob);
                      this.fileUrl = this.sanitizer.bypassSecurityTrustUrl(this.currentObjectUrl);
                      this.isLoadingFile = false;
                      return 'loaded';
                    }),
                    tap({ error: () => (this.isLoadingFile = false) }),
                  );
                } else {
                  // UNSUPPORTED MEDIA / GENERIC FILES
                  this.isLoadingFile = false;
                  return of('unsupported_file');
                }
              }),
              // FE-012: contain load errors inside the inner stream — one failed request
              // must not terminate the (singleton, session-lifetime) subscription and
              // brick the modal for the rest of the session.
              catchError(() => {
                this.loadError = true;
                this.isLoadingFile = false;
                return of(null);
              }),
            );
          } else {
            this.isLoadingFile = false;
            return of(null);
          }
        }),
      )
      .subscribe({
        // FE-012: defence in depth — anything escaping the inner stream still must not
        // leave the modal spinning forever.
        error: () => {
          this.loadError = true;
          this.isLoadingFile = false;
        },
      });

    // FE-023/FE-024: ESC / X / overlay close through ModalComponent -> ModalService
    // without ever calling this component's closeModal(). Subscribe to the close
    // event so media playback stops and object URLs are released on every path.
    this.modalService
      .getModalEvents(EntryDetailModalComponent.MODAL_ID)
      .pipe(
        filter((event) => event.action === 'close'),
        takeUntil(this.destroy$),
      )
      .subscribe(() => this.teardown());
  }

  /**
   * FE-012: re-runs the load for the same entry after a failure.
   */
  public retryLoad(): void {
    if (this.pendingEntry) {
      this.loadError = false;
      this.entryService.selectEntry(this.pendingEntry);
    }
  }

  /**
   * FE-023/FE-024: stop playback, detach the media element and release the object
   * URL. Idempotent — runs on every modal close path and on destroy.
   */
  private teardown(): void {
    this.mediaElements?.forEach((ref) => {
      const el = ref.nativeElement;
      el.pause();
      el.removeAttribute('src');
      el.load();
    });

    this.revokeFileObjectUrl();

    this.fileUrl = null;
    this.previewUrl = null;
    this.isLoadingFile = true;
    this.loadError = false;
    this.entryForMetadata = null;
    this.pendingEntry = null;
    this.entryService.clearSelectedEntry();
  }

  private updatePermissions(user: User | null, db: Database | null): void {
    if (!user || !db) {
      this.canEdit = false;
      this.canDelete = false;
      return;
    }

    if (user.is_admin) {
      this.canEdit = true;
      this.canDelete = true;
    } else {
      // UPDATED: Match against database_id and db.id
      const dbPermission = user.permissions?.find((p) => p.database_id === db.id);
      this.canEdit = dbPermission?.can_edit || false;
      this.canDelete = dbPermission?.can_delete || false;
    }
  }

  // UPDATED: Renamed parameter to dbId
  public getPreviewUrl(dbId: string, entryId: number): string {
    return this.entryService.getEntryPreviewUrl(dbId, entryId);
  }

  onEdit(): void {
    if (!this.entryForMetadata) return;

    if (this.entryForMetadata.status === 'processing') {
      this.notificationService.showError('Cannot edit an entry that is still processing.');
      return;
    }

    this.modalService.open(EditEntryModalComponent.MODAL_ID);
  }

  onDelete(): void {
    if (!this.currentDatabase || !this.entryForMetadata) return;

    if (this.entryForMetadata.status === 'processing') {
      this.notificationService.showError('Cannot delete an entry that is still processing.');
      return;
    }

    if (this.entryForMetadata.status === 'queued') {
      this.notificationService.showError('Cannot delete an entry that is still queued.');
      return;
    }

    const modalData: ConfirmationModalData = {
      title: 'Delete Entry',
      message: `Are you sure you want to delete entry ${this.entryForMetadata.id}? This action cannot be undone.`,
    };

    this.modalService
      .open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(
        take(1),
        filter((confirmed) => confirmed === true),
      )
      .subscribe(() => {
        this.isLoadingFile = true;

        this.entryService
          .deleteEntry(this.currentDatabase!.id, this.entryForMetadata!.id)
          .pipe(takeUntil(this.destroy$))
          .subscribe({
            next: () => {
              this.closeModal();
            },
            error: () => {
              this.isLoadingFile = false;
            },
          });
      });
  }

  trackByFieldId(index: number, field: any): number | string {
    return field.id ?? field.name;
  }

  /**
   * FE-011: the media element failed (typically a 401 on a Range request after the
   * token embedded in the URL expired). Refresh the access token first — the
   * media element's 401 never reaches the interceptor, so nothing else has
   * refreshed the *expired* token that the failed URL carried — then re-generate
   * the URL (it snapshots the *current* access token) and let the element retry.
   * The playback position is restored once the new stream reports its metadata.
   * If the freshly generated URL is identical to the old one *after* the refresh
   * (nothing to retry with) or the retry budget is spent, report the failure
   * instead of looping.
   */
  public onMediaError(event: Event): void {
    if (!this.currentDatabase || !this.entryForMetadata || !this.currentStreamUrl) {
      return;
    }
    const el = event.target as HTMLMediaElement | null;
    const dbId = this.currentDatabase.id;
    const entryId = this.entryForMetadata.id;

    this.authService
      .forceAccessTokenRefresh()
      .pipe(take(1), takeUntil(this.destroy$))
      .subscribe(() => {
        // The entry (or the whole modal state) may have changed while the refresh
        // was in flight — never overwrite another entry's media URL.
        if (
          !this.currentDatabase ||
          !this.entryForMetadata ||
          !this.currentStreamUrl ||
          this.entryForMetadata.id !== entryId
        ) {
          return;
        }

        const freshUrl = this.entryService.getEntryFileUrl(dbId, entryId);
        if (
          this.mediaUrlRetries >= EntryDetailModalComponent.MEDIA_URL_MAX_RETRIES ||
          freshUrl === this.currentStreamUrl
        ) {
          this.notificationService.showError(
            'Media playback failed — the file could not be loaded.',
          );
          return;
        }

        if (el && typeof el.currentTime === 'number' && el.currentTime > 0) {
          this.pendingResumeTime = el.currentTime;
        }
        this.mediaUrlRetries++;
        this.currentStreamUrl = freshUrl;
        this.fileUrl = this.sanitizer.bypassSecurityTrustUrl(freshUrl);
      });
  }

  /** FE-011: restore the playback position after a media-URL refresh. */
  public onMediaLoadedMetadata(event: Event): void {
    const el = event.target as HTMLMediaElement | null;
    if (el && this.pendingResumeTime !== null) {
      try {
        el.currentTime = this.pendingResumeTime;
      } catch {
        // Some engines refuse the seek — fall back to playing from the start.
      }
      this.pendingResumeTime = null;
    }
  }

  /**
   * FE-011: download via HttpClient + blob/object URL instead of linking the
   * tokenized media URL — the token must not land in browser history or download
   * metadata (it is sent as an Authorization header instead).
   */
  public onDownload(): void {
    if (!this.currentDatabase || !this.entryForMetadata || this.isDownloading) {
      return;
    }
    this.isDownloading = true;
    const filename = this.entryForMetadata.filename || 'download';
    this.entryService
      .getEntryFileBlob(this.currentDatabase.id, this.entryForMetadata.id)
      .pipe(
        finalize(() => {
          this.isDownloading = false;
        }),
        takeUntil(this.destroy$),
      )
      .subscribe((blob) => {
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
      });
  }

  hasCoordinate(val: any): boolean {
    return (
      val !== null &&
      val !== undefined &&
      typeof val === 'object' &&
      typeof val.latitude === 'number' &&
      typeof val.longitude === 'number' &&
      !isNaN(val.latitude) &&
      !isNaN(val.longitude)
    );
  }

  formatCoordinate(val: any): string {
    if (!this.hasCoordinate(val)) return 'N/A';
    return `${val.latitude.toFixed(6)}, ${val.longitude.toFixed(6)}`;
  }

  getOpenStreetMapUrl(val: any): string {
    if (!this.hasCoordinate(val)) return '#';
    return `https://www.openstreetmap.org/?mlat=${val.latitude}&mlon=${val.longitude}#map=16/${val.latitude}/${val.longitude}`;
  }

  private revokeFileObjectUrl(): void {
    if (this.currentObjectUrl) {
      URL.revokeObjectURL(this.currentObjectUrl);
      this.currentObjectUrl = null;
    }
  }

  closeModal(): void {
    // FE-023/FE-024: all cleanup happens in the modal 'close' event handler
    // (teardown), which also covers ESC / X / overlay dismissal.
    this.modalService.close(EntryDetailModalComponent.MODAL_ID);
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
    this.teardown();
  }
}
