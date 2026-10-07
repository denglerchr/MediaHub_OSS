// frontend/src/app/services/database.service.ts

import { Injectable } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { BehaviorSubject, Observable, throwError, of } from 'rxjs';
import { catchError, map, switchMap, tap } from 'rxjs/operators';
import { Database, Housekeeping, HousekeepingReport, DatabaseConfig, CustomField } from '../models';
import { NotificationService } from './notification.service';
import { Router } from '@angular/router';

// FE-042: typed instead of `housekeeping?: any`.
export interface DatabaseUpdatePayload {
  name?: string;
  n_max_queued?: number;
  config?: DatabaseConfig;
  housekeeping?: Housekeeping;
}

@Injectable({
  providedIn: 'root',
})
export class DatabaseService {
  private readonly apiUrl = '/api';

  // State specific to databases
  private databasesSubject = new BehaviorSubject<Database[]>([]);
  private selectedDatabaseSubject = new BehaviorSubject<Database | null>(null);

  public databases$ = this.databasesSubject.asObservable();
  public selectedDatabase$ = this.selectedDatabaseSubject.asObservable();

  constructor(
    private http: HttpClient,
    private notificationService: NotificationService,
    private router: Router,
  ) {}

  /**
   * Centralized error handler for HTTP requests.
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
      errorMessage = 'Not Found: The requested database resource does not exist.';
    } else if (error.status >= 500) {
      errorMessage = `Server Error (${error.status}): ${error.statusText || 'Internal Error'}`;
    }

    this.notificationService.showError(errorMessage);
    return throwError(() => new Error(errorMessage));
  }

  // --- DATABASE ENDPOINTS ---

  /**
   * Fetches the list of databases accessible to the current user.
   */
  public loadDatabases(): Observable<Database[]> {
    return this.http.get<Database[]>(`${this.apiUrl}/databases`).pipe(
      tap((databases) => {
        this.databasesSubject.next(databases);
      }),
      catchError((err) => this.handleError(err)),
    );
  }

  /**
   * Selects a database by its ULID and updates the active database state.
   */
  public selectDatabase(id: string): Observable<Database | null> {
    const currentList = this.databasesSubject.value;
    const localDb = currentList.find((db) => db.id === id);
    // FE-036: remember the selection so a failed lookup can roll the optimistic
    // pre-selection back instead of leaving a stale/partial DB selected.
    const previousSelection = this.selectedDatabaseSubject.value;

    if (localDb) {
      this.selectedDatabaseSubject.next(localDb);
    }

    return this.http.get<Database>(`${this.apiUrl}/database/${id}`).pipe(
      tap((db) => {
        this.selectedDatabaseSubject.next(db);
        // FE-035: never mutate the array held by the BehaviorSubject — map to a new one.
        const updatedList = this.databasesSubject.value.map((d) => (d.id === db.id ? db : d));
        if (!updatedList.some((d) => d.id === db.id)) {
          updatedList.push(db);
        }
        this.databasesSubject.next(updatedList);
      }),
      catchError((err) => {
        // FE-036: roll back the optimistic selection.
        this.selectedDatabaseSubject.next(previousSelection);
        this.handleError(err);
        return of(null);
      }),
    );
  }

  /**
   * Creates a new database and navigates to its ID-based route.
   */
  public createDatabase(dbData: Partial<Database>): Observable<Database> {
    return this.http.post<Database>(`${this.apiUrl}/database`, dbData).pipe(
      // FE-037: the list refresh is composed into the chain instead of a
      // fire-and-forget nested subscription; a refresh hiccup must not fail the
      // creation for the caller.
      switchMap((newDb) =>
        this.loadDatabases().pipe(
          catchError(() => of([] as Database[])),
          map(() => newDb),
        ),
      ),
      tap((newDb) => {
        this.notificationService.showSuccess(`Database '${newDb.name}' created successfully.`);
        // Navigate using the newly generated ULID instead of the name
        this.router.navigate(['/dashboard/db', newDb.id]);
      }),
      catchError((err) => this.handleError(err)),
    );
  }

  /**
   * Updates an existing database using its ULID.
   */
  public updateDatabase(id: string, updates: DatabaseUpdatePayload): Observable<Database> {
    return this.http.put<Database>(`${this.apiUrl}/database/${id}`, updates).pipe(
      tap((updatedDb) => {
        // Update the currently selected DB if it matches the ID
        if (this.selectedDatabaseSubject.value?.id === id) {
          this.selectedDatabaseSubject.next(updatedDb);
        }

        // FE-035: build a new array instead of mutating the BehaviorSubject's value.
        const currentDbs = this.databasesSubject.value;
        if (currentDbs.some((db) => db.id === id)) {
          this.databasesSubject.next(currentDbs.map((db) => (db.id === id ? updatedDb : db)));
        }
        this.notificationService.showSuccess(
          `Database '${updatedDb.name}' settings updated successfully.`,
        );
      }),
      catchError((err) => this.handleError(err)),
    );
  }

  /**
   * Deletes a database by its ULID.
   */
  public deleteDatabase(id: string): Observable<{ message: string }> {
    return this.http.delete<{ message: string }>(`${this.apiUrl}/database/${id}`).pipe(
      // FE-037: composed list refresh (see createDatabase).
      switchMap((res) =>
        this.loadDatabases().pipe(
          catchError(() => of([] as Database[])),
          map(() => res),
        ),
      ),
      tap((res) => {
        // Utilizing the backend's explicit message string for the notification
        this.notificationService.showSuccess(res.message);

        if (this.selectedDatabaseSubject.value?.id === id) {
          this.selectedDatabaseSubject.next(null);
        }
        this.router.navigate(['/dashboard']);
      }),
      catchError((err) => this.handleError(err)),
    );
  }

  /**
   * Triggers the housekeeping background worker using the ULID.
   */
  public triggerHousekeeping(id: string): Observable<HousekeepingReport> {
    return this.http
      .post<HousekeepingReport>(`${this.apiUrl}/database/${id}/housekeeping`, null)
      .pipe(
        // FE-037: composed re-selection of the affected database (was a nested
        // `selectDatabase(id).subscribe(...)` inside `tap`).
        switchMap((report) =>
          this.selectedDatabaseSubject.value?.id === id
            ? this.selectDatabase(id).pipe(
                catchError(() => of(null)),
                map(() => report),
              )
            : of(report),
        ),
        tap((report) => {
          this.notificationService.showSuccess(report.message || `Housekeeping complete.`);
        }),
        catchError((err) => this.handleError(err)),
      );
  }

  /**
   * Adds a new custom field to a database.
   */
  public addCustomField(dbId: string, field: CustomField): Observable<CustomField> {
    return this.http.post<CustomField>(`${this.apiUrl}/database/${dbId}/field`, field).pipe(
      // FE-037: composed refresh of the database (was a nested subscription).
      switchMap((newField) =>
        this.selectDatabase(dbId).pipe(
          catchError(() => of(null)),
          map(() => newField),
        ),
      ),
      tap((newField) => {
        this.notificationService.showSuccess(`Custom field '${newField.name}' added successfully.`);
      }),
      catchError((err) => this.handleError(err)),
    );
  }

  /**
   * Updates an existing custom field.
   */
  public updateCustomField(
    dbId: string,
    fieldId: number,
    name?: string,
    isIndexed?: boolean,
  ): Observable<CustomField> {
    // FE-042: typed payload instead of `any`.
    const payload: { name?: string; is_indexed?: boolean } = {};
    if (name !== undefined) payload.name = name;
    if (isIndexed !== undefined) payload.is_indexed = isIndexed;

    return this.http
      .patch<CustomField>(`${this.apiUrl}/database/${dbId}/field/${fieldId}`, payload)
      .pipe(
        // FE-037: composed refresh of the database (was a nested subscription).
        switchMap((updatedField) =>
          this.selectDatabase(dbId).pipe(
            catchError(() => of(null)),
            map(() => updatedField),
          ),
        ),
        tap((updatedField) => {
          this.notificationService.showSuccess(
            `Custom field '${updatedField.name}' updated successfully.`,
          );
        }),
        catchError((err) => this.handleError(err)),
      );
  }

  /**
   * Deletes a custom field.
   */
  public deleteCustomField(dbId: string, fieldId: number): Observable<{ message: string }> {
    return this.http
      .delete<{ message: string }>(`${this.apiUrl}/database/${dbId}/field/${fieldId}`)
      .pipe(
        // FE-037: composed refresh of the database (was a nested subscription).
        switchMap((res) =>
          this.selectDatabase(dbId).pipe(
            catchError(() => of(null)),
            map(() => res),
          ),
        ),
        tap((res) => {
          this.notificationService.showSuccess(res.message || `Custom field deleted successfully.`);
        }),
        catchError((err) => this.handleError(err)),
      );
  }
}
