// frontend/src/app/services/database.service.ts

import { DestroyRef, Injectable, Optional, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { HttpClient } from '@angular/common/http';
import { BehaviorSubject, MonoTypeOperatorFunction, Observable, of } from 'rxjs';
import { catchError, map, switchMap, tap } from 'rxjs/operators';
import { Database, Housekeeping, HousekeepingReport, DatabaseConfig, CustomField } from '../models';
import { NotificationService } from './notification.service';
import { AuthService } from './auth.service';
import { Router } from '@angular/router';
import { catchApiError } from '../../shared/utils/http-error';

// FE-042: typed instead of `housekeeping?: any`.
export interface DatabaseUpdatePayload {
  name: string;
  n_max_queued: number;
  config: DatabaseConfig;
  housekeeping: Housekeeping;
}

export interface DatabaseCreatePayload {
  name: string;
  content_type: string;
  n_max_queued?: number;
  config?: DatabaseConfig;
  housekeeping?: Housekeeping;
  custom_fields?: CustomField[];
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
  private selectRequestSeq = 0;
  private listRequestSeq = 0;
  private readonly destroyRef = inject(DestroyRef);

  constructor(
    private http: HttpClient,
    private notificationService: NotificationService,
    private router: Router,
    @Optional() private authService?: AuthService,
  ) {
    this.authService?.sessionCleared$?.pipe(takeUntilDestroyed(this.destroyRef)).subscribe(() => {
      this.listRequestSeq++;
      this.selectRequestSeq++;
      this.databasesSubject.next([]);
      this.selectedDatabaseSubject.next(null);
    });
  }

  // --- DATABASE ENDPOINTS ---

  /**
   * Fetches the list of databases accessible to the current user.
   */
  public loadDatabases(): Observable<Database[]> {
    const seq = ++this.listRequestSeq;
    return this.http.get<Database[]>(`${this.apiUrl}/databases`).pipe(
      tap((databases) => {
        if (seq === this.listRequestSeq) {
          this.databasesSubject.next(databases);
        }
      }),
      catchApiError(this.notificationService, { notFoundResource: 'Database' }),
    );
  }

  /**
   * Selects a database by its ULID and updates the active database state.
   */
  public selectDatabase(id: string): Observable<Database | null> {
    const seq = ++this.selectRequestSeq;
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
        if (seq !== this.selectRequestSeq) {
          return;
        }
        this.selectedDatabaseSubject.next(db);
        // FE-035: never mutate the array held by the BehaviorSubject — map to a new one.
        const updatedList = this.databasesSubject.value.map((d) => (d.id === db.id ? db : d));
        if (!updatedList.some((d) => d.id === db.id)) {
          updatedList.push(db);
        }
        this.databasesSubject.next(updatedList);
      }),
      tap({
        error: () => {
          // FE-036: roll back the optimistic selection only if no newer request took over.
          if (seq === this.selectRequestSeq) {
            this.selectedDatabaseSubject.next(previousSelection);
          }
        },
      }),
      catchApiError(this.notificationService, { notFoundResource: 'Database' }),
    );
  }

  /**
   * Creates a new database and navigates to its ID-based route.
   */
  public createDatabase(dbData: DatabaseCreatePayload): Observable<Database> {
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
      catchApiError(this.notificationService, { notFoundResource: 'Database' }),
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
      catchApiError(this.notificationService, { notFoundResource: 'Database' }),
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
          catchError(() => {
            this.databasesSubject.next(this.databasesSubject.value.filter((d) => d.id !== id));
            return of([] as Database[]);
          }),
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
      catchApiError(this.notificationService, { notFoundResource: 'Database' }),
    );
  }

  /**
   * FE-037: composed refresh of the active database when an operation mutates it.
   */
  private refreshSelectedIfActive<T>(dbId: string): MonoTypeOperatorFunction<T> {
    return switchMap((result) =>
      this.selectedDatabaseSubject.value?.id === dbId
        ? this.selectDatabase(dbId).pipe(
            catchError(() => of(null)),
            map(() => result),
          )
        : of(result),
    );
  }

  /**
   * Triggers the housekeeping background worker using the ULID.
   */
  public triggerHousekeeping(id: string): Observable<HousekeepingReport> {
    return this.http
      .post<HousekeepingReport>(`${this.apiUrl}/database/${id}/housekeeping`, null)
      .pipe(
        this.refreshSelectedIfActive(id),
        tap((report) => {
          this.notificationService.showSuccess(report.message || `Housekeeping complete.`);
        }),
        catchApiError(this.notificationService, { notFoundResource: 'Database' }),
      );
  }

  /**
   * Adds a new custom field to a database.
   */
  public addCustomField(dbId: string, field: CustomField): Observable<CustomField> {
    return this.http.post<CustomField>(`${this.apiUrl}/database/${dbId}/field`, field).pipe(
      this.refreshSelectedIfActive(dbId),
      tap((newField) => {
        this.notificationService.showSuccess(`Custom field '${newField.name}' added successfully.`);
      }),
      catchApiError(this.notificationService, { notFoundResource: 'Database' }),
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
        this.refreshSelectedIfActive(dbId),
        tap((updatedField) => {
          this.notificationService.showSuccess(
            `Custom field '${updatedField.name}' updated successfully.`,
          );
        }),
        catchApiError(this.notificationService, { notFoundResource: 'Database' }),
      );
  }

  /**
   * Deletes a custom field.
   */
  public deleteCustomField(dbId: string, fieldId: number): Observable<{ message: string }> {
    return this.http
      .delete<{ message: string }>(`${this.apiUrl}/database/${dbId}/field/${fieldId}`)
      .pipe(
        this.refreshSelectedIfActive(dbId),
        tap((res) => {
          this.notificationService.showSuccess(res.message || `Custom field deleted successfully.`);
        }),
        catchApiError(this.notificationService, { notFoundResource: 'Database' }),
      );
  }
}
