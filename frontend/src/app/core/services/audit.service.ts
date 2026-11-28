import { Injectable } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { Observable } from 'rxjs';
import { NotificationService } from './notification.service';
import { AuditLog } from '../models/audit.models';
import { catchApiError } from '../../shared/utils/http-error';

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
    return this.http.get<AuditLog[]>(`${this.apiUrl}/audit`, { params }).pipe(
      catchApiError(this.notificationService, {
        prefix: 'Audit Log Error: ',
        notFoundResource: 'Audit logs',
      }),
    );
  }
}
