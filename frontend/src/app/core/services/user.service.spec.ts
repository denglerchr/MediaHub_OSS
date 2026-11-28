// frontend/src/app/services/user.service.spec.ts

import { TestBed } from '@angular/core/testing';
import { HttpErrorResponse } from '@angular/common/http';
import { HttpClientTestingModule, HttpTestingController } from '@angular/common/http/testing';
import { UserService } from './user.service';
import { NotificationService } from './notification.service';
import { User, UserPayload } from '../models';

describe('UserService', () => {
  let service: UserService;
  let httpMock: HttpTestingController;
  let notificationSpy: jasmine.SpyObj<NotificationService>;

  const sampleUser: User = {
    id: 'u1',
    username: 'alice',
    is_admin: false,
    account_type: 'local',
    permissions: [],
  };

  beforeEach(() => {
    notificationSpy = jasmine.createSpyObj<NotificationService>('NotificationService', [
      'showError',
      'showSuccess',
      'showInfo',
      'showGlobalError',
      'clearGlobalError',
    ]);

    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule],
      providers: [UserService, { provide: NotificationService, useValue: notificationSpy }],
    });

    service = TestBed.inject(UserService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('getUsers() requests GET /api/users without account_type param when omitted or empty', () => {
    let result: User[] | undefined;
    service.getUsers().subscribe((users) => (result = users));

    const req = httpMock.expectOne('/api/users');
    expect(req.request.method).toBe('GET');
    expect(req.request.params.has('account_type')).toBeFalse();
    req.flush([sampleUser]);
    expect(result).toEqual([sampleUser]);

    service.getUsers('').subscribe();
    const emptyReq = httpMock.expectOne('/api/users');
    expect(emptyReq.request.params.has('account_type')).toBeFalse();
    emptyReq.flush([]);
  });

  it('getUsers(accountType) sets account_type HttpParams', () => {
    let result: User[] | undefined;
    service.getUsers('service_account').subscribe((users) => (result = users));

    const req = httpMock.expectOne(
      (r) => r.url === '/api/users' && r.params.get('account_type') === 'service_account',
    );
    expect(req.request.method).toBe('GET');
    req.flush([sampleUser]);
    expect(result).toEqual([sampleUser]);
  });

  it('getUser(userId) requests GET /api/user/:userId', () => {
    let result: User | undefined;
    service.getUser('u1').subscribe((user) => (result = user));

    const req = httpMock.expectOne('/api/user/u1');
    expect(req.request.method).toBe('GET');
    req.flush(sampleUser);
    expect(result).toEqual(sampleUser);
  });

  it('createUser() posts payload and routes errors through catchApiError', () => {
    const payload: UserPayload = { username: 'bob', password: 'secret', is_admin: false };
    let created: User | undefined;

    service.createUser(payload).subscribe((u) => (created = u));
    const req = httpMock.expectOne('/api/user');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual(payload);
    req.flush({ ...sampleUser, id: 'u2', username: 'bob' });
    expect(created?.id).toBe('u2');

    let caughtError: HttpErrorResponse | undefined;
    service.createUser(payload).subscribe({
      error: (err: HttpErrorResponse) => (caughtError = err),
    });
    const errReq = httpMock.expectOne('/api/user');
    errReq.flush({ error: 'Username already exists' }, { status: 409, statusText: 'Conflict' });

    expect(notificationSpy.showError).toHaveBeenCalledWith('Username already exists');
    expect(caughtError?.status).toBe(409);
  });

  it('updateUser() patches /api/user/:userId and uses User notFoundResource on 404', () => {
    const updates: UserPayload = { is_admin: true };
    let updated: User | undefined;

    service.updateUser('u1', updates).subscribe((u) => (updated = u));
    const req = httpMock.expectOne('/api/user/u1');
    expect(req.request.method).toBe('PATCH');
    expect(req.request.body).toEqual(updates);
    req.flush({ ...sampleUser, is_admin: true });
    expect(updated?.is_admin).toBeTrue();

    let caughtError: HttpErrorResponse | undefined;
    service.updateUser('missing', updates).subscribe({
      error: (err: HttpErrorResponse) => (caughtError = err),
    });
    const errReq = httpMock.expectOne('/api/user/missing');
    errReq.flush(null, { status: 404, statusText: 'Not Found' });

    expect(notificationSpy.showError).toHaveBeenCalledWith('Not Found: User could not be found.');
    expect(caughtError?.status).toBe(404);
  });

  it('deleteUser() sends DELETE /api/user/:userId and handles 404 via catchApiError', () => {
    let response: { message: string } | undefined;

    service.deleteUser('u1').subscribe((res) => (response = res));
    const req = httpMock.expectOne('/api/user/u1');
    expect(req.request.method).toBe('DELETE');
    req.flush({ message: 'User deleted.' });
    expect(response).toEqual({ message: 'User deleted.' });

    let caughtError: HttpErrorResponse | undefined;
    service.deleteUser('missing').subscribe({
      error: (err: HttpErrorResponse) => (caughtError = err),
    });
    const errReq = httpMock.expectOne('/api/user/missing');
    errReq.flush(null, { status: 404, statusText: 'Not Found' });

    expect(notificationSpy.showError).toHaveBeenCalledWith('Not Found: User could not be found.');
    expect(caughtError?.status).toBe(404);
  });
});
