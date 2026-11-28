// frontend/src/app/services/api-key.service.ts

import { Injectable } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable, of } from 'rxjs';
import { catchError, map, switchMap, take, tap } from 'rxjs/operators';
import { ApiKey, ApiKeyPayload, ConfirmationModalData } from '../models';
import { ModalService } from './modal.service';
import { NotificationService } from './notification.service';
import { catchApiError } from '../../shared/utils/http-error';

/**
 * Manages API key CRUD endpoints and shared modal/confirmation flows.
 */
@Injectable({
  providedIn: 'root',
})
export class ApiKeyService {
  private readonly apiUrl = '/api';

  constructor(
    private http: HttpClient,
    private modalService: ModalService,
    private notificationService: NotificationService,
  ) {}

  getGlobalKeys(): Observable<ApiKey[]> {
    return this.http.get<ApiKey[]>(`${this.apiUrl}/users/keys`);
  }

  getUserKeys(userId: string): Observable<ApiKey[]> {
    return this.http.get<ApiKey[]>(`${this.apiUrl}/user/${userId}/keys`);
  }

  createUserKey(userId: string, data: ApiKeyPayload): Observable<ApiKey> {
    return this.http
      .post<ApiKey>(`${this.apiUrl}/user/${userId}/keys`, data)
      .pipe(catchApiError(this.notificationService, { notFoundResource: 'User' }));
  }

  updateUserKey(userId: string, keyId: string, data: ApiKeyPayload): Observable<ApiKey> {
    return this.http
      .patch<ApiKey>(`${this.apiUrl}/user/${userId}/keys/${keyId}`, data)
      .pipe(catchApiError(this.notificationService, { notFoundResource: 'User' }));
  }

  deleteUserKey(userId: string, keyId: string): Observable<unknown> {
    return this.http
      .delete(`${this.apiUrl}/user/${userId}/keys/${keyId}`)
      .pipe(catchApiError(this.notificationService, { notFoundResource: 'User' }));
  }

  openCreateKeyModal(userId: string): Observable<boolean> {
    return this.modalService.open('apiKeyModal', { userId }).pipe(
      take(1),
      map((created) => created === true),
    );
  }

  openEditKeyModal(userId: string, apiKey: ApiKey): Observable<boolean> {
    return this.modalService.open('apiKeyModal', { userId, apiKey }).pipe(
      take(1),
      map((updated) => updated === true),
    );
  }

  confirmRevokeKey(
    userId: string,
    key: ApiKey,
    modalData?: ConfirmationModalData,
  ): Observable<boolean> {
    const keyId = key.id;
    return this.modalService
      .confirm(
        modalData ?? {
          title: 'Revoke API Key',
          message: `Are you sure you want to revoke/delete the API key "${key.name}"? Applications using this key will immediately lose access.`,
        },
      )
      .pipe(
        switchMap((confirmed) =>
          !confirmed
            ? of(false)
            : this.deleteUserKey(userId, keyId).pipe(
                tap(() => this.notificationService.showSuccess('API key revoked successfully.')),
                map(() => true),
                catchError(() => of(false)),
              ),
        ),
      );
  }
}
