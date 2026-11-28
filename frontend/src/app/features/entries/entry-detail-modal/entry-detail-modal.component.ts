import { CommonModule } from '@angular/common';
import {
  Component,
  OnDestroy,
  OnInit,
  ViewChildren,
  QueryList,
  ElementRef,
  DestroyRef,
  inject,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { Observable, of, combineLatest, BehaviorSubject, EMPTY } from 'rxjs';
import {
  switchMap,
  map,
  filter,
  tap,
  withLatestFrom,
  catchError,
  finalize,
  distinctUntilChanged,
} from 'rxjs/operators';
import { CustomField, Database, Entry, User } from '../../../core/models';
import { EntryStatus } from '../../../core/models/enums';
import { EntryService } from '../../../core/services/entry.service';
import { DatabaseService } from '../../../core/services/database.service';
import { ModalService } from '../../../core/services/modal.service';
import { DomSanitizer, SafeUrl } from '@angular/platform-browser';
import { AuthService } from '../../../core/services/auth.service';
import { NotificationService } from '../../../core/services/notification.service';
import { ConfirmationModalData } from '../../../shared/components/confirmation-modal/confirmation-modal.component';
import { EditEntryModalComponent } from '../edit-entry-modal/edit-entry-modal.component';
import { ModalComponent } from '../../../shared/components/modal/modal.component';
import { CoordinateLinkComponent } from '../../../shared/components/coordinate-link/coordinate-link.component';
import { SecureImageDirective } from '../../../shared/directives/secure-image.directive';
import { FormatBytesPipe } from '../../../shared/pipes/format-bytes.pipe';
import { isMimeTypeStreamable } from '../../../shared/utils/mime-types';
import { downloadBlob } from '../../../shared/utils/download';
import { MediaPlaybackController } from '../../../shared/utils/media-playback.controller';
import {
  canDeleteEntry,
  canEditEntry,
  isEntryBusy,
  isEntryError,
  isEntryProcessing,
  isEntryQueued,
} from '../../../shared/utils/entry-domain';
import { resolveDatabasePermissions } from '../../../shared/utils/permissions';

@Component({
  selector: 'app-entry-detail-modal',
  templateUrl: './entry-detail-modal.component.html',
  styleUrls: ['./entry-detail-modal.component.css'],
  standalone: true,
  imports: [
    CommonModule,
    ModalComponent,
    SecureImageDirective,
    FormatBytesPipe,
    CoordinateLinkComponent,
  ],
})
export class EntryDetailModalComponent implements OnInit, OnDestroy {
  public static readonly MODAL_ID = 'entryDetailModal';

  public readonly EntryStatus = EntryStatus;
  public readonly canEditEntry = canEditEntry;
  public readonly canDeleteEntry = canDeleteEntry;
  public readonly isEntryBusy = isEntryBusy;
  public readonly isEntryError = isEntryError;
  public readonly isEntryQueued = isEntryQueued;

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

  private pendingEntry: Entry | null = null;
  private sessionGeneration = 0;
  private readonly destroyRef = inject(DestroyRef);
  private isOpen$ = new BehaviorSubject<boolean>(false);

  // FE-011: `getEntryFileUrl` embeds a *snapshot* of the short-lived access token
  // in the query string (the browser's media engine cannot send an Authorization
  // header). A stream that outlives the token (~5 min) starts failing its Range
  // requests with 401 — see onMediaError(), which re-generates the URL with a
  // fresh token. Backend follow-up: signed per-resource URLs would remove the
  // token from the URL entirely.
  private currentStreamUrl: string | null = null;
  private mediaController: MediaPlaybackController;
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
    this.mediaController = new MediaPlaybackController(
      this.entryService,
      this.authService,
      this.notificationService,
    );
  }

  ngOnInit(): void {
    // Keep internal state updated
    combineLatest([this.databaseService.selectedDatabase$, this.currentUser$])
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(([db, user]) => {
        this.currentDatabase = db;
        this.updatePermissions(user, db);
      });

    this.isOpen$
      .pipe(
        distinctUntilChanged(),
        switchMap((isOpen) =>
          isOpen
            ? this.entryService.selectedEntry$.pipe(
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
                    (entry.database_id ?? this.currentDatabase?.id) ===
                      (this.entryForMetadata.database_id ?? this.currentDatabase?.id) &&
                    this.fileUrl &&
                    !this.loadError
                  ) {
                    this.pendingEntry = entry;
                    this.entryForMetadata = { ...this.entryForMetadata, ...entry };
                    return of(null);
                  }

                  this.mediaController.resetForNewEntry();

                  this.fileUrl = null;
                  this.previewUrl = null;
                  this.isLoadingFile = true;
                  this.loadError = false;
                  this.isDownloading = false;
                  this.currentStreamUrl = null;
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
                          const streamUrl = this.mediaController.createStreamUrl(
                            currentDb.id,
                            entry.id,
                          );
                          this.currentStreamUrl = streamUrl;
                          this.fileUrl = this.sanitizer.bypassSecurityTrustUrl(streamUrl);
                          this.isLoadingFile = false;

                          return of('streaming_ready');
                        } else if (mime.startsWith('image/')) {
                          // IMAGES: Fall back to Blob download
                          return this.mediaController.loadImageBlob(currentDb.id, entry.id).pipe(
                            map((objectUrl) => {
                              this.fileUrl = this.sanitizer.bypassSecurityTrustUrl(objectUrl);
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
            : EMPTY,
        ),
        takeUntilDestroyed(this.destroyRef),
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
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((event) => {
        if (event.action === 'open') {
          this.sessionGeneration++;
          this.isOpen$.next(true);
        } else if (event.action === 'close') {
          this.isOpen$.next(false);
          this.teardown();
        }
      });
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
    this.sessionGeneration++;
    this.mediaController.teardown(this.mediaElements?.map((ref) => ref.nativeElement));

    this.currentStreamUrl = null;
    this.fileUrl = null;
    this.previewUrl = null;
    this.isLoadingFile = true;
    this.loadError = false;
    this.isDownloading = false;
    this.entryForMetadata = null;
    this.pendingEntry = null;
    this.entryService.clearSelectedEntry();
  }

  private updatePermissions(user: User | null, db: Database | null): void {
    const perms = resolveDatabasePermissions(user, db?.id);
    this.canEdit = perms.canEdit;
    this.canDelete = perms.canDelete;
  }

  // UPDATED: Renamed parameter to dbId
  public getPreviewUrl(dbId: string, entryId: number): string {
    return this.entryService.getEntryPreviewUrl(dbId, entryId);
  }

  onEdit(): void {
    if (!this.entryForMetadata) return;

    if (!canEditEntry(this.entryForMetadata)) {
      this.notificationService.showError(
        isEntryProcessing(this.entryForMetadata)
          ? 'Cannot edit an entry that is still processing.'
          : `Cannot edit an entry that is ${this.entryForMetadata.status}.`,
      );
      return;
    }

    this.modalService.open(EditEntryModalComponent.MODAL_ID);
  }

  onDelete(): void {
    if (!this.currentDatabase || !this.entryForMetadata) return;

    if (!canDeleteEntry(this.entryForMetadata)) {
      this.notificationService.showError(
        `Cannot delete an entry that is still ${this.entryForMetadata.status}.`,
      );
      return;
    }

    const dbId = this.currentDatabase.id;
    const entryId = this.entryForMetadata.id;
    const gen = this.sessionGeneration;

    const modalData: ConfirmationModalData = {
      title: 'Delete Entry',
      message: `Are you sure you want to delete entry ${entryId}? This action cannot be undone.`,
    };

    this.modalService
      .confirm(modalData)
      .pipe(filter(Boolean), takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          if (gen !== this.sessionGeneration) return;
          this.isLoadingFile = true;

          this.entryService
            .deleteEntry(dbId, entryId)
            .pipe(takeUntilDestroyed(this.destroyRef))
            .subscribe({
              next: () => {
                if (gen === this.sessionGeneration) {
                  this.closeModal();
                }
              },
              error: () => {
                if (gen === this.sessionGeneration) {
                  this.isLoadingFile = false;
                }
              },
            });
        },
        error: () => {},
      });
  }

  trackByFieldId(_index: number, field: CustomField): number | string {
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
    const failedUrl = this.currentStreamUrl;

    this.mediaController.handleMediaError({
      dbId,
      entryId,
      currentUrl: failedUrl,
      mediaElement: el,
      isStillActive: () =>
        !!this.currentDatabase &&
        this.currentDatabase.id === dbId &&
        !!this.entryForMetadata &&
        this.entryForMetadata.id === entryId &&
        this.currentStreamUrl === failedUrl,
      onRetryUrl: (freshUrl) => {
        this.currentStreamUrl = freshUrl;
        this.fileUrl = this.sanitizer.bypassSecurityTrustUrl(freshUrl);
      },
    });
  }

  /** FE-011: restore the playback position after a media-URL refresh. */
  public onMediaLoadedMetadata(event: Event): void {
    this.mediaController.restorePlaybackPosition(event.target as HTMLMediaElement | null);
  }

  /**
   * FE-011: download via HttpClient + blob/object URL instead of linking the
   * tokenized media URL — the token must not land in browser history or download
   * metadata (it is sent as an Authorization header instead).
   */
  public get mediaKind(): 'image' | 'video' | 'audio' | 'fallback' {
    if (!this.fileUrl || !this.entryForMetadata) {
      return 'fallback';
    }
    const mime = this.entryForMetadata.mime_type || '';
    if (mime.startsWith('image/')) {
      return 'image';
    }
    if (mime.startsWith('video/')) {
      return 'video';
    }
    if (mime.startsWith('audio/') || mime === 'application/ogg') {
      return 'audio';
    }
    return 'fallback';
  }

  public onDownload(): void {
    if (!this.currentDatabase || !this.entryForMetadata || this.isDownloading) {
      return;
    }
    const gen = this.sessionGeneration;
    this.isDownloading = true;
    const filename = this.entryForMetadata.filename || 'download';
    this.entryService
      .getEntryFileBlob(this.currentDatabase.id, this.entryForMetadata.id)
      .pipe(
        finalize(() => {
          if (gen === this.sessionGeneration) {
            this.isDownloading = false;
          }
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: (blob) => {
          downloadBlob(blob, filename);
        },
        error: () => {},
      });
  }

  closeModal(): void {
    // FE-023/FE-024: all cleanup happens in the modal 'close' event handler
    // (teardown), which also covers ESC / X / overlay dismissal.
    this.modalService.close(EntryDetailModalComponent.MODAL_ID);
  }

  ngOnDestroy(): void {
    this.isOpen$.complete();
    this.teardown();
    this.mediaController.destroy(this.mediaElements?.map((ref) => ref.nativeElement));
  }
}
