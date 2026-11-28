// frontend/src/app/services/api-key.service.spec.ts

import { TestBed } from '@angular/core/testing';
import { HttpErrorResponse } from '@angular/common/http';
import { HttpClientTestingModule, HttpTestingController } from '@angular/common/http/testing';
import { of } from 'rxjs';
import { ApiKeyService } from './api-key.service';
import { ModalService } from './modal.service';
import { NotificationService } from './notification.service';
import { ApiKey, ApiKeyPayload, ConfirmationModalData } from '../models';

describe('ApiKeyService', () => {
  let service: ApiKeyService;
  let httpMock: HttpTestingController;
  let modalSpy: jasmine.SpyObj<ModalService>;
  let notificationSpy: jasmine.SpyObj<NotificationService>;

  const sampleKey: ApiKey = {
    id: 'k1',
    name: 'CI Runner',
    key_hint: 'mh_...abcd',
    scope_view: true,
    scope_create: false,
    scope_edit: false,
    scope_delete: false,
    scope_admin: false,
    created_at: 1000,
    expires_at: null,
    last_used_at: null,
  };

  beforeEach(() => {
    modalSpy = jasmine.createSpyObj<ModalService>('ModalService', ['open', 'confirm']);
    notificationSpy = jasmine.createSpyObj<NotificationService>('NotificationService', [
      'showError',
      'showSuccess',
      'showInfo',
      'showGlobalError',
      'clearGlobalError',
    ]);

    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule],
      providers: [
        ApiKeyService,
        { provide: ModalService, useValue: modalSpy },
        { provide: NotificationService, useValue: notificationSpy },
      ],
    });

    service = TestBed.inject(ApiKeyService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('getGlobalKeys() requests GET /api/users/keys', () => {
    let result: ApiKey[] | undefined;
    service.getGlobalKeys().subscribe((keys) => (result = keys));

    const req = httpMock.expectOne('/api/users/keys');
    expect(req.request.method).toBe('GET');
    req.flush([sampleKey]);
    expect(result).toEqual([sampleKey]);
  });

  it('getUserKeys() requests GET /api/user/:userId/keys', () => {
    let result: ApiKey[] | undefined;
    service.getUserKeys('u1').subscribe((keys) => (result = keys));

    const req = httpMock.expectOne('/api/user/u1/keys');
    expect(req.request.method).toBe('GET');
    req.flush([sampleKey]);
    expect(result).toEqual([sampleKey]);
  });

  it('createUserKey() posts payload and routes errors through catchApiError', () => {
    const payload: ApiKeyPayload = { name: 'CI Runner', scope_view: true };
    let created: ApiKey | undefined;

    service.createUserKey('u1', payload).subscribe((k) => (created = k));
    const req = httpMock.expectOne('/api/user/u1/keys');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual(payload);
    req.flush({ ...sampleKey, token: 'secret-token' });
    expect(created?.token).toBe('secret-token');

    let caughtError: HttpErrorResponse | undefined;
    service.createUserKey('missing', payload).subscribe({
      error: (err: HttpErrorResponse) => (caughtError = err),
    });
    const errReq = httpMock.expectOne('/api/user/missing/keys');
    errReq.flush(null, { status: 404, statusText: 'Not Found' });

    expect(notificationSpy.showError).toHaveBeenCalledWith('Not Found: User could not be found.');
    expect(caughtError?.status).toBe(404);
  });

  it('updateUserKey() patches /api/user/:userId/keys/:keyId and handles errors via catchApiError', () => {
    const payload: ApiKeyPayload = { name: 'Renamed Key' };
    let updated: ApiKey | undefined;

    service.updateUserKey('u1', 'k1', payload).subscribe((k) => (updated = k));
    const req = httpMock.expectOne('/api/user/u1/keys/k1');
    expect(req.request.method).toBe('PATCH');
    expect(req.request.body).toEqual(payload);
    req.flush({ ...sampleKey, name: 'Renamed Key' });
    expect(updated?.name).toBe('Renamed Key');

    let caughtError: HttpErrorResponse | undefined;
    service.updateUserKey('u1', 'k1', payload).subscribe({
      error: (err: HttpErrorResponse) => (caughtError = err),
    });
    const errReq = httpMock.expectOne('/api/user/u1/keys/k1');
    errReq.flush({ error: 'Invalid key name' }, { status: 400, statusText: 'Bad Request' });

    expect(notificationSpy.showError).toHaveBeenCalledWith('Invalid key name');
    expect(caughtError?.status).toBe(400);
  });

  it('deleteUserKey() deletes /api/user/:userId/keys/:keyId and handles 404 via catchApiError', () => {
    let response: unknown;
    service.deleteUserKey('u1', 'k1').subscribe((res) => (response = res));

    const req = httpMock.expectOne('/api/user/u1/keys/k1');
    expect(req.request.method).toBe('DELETE');
    req.flush({ message: 'Deleted' });
    expect(response).toEqual({ message: 'Deleted' });

    let caughtError: HttpErrorResponse | undefined;
    service.deleteUserKey('missing', 'k1').subscribe({
      error: (err: HttpErrorResponse) => (caughtError = err),
    });
    const errReq = httpMock.expectOne('/api/user/missing/keys/k1');
    errReq.flush(null, { status: 404, statusText: 'Not Found' });

    expect(notificationSpy.showError).toHaveBeenCalledWith('Not Found: User could not be found.');
    expect(caughtError?.status).toBe(404);
  });

  it('openCreateKeyModal() opens apiKeyModal with userId and normalizes boolean result', () => {
    modalSpy.open.and.returnValues(of(true), of(false), of(null));

    let res1: boolean | undefined;
    service.openCreateKeyModal('u1').subscribe((v) => (res1 = v));
    expect(modalSpy.open).toHaveBeenCalledWith('apiKeyModal', { userId: 'u1' });
    expect(res1).toBeTrue();

    let res2: boolean | undefined;
    service.openCreateKeyModal('u1').subscribe((v) => (res2 = v));
    expect(res2).toBeFalse();

    let res3: boolean | undefined;
    service.openCreateKeyModal('u1').subscribe((v) => (res3 = v));
    expect(res3).toBeFalse();
  });

  it('openEditKeyModal() opens apiKeyModal with userId and apiKey and normalizes boolean result', () => {
    modalSpy.open.and.returnValues(of(true), of(null));

    let res1: boolean | undefined;
    service.openEditKeyModal('u1', sampleKey).subscribe((v) => (res1 = v));
    expect(modalSpy.open).toHaveBeenCalledWith('apiKeyModal', { userId: 'u1', apiKey: sampleKey });
    expect(res1).toBeTrue();

    let res2: boolean | undefined;
    service.openEditKeyModal('u1', sampleKey).subscribe((v) => (res2 = v));
    expect(res2).toBeFalse();
  });

  it('confirmRevokeKey() returns false without calling DELETE when confirmation is cancelled', () => {
    modalSpy.confirm.and.returnValue(of(false));

    let result: boolean | undefined;
    service.confirmRevokeKey('u1', sampleKey).subscribe((v) => (result = v));

    expect(modalSpy.confirm).toHaveBeenCalledWith({
      title: 'Revoke API Key',
      message:
        'Are you sure you want to revoke/delete the API key "CI Runner"? Applications using this key will immediately lose access.',
    });
    httpMock.expectNone('/api/user/u1/keys/k1');
    expect(notificationSpy.showSuccess).not.toHaveBeenCalled();
    expect(result).toBeFalse();
  });

  it('confirmRevokeKey() deletes key, shows success toast, and returns true when confirmed', () => {
    const customModal: ConfirmationModalData = {
      title: 'Custom Title',
      message: 'Custom message',
      confirmText: 'Revoke',
    };
    modalSpy.confirm.and.returnValue(of(true));

    let result: boolean | undefined;
    service.confirmRevokeKey('u1', sampleKey, customModal).subscribe((v) => (result = v));

    expect(modalSpy.confirm).toHaveBeenCalledWith(customModal);
    const req = httpMock.expectOne('/api/user/u1/keys/k1');
    expect(req.request.method).toBe('DELETE');
    req.flush({ message: 'Revoked' });

    expect(notificationSpy.showSuccess).toHaveBeenCalledWith('API key revoked successfully.');
    expect(result).toBeTrue();
  });

  it('confirmRevokeKey() catches delete errors and returns false without success toast', () => {
    modalSpy.confirm.and.returnValue(of(true));

    let result: boolean | undefined;
    service.confirmRevokeKey('u1', sampleKey).subscribe((v) => (result = v));

    const req = httpMock.expectOne('/api/user/u1/keys/k1');
    req.flush({ error: 'Delete failed' }, { status: 500, statusText: 'Server Error' });

    expect(notificationSpy.showError).toHaveBeenCalledWith('Delete failed');
    expect(notificationSpy.showSuccess).not.toHaveBeenCalled();
    expect(result).toBeFalse();
  });
});
