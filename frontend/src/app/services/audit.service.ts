import { Injectable } from '@angular/core';
import { HttpClient, HttpParams, HttpErrorResponse } from '@angular/common/http';
import { Observable, throwError } from 'rxjs';
import { catchError } from 'rxjs/operators';
import { NotificationService } from './notification.service';
import { AuditLog } from '../models/audit.models';

@Injectable({
  providedIn: 'root',
})
export class AuditService {
  private readonly apiUrl = '/api';

  constructor(
    private http: HttpClient,
    private notificationService: NotificationService,
  ) {}

  /**
   * Centralized error handler mirroring your existing pattern.
   */
  private handleError(error: HttpErrorResponse): Observable<never> {
    let errorMessage = 'An unknown error occurred.';
    if (error.error && typeof error.error.error === 'string') {
      errorMessage = error.error.error;
    } else if (error.status === 0) {
      errorMessage = 'Network error: Server is unreachable.';
    } else if (error.status === 401) {
      errorMessage = 'Unauthorized: Please log in again.';
    } else if (error.status === 403) {
      errorMessage = 'Forbidden: You lack permission for this operation.';
    } else if (error.status === 404) {
      errorMessage = 'Not Found: The requested audit log resource does not exist.';
    } else if (error.status >= 500) {
      errorMessage = `Server Error (${error.status}): ${error.statusText || 'Internal Error'}`;
    } else if (error.statusText) {
      errorMessage = `Error ${error.status}: ${error.statusText}`;
    }

    // Display the error using your global notification service
    this.notificationService.showError(`Audit Log Error: ${errorMessage}`);
    return throwError(() => new Error(errorMessage));
  }

  /**
   * Retrieves a paginated and filtered list of system audit logs.
   * * @param limit The maximum number of logs to return (default: 50).
   * @param offset The pagination offset (default: 0).
   * @param order Sorting direction based on timestamp ('asc' or 'desc').
   * @param tstart Optional Unix epoch timestamp (ms) to filter logs after this time.
   * @param tend Optional Unix epoch timestamp (ms) to filter logs before this time.
   * @returns An observable containing the array of AuditLogs.
   */
  public getAuditLogs(
    limit: number = 50,
    offset: number = 0,
    order: 'asc' | 'desc' = 'desc',
    tstart?: number,
    tend?: number,
  ): Observable<AuditLog[]> {
    const safeLimit = Number.isFinite(limit) ? Math.min(Math.max(Math.trunc(limit), 1), 1000) : 50;

    // Build the HTTP Query Parameters dynamically
    let params = new HttpParams()
      .set('limit', safeLimit.toString())
      .set('offset', offset.toString())
      .set('order', order);

    if (typeof tstart === 'number' && Number.isFinite(tstart)) {
      params = params.set('tstart', tstart.toString());
    }

    if (typeof tend === 'number' && Number.isFinite(tend)) {
      params = params.set('tend', tend.toString());
    }

    // Execute the GET request to /api/audit
    return this.http
      .get<AuditLog[]>(`${this.apiUrl}/audit`, { params })
      .pipe(catchError((error: HttpErrorResponse) => this.handleError(error)));
  }
}
