// frontend/src/app/services/entry.service.ts

import { Injectable } from '@angular/core';
import {
  HttpClient,
  HttpErrorResponse,
  HttpHeaders,
  HttpEvent,
  HttpEventType,
  HttpResponse,
} from '@angular/common/http';
import {
  BehaviorSubject,
  EMPTY,
  Observable,
  Subject,
  Subscription,
  of,
  throwError,
  timer,
} from 'rxjs';
import { catchError, tap, map, switchMap, filter, take, finalize, expand } from 'rxjs/operators';
import {
  Entry,
  SearchRequest,
  PartialEntryResponse,
  BulkDeleteResponse,
  UploadEntryMetadata,
  ImportConfig,
} from '../models';
import { NotificationService } from './notification.service';
import { AuthService } from './auth.service';

@Injectable({
  providedIn: 'root',
})
export class EntryService {
  private readonly apiUrl = '/api';

  // State specific to entries
  private selectedEntrySubject = new BehaviorSubject<Entry | null>(null);
  private refreshNotifier = new Subject<void>();
  private entryCreatedNotifier = new Subject<Entry>();
  private entryUpdatedNotifier = new Subject<Entry>();
  private processingEntriesSubject = new BehaviorSubject<number[]>([]);

  // FE-013: subscriptions of the service-owned status polls, keyed by entry id.
  private activePolls = new Map<number, Subscription>();

  public selectedEntry$ = this.selectedEntrySubject.asObservable();
  public refreshRequired$ = this.refreshNotifier.asObservable();
  public entryCreated$ = this.entryCreatedNotifier.asObservable();
  public entryUpdated$ = this.entryUpdatedNotifier.asObservable();
  public processingEntries$ = this.processingEntriesSubject.asObservable();

  constructor(
    private http: HttpClient,
    private notificationService: NotificationService,
    private authService: AuthService,
  ) {
    // FE-013: service-owned polls must not outlive the session. A poll surviving
    // logout keeps firing unauthenticated requests whose 401 → refreshToken() →
    // "No refresh token available" → logout(false) can bounce a freshly logged-in
    // user straight back to /login.
    this.authService.currentUser$
      .pipe(filter((user) => user === null))
      .subscribe(() => this.stopAllPolls());
  }

  public triggerImageListRefresh(): void {
    this.refreshNotifier.next();
  }

  /**
   * Centralized error handler for HTTP requests.
   * FE-022: `options.silent` suppresses the toast for callers that report the
   * failure themselves (e.g. the batch-upload loop collecting per-file errors) —
   * it must never swallow the error itself.
   */
  private handleError(error: HttpErrorResponse, options?: { silent?: boolean }): Observable<never> {
    let errorMessage: string;
    let isAuthError = false;

    if (error.error && typeof error.error.error === 'string') {
      errorMessage = error.error.error;
    } else if (error.status === 0) {
      errorMessage = 'Network error or backend unreachable. Check CORS or server status.';
    } else if (error.status === 401) {
      errorMessage = 'Authentication failed or session expired. Please log in again.';
      isAuthError = true;
    } else if (error.status === 403) {
      errorMessage = 'Forbidden: You lack permission for this action.';
    } else if (error.status === 400) {
      errorMessage = `Bad Request: ${error.error?.error || 'Invalid input.'}`;
    } else if (error.status >= 500) {
      errorMessage = `Server Error (${error.status}): ${error.statusText}. Please try again later.`;
    } else if (error.statusText) {
      errorMessage = `Error ${error.status}: ${error.statusText}`;
    } else {
      errorMessage = 'An unknown error occurred.';
    }

    if (!options?.silent) {
      if (isAuthError) {
        this.notificationService.showGlobalError(errorMessage);
      } else {
        this.notificationService.showError(errorMessage);
      }
    }

    return throwError(() => new Error(errorMessage));
  }

  // --- ENTRIES (BULK) ENDPOINTS ---

  public searchEntries(
    dbId: string,
    payload: SearchRequest,
    options?: { silent?: boolean },
  ): Observable<Entry[]> {
    return this.http
      .post<Entry[]>(`${this.apiUrl}/database/${dbId}/entries/search`, payload)
      .pipe(catchError((err) => (options?.silent ? throwError(() => err) : this.handleError(err))));
  }

  public bulkDeleteEntries(dbId: string, ids: number[]): Observable<BulkDeleteResponse> {
    return this.http
      .post<BulkDeleteResponse>(`${this.apiUrl}/database/${dbId}/entries/delete`, { ids })
      .pipe(
        // FE-017: report the *real* result instead of claiming `ids.length` successes.
        // A partial failure answers 200 with deleted_count < requested and a composed
        // `errors` string (entryhandler/models.go BulkDeleteResponse).
        tap((res) => {
          const deleted = res?.deleted_count ?? 0;
          if (res?.errors) {
            const failed = Math.max(ids.length - deleted, 0);
            this.notificationService.showError(
              `Deleted ${deleted} of ${ids.length} entries` +
                (failed > 0 ? `, ${failed} failed` : '') +
                `: ${res.errors}`,
            );
          } else {
            this.notificationService.showSuccess(`Successfully deleted ${deleted} entries.`);
          }
          this.triggerImageListRefresh();
        }),
        catchError((err) => this.handleError(err)),
      );
  }

  public bulkExportEntries(dbId: string, ids: number[]): Observable<Blob> {
    return this.http
      .post(
        `${this.apiUrl}/database/${dbId}/entries/export`,
        { ids },
        {
          responseType: 'blob',
        },
      )
      .pipe(catchError((err) => this.handleError(err)));
  }

  /**
   * Uploads a ZIP archive for bulk importing entries with progress tracking.
   * FE-042: `config` typed as ImportConfig (was `any`).
   */
  public importEntries(
    dbId: string,
    file: File,
    config: ImportConfig,
  ): Observable<HttpEvent<unknown>> {
    const formData = new FormData();
    formData.append('config', JSON.stringify(config));
    // FE-051: the multipart part must carry a ZIP Content-Type — the browser uses
    // File.type, which some platforms leave empty or set to application/octet-stream
    // for .zip files, and the backend rejects anything but application/zip /
    // application/x-zip-compressed (entryhandler/entries.go:893) with 415.
    formData.append('file', new File([file], file.name, { type: 'application/zip' }), file.name);

    return this.http.post(`${this.apiUrl}/database/${dbId}/entries/import`, formData, {
      reportProgress: true,
      observe: 'events',
    });
  }

  // --- ENTRY (SINGLE) ENDPOINTS ---

  public selectEntry(entry: Entry): void {
    this.selectedEntrySubject.next(entry);
  }

  public clearSelectedEntry(): void {
    this.selectedEntrySubject.next(null);
  }

  /**
   * Uploads a file with associated metadata.
   * Sends both payload and binary using multipart/form-data.
   * FE-022: `options.silentError` suppresses the error toast so the batch-upload
   * loop can aggregate per-file failures into a single summary notification.
   */
  public uploadEntry(
    dbId: string,
    // FE-042: explicit upload-metadata shape instead of
    // `Omit<Entry, 'id' | 'width' | 'height' | ...>` + `as any` casts.
    metadata: UploadEntryMetadata,
    file: File,
    // FE-032: `onProgress` receives per-file byte progress (0-100).
    options?: {
      silentSuccess?: boolean;
      silentError?: boolean;
      onProgress?: (percent: number) => void;
    },
  ): Observable<void> {
    const formData = new FormData();

    formData.append('metadata', JSON.stringify(metadata));
    formData.append('file', file, file.name);

    return this.http
      .post<Entry | PartialEntryResponse>(`${this.apiUrl}/database/${dbId}/entry`, formData, {
        // FE-032: per-byte progress (mirrors `importEntries`) instead of only the
        // final response — a big file's progress bar used to jump 0 → 100.
        observe: 'events',
        reportProgress: true,
      })
      .pipe(
        tap((event) => {
          // FE-032: surface byte-level upload progress to the caller.
          if (event.type === HttpEventType.UploadProgress && event.total && options?.onProgress) {
            options.onProgress(Math.round((100 * event.loaded) / event.total));
          }

          if (event.type === HttpEventType.Response) {
            const response = event as HttpResponse<Entry | PartialEntryResponse>;
            if (response.status === 201) {
              const entry = response.body as Entry;
              if (!options?.silentSuccess) {
                this.notificationService.showSuccess('Entry uploaded successfully.');
              }

              if (entry) {
                if (entry.status === 'processing' || entry.status === 'queued') {
                  this.addProcessingEntry(entry.id);
                  this.startPollingEntryStatus(dbId, entry.id);
                }
                this.entryCreatedNotifier.next(entry);
              }
            }

            if (response.status === 202) {
              const partialEntry = response.body as PartialEntryResponse;
              // The backend decides whether the entry is processed right away ("processing")
              // or waiting for a free conversion slot ("queued"). Reflect the real status.
              const status = partialEntry.status === 'queued' ? 'queued' : 'processing';
              const entry: Entry = {
                id: partialEntry.id,
                timestamp: metadata.timestamp || Date.now(),
                created_at: partialEntry.created_at || Date.now(),
                updated_at: partialEntry.updated_at || Date.now(),
                database_id: dbId,
                filename: file.name,
                filesize: file.size,
                mime_type: file.type,
                status,
              };
              this.addProcessingEntry(partialEntry.id);
              if (!options?.silentSuccess) {
                if (status === 'queued') {
                  this.notificationService.showInfo(
                    `Large file (ID: ${partialEntry.id}) is queued for processing...`,
                  );
                } else {
                  this.notificationService.showInfo(
                    `Large file (ID: ${partialEntry.id}) is processing...`,
                  );
                }
              }
              this.startPollingEntryStatus(dbId, partialEntry.id);
              this.entryCreatedNotifier.next(entry);
            }
          }
        }),
        // FE-032: complete only on the final response (progress events are skipped).
        filter((event) => event.type === HttpEventType.Response),
        map(() => void 0),
        catchError((err) => this.handleError(err, { silent: options?.silentError })),
      );
  }

  public getEntryMeta(
    dbId: string,
    entryId: number,
    options?: { silent?: boolean },
  ): Observable<Entry> {
    return this.http
      .get<Entry>(`${this.apiUrl}/database/${dbId}/entry/${entryId}`)
      .pipe(catchError((err) => (options?.silent ? throwError(() => err) : this.handleError(err))));
  }

  public getEntryFileBlob(dbId: string, entryId: number): Observable<Blob> {
    return this.http
      .get(`${this.apiUrl}/database/${dbId}/entry/${entryId}/file`, {
        responseType: 'blob',
        headers: new HttpHeaders({ Accept: '*/*' }),
      })
      .pipe(catchError((err) => this.handleError(err)));
  }

  /**
   * Generates a direct URL to the file endpoint, bypassing Angular's HttpClient.
   * This is crucial for <video> and <audio> tags so the browser can utilize
   * HTTP Range requests (streaming) instead of downloading the entire file into memory.
   */
  public getEntryFileUrl(dbId: string, entryId: number): string {
    const baseUrl = `${this.apiUrl}/database/${dbId}/entry/${entryId}/file`;
    const token = this.authService.getAccessToken();

    // Append the token as a query parameter so the browser's native media engine can authenticate
    if (token) {
      return `${baseUrl}?token=${encodeURIComponent(token)}`;
    }

    return baseUrl;
  }

  public getEntryPreviewUrl(dbId: string, entryId: number): string {
    return `${this.apiUrl}/database/${dbId}/entry/${entryId}/preview`;
  }

  public updateEntry(dbId: string, entryId: number, updates: Partial<Entry>): Observable<Entry> {
    return this.http.patch<Entry>(`${this.apiUrl}/database/${dbId}/entry/${entryId}`, updates).pipe(
      tap((updated) => {
        this.notificationService.showSuccess(`Entry ${entryId} updated successfully.`);
        this.triggerImageListRefresh();

        // Propagate the PATCH response ("the full, updated entry metadata object",
        // internal/httpserver/entryhandler/entries.go PatchEntry) so open views show
        // the new values: entry-list merges it via entryUpdated$, and the detail/edit
        // modals read the selected-entry state. Without this the entry modal kept
        // rendering the pre-edit metadata despite the success toast.
        if (updated) {
          this.entryUpdatedNotifier.next(updated);
          const selected = this.selectedEntrySubject.value;
          const updatedDb = updated.database_id ?? dbId;
          if (
            selected &&
            selected.id === updated.id &&
            (selected.database_id ?? dbId) === updatedDb
          ) {
            this.selectedEntrySubject.next(updated);
          }
        }
      }),
      catchError((err) => this.handleError(err)),
    );
  }

  public deleteEntry(dbId: string, entryId: number): Observable<{ message: string }> {
    return this.http
      .delete<{ message: string }>(`${this.apiUrl}/database/${dbId}/entry/${entryId}`)
      .pipe(
        tap((res) => {
          this.notificationService.showSuccess(res.message || `Entry ${entryId} deleted.`);
          this.triggerImageListRefresh();
        }),
        catchError((err) => this.handleError(err)),
      );
  }

  // --- HELPER METHODS FOR ASYNC UPLOAD ---

  private addProcessingEntry(id: number): void {
    const current = this.processingEntriesSubject.value;
    if (!current.includes(id)) {
      this.processingEntriesSubject.next([...current, id]);
    }
  }

  private removeProcessingEntry(id: number): void {
    const current = this.processingEntriesSubject.value;
    this.processingEntriesSubject.next(current.filter((entryId) => entryId !== id));
  }

  // FE-013: polling cadence — start fast, back off while nothing changes. There is
  // deliberately no duration cap: a conversion may take arbitrarily long and the
  // contract (AI/Concept/02_04_HTTP_Entry.md) is to poll until `status` is `ready`.
  // A poll terminates on a terminal status, on 404/410 (entry or database gone) or
  // after POLL_MAX_CONSECUTIVE_FAILURES consecutive transport failures.
  private static readonly POLL_INITIAL_DELAY_MS = 2000;
  private static readonly POLL_MAX_DELAY_MS = 30000;
  private static readonly POLL_MAX_CONSECUTIVE_FAILURES = 10;

  /**
   * FE-013: polls the entry's status until it settles (`ready`/`error`), the entry
   * turns out to be gone (404/410) or too many consecutive transport failures pile
   * up. The returned observable completes in every one of those cases; the id is
   * removed from `processingEntries$` when it does (see the `finalize` below).
   */
  private pollForEntryStatus(dbId: string, entryId: number): Observable<void> {
    // Status seen on the previous poll. Used to refresh the list view when the
    // entry moves from "queued" to "processing" (so the UI swaps the QUEUED
    // placeholder for the spinner without a manual reload).
    let lastSeenStatus: string | null = null;
    let delayMs = EntryService.POLL_INITIAL_DELAY_MS;
    let consecutiveFailures = 0;
    let terminate = false;

    const pollOnce = (): Observable<Entry | null> =>
      this.getEntryMeta(dbId, entryId, { silent: true }).pipe(
        // FE-013: a transient poll failure must neither kill the chain nor pop a
        // spurious global error toast — just skip this round. But a lookup that
        // can never succeed again (404/410: entry or database gone) or too many
        // consecutive failures (≈ 5 min of silence at the 30 s backoff cap) must
        // end the poll instead of leaking one request every 30 s forever.
        catchError((err: HttpErrorResponse) => {
          if (err?.status === 404 || err?.status === 410) {
            terminate = true;
            this.handleEntryGone(dbId, entryId);
          } else if (++consecutiveFailures >= EntryService.POLL_MAX_CONSECUTIVE_FAILURES) {
            terminate = true;
          }
          return of(null);
        }),
        tap((entry) => {
          const status = (entry?.status as string) ?? null;
          if (entry) {
            consecutiveFailures = 0;
          }
          // Back off while nothing changes (slow conversion, repeated failures) and
          // speed back up as soon as the entry makes progress.
          delayMs =
            entry && status !== lastSeenStatus
              ? EntryService.POLL_INITIAL_DELAY_MS
              : Math.min(delayMs * 2, EntryService.POLL_MAX_DELAY_MS);
        }),
      );

    return timer(EntryService.POLL_INITIAL_DELAY_MS).pipe(
      switchMap(() => pollOnce()),
      // FE-013: exponential-ish backoff instead of the old `take(30)` 60 s hard cap
      // that silently dropped large conversions from the processing list. A poll
      // that hit a termination condition schedules no further round.
      expand(() => (terminate ? EMPTY : timer(delayMs).pipe(switchMap(() => pollOnce())))),
      tap((entry) => {
        if (!entry) {
          return;
        }
        const status = entry.status as string;
        if (status !== lastSeenStatus && status !== 'ready' && status !== 'error') {
          this.entryUpdatedNotifier.next(entry);
        }
        lastSeenStatus = status;
      }),
      // Keep polling while the entry is still waiting ("queued") or actively
      // being worked on ("processing"); only stop on a terminal status.
      filter(
        (entry): entry is Entry =>
          !!entry && entry.status !== 'processing' && entry.status !== 'queued',
      ),
      take(1),
      tap((entry) => {
        if (entry.status === 'ready') {
          this.entryUpdatedNotifier.next(entry);
        } else if (entry.status === 'error') {
          this.notificationService.showError(`Entry ${entry.id} failed to process.`);
          this.entryUpdatedNotifier.next(entry);
        }
      }),
      map(() => void 0),
      finalize(() => {
        if (this.processingEntriesSubject.value.includes(entryId)) {
          this.removeProcessingEntry(entryId);
        }
      }),
    );
  }

  /**
   * FE-013: the entry (or its whole database) vanished mid-processing. Views drop
   * the card via `entryUpdated$` (the terminal `error` placeholder) and the silent
   * list refresh removes the row on the next fetch.
   */
  private handleEntryGone(dbId: string, entryId: number): void {
    this.entryUpdatedNotifier.next({ id: entryId, database_id: dbId, status: 'error' } as Entry);
    this.triggerImageListRefresh();
  }

  /** FE-013: subscribes a status poll and keeps track of it for session teardown. */
  private startPollingEntryStatus(dbId: string, entryId: number): void {
    this.stopPollingEntry(entryId);
    const subscription = this.pollForEntryStatus(dbId, entryId)
      .pipe(finalize(() => this.activePolls.delete(entryId)))
      .subscribe();
    this.activePolls.set(entryId, subscription);
  }

  private stopPollingEntry(entryId: number): void {
    const subscription = this.activePolls.get(entryId);
    if (subscription) {
      subscription.unsubscribe();
      this.activePolls.delete(entryId);
    }
  }

  /** FE-013: stops every service-owned poll (called when the session ends). */
  private stopAllPolls(): void {
    this.activePolls.forEach((subscription) => subscription.unsubscribe());
    this.activePolls.clear();
  }
}
