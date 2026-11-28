// frontend/src/app/utils/http-error.spec.ts

import { HttpErrorResponse } from '@angular/common/http';
import { firstValueFrom, throwError } from 'rxjs';
import { NotificationService } from '../../core/services/notification.service';
import { catchApiError, extractHttpErrorMessage } from './http-error';

describe('http-error utilities', () => {
  let notificationSpy: jasmine.SpyObj<NotificationService>;

  beforeEach(() => {
    notificationSpy = jasmine.createSpyObj<NotificationService>('NotificationService', [
      'showError',
      'showGlobalError',
    ]);
  });

  it('extracts error and message fields from JSON object error payloads', async () => {
    const errWithError = new HttpErrorResponse({
      status: 400,
      statusText: 'Bad Request',
      error: { error: 'Cannot remove the last admin' },
    });
    expect(await firstValueFrom(extractHttpErrorMessage(errWithError))).toBe(
      'Cannot remove the last admin',
    );

    const errWithMessage = new HttpErrorResponse({
      status: 400,
      statusText: 'Bad Request',
      error: { message: 'Validation failed' },
    });
    expect(await firstValueFrom(extractHttpErrorMessage(errWithMessage))).toBe('Validation failed');
  });

  it('extracts plain string error payloads and JSON string payloads', async () => {
    const plainTextErr = new HttpErrorResponse({
      status: 400,
      statusText: 'Bad Request',
      error: 'Plain text failure',
    });
    expect(await firstValueFrom(extractHttpErrorMessage(plainTextErr))).toBe('Plain text failure');

    const jsonStringErr = new HttpErrorResponse({
      status: 400,
      statusText: 'Bad Request',
      error: JSON.stringify({ error: 'Parsed string error' }),
    });
    expect(await firstValueFrom(extractHttpErrorMessage(jsonStringErr))).toBe(
      'Parsed string error',
    );
  });

  it('ignores HTML proxy error pages and falls back to status defaults', async () => {
    const doctypeErr = new HttpErrorResponse({
      status: 502,
      statusText: 'Bad Gateway',
      error: '<!DOCTYPE html><html><body><h1>502 Bad Gateway</h1></body></html>',
    });
    expect(await firstValueFrom(extractHttpErrorMessage(doctypeErr))).toBe(
      'Server Error (502): Bad Gateway',
    );

    const htmlFragmentErr = new HttpErrorResponse({
      status: 503,
      statusText: 'Service Unavailable',
      error: '  <html><body>Maintenance</body></html>',
    });
    expect(await firstValueFrom(extractHttpErrorMessage(htmlFragmentErr))).toBe(
      'Service Unavailable (503): Service Unavailable',
    );
  });

  it('parses JSON Blob error payloads', async () => {
    const blobErr = new HttpErrorResponse({
      status: 400,
      statusText: 'Bad Request',
      error: new Blob([JSON.stringify({ error: 'Export limit exceeded' })], {
        type: 'application/json',
      }),
    });
    expect(await firstValueFrom(extractHttpErrorMessage(blobErr))).toBe('Export limit exceeded');

    const msgBlobErr = new HttpErrorResponse({
      status: 422,
      statusText: 'Unprocessable Entity',
      error: new Blob([JSON.stringify({ message: 'Invalid export selection' })], {
        type: 'application/json',
      }),
    });
    expect(await firstValueFrom(extractHttpErrorMessage(msgBlobErr))).toBe(
      'Invalid export selection',
    );
  });

  it('falls back to status defaults for non-JSON Blob errors', async () => {
    const nonJsonBlobErr = new HttpErrorResponse({
      status: 403,
      statusText: 'Forbidden',
      error: new Blob(['<html>Forbidden</html>'], { type: 'text/html' }),
    });
    expect(await firstValueFrom(extractHttpErrorMessage(nonJsonBlobErr))).toBe(
      'Forbidden: You lack permission for this operation.',
    );
  });

  it('maps standard HTTP status defaults and respects notFoundResource', async () => {
    const status0 = new HttpErrorResponse({ status: 0, statusText: 'Unknown Error' });
    expect(await firstValueFrom(extractHttpErrorMessage(status0))).toBe(
      'Network error: Server is unreachable.',
    );

    const status401 = new HttpErrorResponse({ status: 401, statusText: 'Unauthorized' });
    expect(await firstValueFrom(extractHttpErrorMessage(status401))).toBe(
      'Unauthorized: Please log in again.',
    );

    const status403 = new HttpErrorResponse({ status: 403, statusText: 'Forbidden' });
    expect(await firstValueFrom(extractHttpErrorMessage(status403))).toBe(
      'Forbidden: You lack permission for this operation.',
    );

    const status404Default = new HttpErrorResponse({ status: 404, statusText: 'Not Found' });
    expect(await firstValueFrom(extractHttpErrorMessage(status404Default))).toContain('Resource');

    const status404Db = new HttpErrorResponse({ status: 404, statusText: 'Not Found' });
    expect(
      await firstValueFrom(extractHttpErrorMessage(status404Db, { notFoundResource: 'Database' })),
    ).toContain('Database');

    const status500 = new HttpErrorResponse({ status: 500, statusText: 'Internal Server Error' });
    expect(await firstValueFrom(extractHttpErrorMessage(status500))).toBe(
      'Server Error (500): Internal Server Error',
    );

    const status503 = new HttpErrorResponse({ status: 503, statusText: 'Service Unavailable' });
    expect(await firstValueFrom(extractHttpErrorMessage(status503))).toContain(
      'Service Unavailable (503)',
    );
  });

  it('applies prefix without duplicating an already-prefixed message', async () => {
    const err = new HttpErrorResponse({
      status: 404,
      statusText: 'Not Found',
    });
    expect(
      await firstValueFrom(
        extractHttpErrorMessage(err, {
          prefix: 'Audit Log Error: ',
          notFoundResource: 'Audit logs',
        }),
      ),
    ).toBe('Audit Log Error: Not Found: Audit logs could not be found.');

    const alreadyPrefixed = new HttpErrorResponse({
      status: 400,
      statusText: 'Bad Request',
      error: { error: 'Audit Log Error: Invalid range' },
    });
    expect(
      await firstValueFrom(
        extractHttpErrorMessage(alreadyPrefixed, { prefix: 'Audit Log Error: ' }),
      ),
    ).toBe('Audit Log Error: Invalid range');
  });

  it('handles plain Error and unknown values using fallback', async () => {
    expect(await firstValueFrom(extractHttpErrorMessage(new Error('Something broke')))).toBe(
      'Something broke',
    );
    expect(
      await firstValueFrom(extractHttpErrorMessage(new Error(''), { fallback: 'Custom fallback' })),
    ).toBe('Custom fallback');
    expect(
      await firstValueFrom(extractHttpErrorMessage('unexpected', { fallback: 'Custom fallback' })),
    ).toBe('Custom fallback');
  });

  it('catchApiError notifies showError and rethrows the original error', async () => {
    const original = new HttpErrorResponse({
      status: 409,
      statusText: 'Conflict',
      error: { error: 'Duplicate entry' },
    });

    let caught: unknown;
    try {
      await firstValueFrom(throwError(() => original).pipe(catchApiError(notificationSpy)));
    } catch (err) {
      caught = err;
    }

    expect(caught).toBe(original);
    expect(notificationSpy.showError).toHaveBeenCalledOnceWith('Duplicate entry');
    expect(notificationSpy.showGlobalError).not.toHaveBeenCalled();
  });

  it('catchApiError suppresses notifications when silent is true', async () => {
    const original = new HttpErrorResponse({
      status: 500,
      statusText: 'Server Error',
    });

    let caught: unknown;
    try {
      await firstValueFrom(
        throwError(() => original).pipe(catchApiError(notificationSpy, { silent: true })),
      );
    } catch (err) {
      caught = err;
    }

    expect(caught).toBe(original);
    expect(notificationSpy.showError).not.toHaveBeenCalled();
    expect(notificationSpy.showGlobalError).not.toHaveBeenCalled();
  });

  it('catchApiError routes 401 to showGlobalError when globalOn401 is true', async () => {
    const authErr = new HttpErrorResponse({
      status: 401,
      statusText: 'Unauthorized',
    });

    let caughtAuth: unknown;
    try {
      await firstValueFrom(
        throwError(() => authErr).pipe(catchApiError(notificationSpy, { globalOn401: true })),
      );
    } catch (err) {
      caughtAuth = err;
    }

    expect(caughtAuth).toBe(authErr);
    expect(notificationSpy.showGlobalError).toHaveBeenCalledOnceWith(
      'Unauthorized: Please log in again.',
    );
    expect(notificationSpy.showError).not.toHaveBeenCalled();

    const serverErr = new HttpErrorResponse({
      status: 500,
      statusText: 'Server Error',
    });

    let caughtServer: unknown;
    try {
      await firstValueFrom(
        throwError(() => serverErr).pipe(catchApiError(notificationSpy, { globalOn401: true })),
      );
    } catch (err) {
      caughtServer = err;
    }

    expect(caughtServer).toBe(serverErr);
    expect(notificationSpy.showError).toHaveBeenCalledOnceWith('Server Error (500): Server Error');
  });
});
