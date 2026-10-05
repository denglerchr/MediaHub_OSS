// frontend/src/app/services/entry.service.ts

import { Injectable } from '@angular/core';
import { HttpClient, HttpErrorResponse, HttpHeaders } from '@angular/common/http';
import { BehaviorSubject, Observable, Subject, throwError, timer } from 'rxjs';
import { catchError, tap, map, switchMap, filter, take, finalize } from 'rxjs/operators';
import { Entry, SearchRequest, PartialEntryResponse } from '../models';
import { NotificationService } from './notification.service';
import { AuthService } from './auth.service';
import { HttpEvent, HttpRequest } from '@angular/common/http';

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

  public selectedEntry$ = this.selectedEntrySubject.asObservable();
  public refreshRequired$ = this.refreshNotifier.asObservable();
  public entryCreated$ = this.entryCreatedNotifier.asObservable();
  public entryUpdated$ = this.entryUpdatedNotifier.asObservable();
  public processingEntries$ = this.processingEntriesSubject.asObservable();

  constructor(
    private http: HttpClient,
    private notificationService: NotificationService,
    private authService: AuthService
  ) {}

  public triggerImageListRefresh(): void {
    this.refreshNotifier.next();
  }

  /**
   * Centralized error handler for HTTP requests.
   */
  private handleError(error: HttpErrorResponse): Observable<never> {
    console.error("[DEBUG] EntryService: Full HTTP Error Response:", error);

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

    if (isAuthError) {
      this.notificationService.showGlobalError(errorMessage);
    } else {
      this.notificationService.showError(errorMessage);
    }

    return throwError(() => new Error(errorMessage));
  }

  // --- ENTRIES (BULK) ENDPOINTS ---

  public searchEntries(dbId: string, payload: SearchRequest): Observable<Entry[]> {
    return this.http
      .post<Entry[]>(`${this.apiUrl}/database/${dbId}/entries/search`, payload)
      .pipe(catchError((err) => this.handleError(err)));
  }

  public bulkDeleteEntries(dbId: string, ids: number[]): Observable<any> {
    return this.http.post(`${this.apiUrl}/database/${dbId}/entries/delete`, { ids }).pipe(
      tap(() => {
        this.notificationService.showSuccess(`Successfully deleted ${ids.length} entries.`);
        this.triggerImageListRefresh();
      }),
      catchError((err) => this.handleError(err))
    );
  }

  public bulkExportEntries(dbId: string, ids: number[]): Observable<Blob> {
    return this.http.post(`${this.apiUrl}/database/${dbId}/entries/export`, { ids }, { 
      responseType: 'blob' 
    }).pipe(
      catchError((err) => this.handleError(err))
    );
  }

  /**
   * Uploads a ZIP archive for bulk importing entries with progress tracking.
   */
  public importEntries(dbId: string, file: File, config: any): Observable<HttpEvent<any>> {
    const formData = new FormData();
    formData.append('config', JSON.stringify(config));
    formData.append('file', file, file.name);

    return this.http.post(`${this.apiUrl}/database/${dbId}/entries/import`, formData, {
      reportProgress: true,
      observe: 'events'
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
   */
  public uploadEntry(
    dbId: string, 
    metadata: Omit<Entry, 'id' | 'width' | 'height' | 'filesize' | 'mime_type' | 'status'>, 
    file: File,
    options?: { silentSuccess?: boolean }
  ): Observable<void> {
    const formData = new FormData();
    
    formData.append('metadata', JSON.stringify(metadata)); 
    formData.append('file', file, file.name);

    return this.http.post<Entry | PartialEntryResponse>(`${this.apiUrl}/database/${dbId}/entry`, formData, { 
      observe: 'response'
    }).pipe(
      tap(response => {
        if (response.status === 201) {
          const entry = response.body as Entry;
          if (!options?.silentSuccess) {
            this.notificationService.showSuccess('Entry uploaded successfully.');
          }
          
          if (entry) {
             if (entry.status === 'processing' || entry.status === 'queued') {
                this.addProcessingEntry(entry.id);
                this.pollForEntryStatus(dbId, entry.id);
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
            timestamp: (metadata as any).timestamp || Date.now(),
            created_at: partialEntry.created_at || Date.now(),
            updated_at: partialEntry.updated_at || Date.now(),
            database_id: dbId,
            filename: file.name,
            filesize: file.size,
            mime_type: file.type,
            status
          };
          this.addProcessingEntry(partialEntry.id);
          if (!options?.silentSuccess) {
            if (status === 'queued') {
              this.notificationService.showInfo(`Large file (ID: ${partialEntry.id}) is queued for processing...`);
            } else {
              this.notificationService.showInfo(`Large file (ID: ${partialEntry.id}) is processing...`);
            }
          }
          this.pollForEntryStatus(dbId, partialEntry.id);
          this.entryCreatedNotifier.next(entry);
        }
      }),
      map(() => void 0),
      catchError((err) => this.handleError(err))
    );
  }

  public getEntryMeta(dbId: string, entryId: number): Observable<Entry> {
    return this.http
      .get<Entry>(`${this.apiUrl}/database/${dbId}/entry/${entryId}`)
      .pipe(catchError((err) => this.handleError(err)));
  }

  public getEntryFileBlob(dbId: string, entryId: number): Observable<Blob> {
    return this.http.get(`${this.apiUrl}/database/${dbId}/entry/${entryId}/file`, {
        responseType: 'blob',
        headers: new HttpHeaders({ 'Accept': '*/*' })
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
      tap(() => {
        this.notificationService.showSuccess(`Entry ${entryId} updated successfully.`);
        this.triggerImageListRefresh();
      }),
      catchError(err => this.handleError(err))
    );
  }

  public deleteEntry(dbId: string, entryId: number): Observable<{ message: string }> {
    return this.http.delete<{ message: string }>(`${this.apiUrl}/database/${dbId}/entry/${entryId}`).pipe(
      tap(res => {
        this.notificationService.showSuccess(res.message || `Entry ${entryId} deleted.`);
        this.triggerImageListRefresh();
      }),
      catchError(err => this.handleError(err))
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
    this.processingEntriesSubject.next(current.filter(entryId => entryId !== id));
  }

  private pollForEntryStatus(dbId: string, entryId: number): void {
    // Status seen on the previous poll. Used to refresh the list view when the
    // entry moves from "queued" to "processing" (so the UI swaps the QUEUED
    // placeholder for the spinner without a manual reload).
    let lastSeenStatus: string | null = null;

    timer(2000, 2000).pipe(
      take(30),
      switchMap(() => this.getEntryMeta(dbId, entryId)),
      tap(entry => {
        const status = entry.status as string;
        if (status !== lastSeenStatus && status !== 'ready' && status !== 'error') {
          this.entryUpdatedNotifier.next(entry);
        }
        lastSeenStatus = status;
      }),
      // Keep polling while the entry is still waiting ("queued") or actively
      // being worked on ("processing"); only stop on a terminal status.
      filter(entry => entry.status !== 'processing' && entry.status !== 'queued'),
      take(1),
      finalize(() => {
        if (this.processingEntriesSubject.value.includes(entryId)) {
           this.removeProcessingEntry(entryId);
        }
      })
    ).subscribe({
      next: entry => {
        this.removeProcessingEntry(entry.id);
        if (entry.status === 'ready') {
          this.entryUpdatedNotifier.next(entry);
        } else if (entry.status === 'error') {
          this.notificationService.showError(`Entry ${entry.id} failed to process.`);
          this.entryUpdatedNotifier.next(entry);
        }
      },
      error: err => {
        this.removeProcessingEntry(entryId);
        console.error(`Error polling for entry ${entryId}:`, err);
      }
    });
  }
}