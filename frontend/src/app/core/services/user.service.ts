// frontend/src/app/services/user.service.ts

import { Injectable } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { Observable } from 'rxjs';
import { User, UserPayload } from '../models';
import { NotificationService } from './notification.service';
import { catchApiError } from '../../shared/utils/http-error';

/**
 * Manages CRUD operations for user and service accounts.
 */
@Injectable({
  providedIn: 'root',
})
export class UserService {
  private readonly apiUrl = '/api';

  constructor(
    private http: HttpClient,
    private notificationService: NotificationService,
  ) {}

  getUsers(accountType?: string): Observable<User[]> {
    let params = new HttpParams();
    if (accountType) {
      params = params.set('account_type', accountType);
    }
    return this.http.get<User[]>(`${this.apiUrl}/users`, { params });
  }

  getUser(userId: string): Observable<User> {
    return this.http.get<User>(`${this.apiUrl}/user/${userId}`);
  }

  createUser(userData: UserPayload): Observable<User> {
    return this.http
      .post<User>(`${this.apiUrl}/user`, userData)
      .pipe(catchApiError(this.notificationService, { notFoundResource: 'User' }));
  }

  updateUser(userId: string, updates: UserPayload): Observable<User> {
    return this.http
      .patch<User>(`${this.apiUrl}/user/${userId}`, updates)
      .pipe(catchApiError(this.notificationService, { notFoundResource: 'User' }));
  }

  deleteUser(userId: string): Observable<{ message: string }> {
    return this.http
      .delete<{ message: string }>(`${this.apiUrl}/user/${userId}`)
      .pipe(catchApiError(this.notificationService, { notFoundResource: 'User' }));
  }
}
