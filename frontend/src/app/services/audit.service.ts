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
    if (error.error && error.error.error) {
      errorMessage = error.error.error;
    } else if (error.message) {
      errorMessage = error.message;
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
    // Build the HTTP Query Parameters dynamically
    let params = new HttpParams()
      .set('limit', limit.toString())
      .set('offset', offset.toString())
      .set('order', order);

    if (tstart !== undefined && tstart !== null) {
      params = params.set('tstart', tstart.toString());
    }

    if (tend !== undefined && tend !== null) {
      params = params.set('tend', tend.toString());
    }

    // Execute the GET request to /api/audit
    return this.http
      .get<AuditLog[]>(`${this.apiUrl}/audit`, { params })
      .pipe(catchError((error: HttpErrorResponse) => this.handleError(error)));
  }
}
