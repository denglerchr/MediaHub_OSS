// frontend/src/app/utils/http-error.ts

import { HttpErrorResponse } from '@angular/common/http';
import { Observable, OperatorFunction, from, of, throwError } from 'rxjs';
import { catchError, map, switchMap } from 'rxjs/operators';
import { NotificationService } from '../../core/services/notification.service';

export interface HttpErrorHandlerOptions {
  prefix?: string;
  notFoundResource?: string;
  silent?: boolean;
  globalOn401?: boolean;
  fallback?: string;
}

const DEFAULT_FALLBACK = 'An unknown error occurred.';

function applyPrefix(message: string, prefix?: string): string {
  if (!prefix) {
    return message;
  }
  return message.startsWith(prefix) ? message : `${prefix}${message}`;
}

function extractPayloadMessage(payload: unknown): string | null {
  if (typeof payload === 'string') {
    const trimmed = payload.trim();
    if (!trimmed) {
      return null;
    }
    if (/^\s*<!doctype\s+html\b|^\s*<html\b/i.test(trimmed) || trimmed.startsWith('<')) {
      return null;
    }
    if (trimmed.startsWith('{')) {
      try {
        const parsed: unknown = JSON.parse(trimmed);
        return extractPayloadMessage(parsed);
      } catch {
        return trimmed;
      }
    }
    return trimmed;
  }

  if (payload && typeof payload === 'object') {
    const record = payload as { error?: unknown; message?: unknown };
    if (typeof record.error === 'string' && record.error.trim()) {
      return record.error.trim();
    }
    if (typeof record.message === 'string' && record.message.trim()) {
      return record.message.trim();
    }
  }

  return null;
}

function mapStatusMessage(
  error: HttpErrorResponse,
  notFoundResource: string,
  fallback: string,
): string {
  switch (error.status) {
    case 0:
      return 'Network error: Server is unreachable.';
    case 400:
      return 'Bad Request: Invalid input.';
    case 401:
      return 'Unauthorized: Please log in again.';
    case 403:
      return 'Forbidden: You lack permission for this operation.';
    case 404:
      return `Not Found: ${notFoundResource} could not be found.`;
    case 409:
      return 'Conflict: The operation conflicts with the current state.';
    case 413:
      return 'Payload Too Large: The uploaded file exceeds the maximum allowed size.';
    case 415:
      return 'Unsupported Media Type: The file format is not supported.';
    case 500:
      return `Server Error (500): ${error.statusText || 'Internal Error'}`;
    case 503:
      return `Service Unavailable (503): ${error.statusText || 'The server is temporarily unavailable.'}`;
    default:
      if (error.status >= 500) {
        return `Server Error (${error.status}): ${error.statusText || 'Internal Error'}`;
      }
      if (error.statusText) {
        return `Error ${error.status}: ${error.statusText}`;
      }
      return fallback;
  }
}

/**
 * Extracts a human-readable error message from an HTTP or generic error,
 * including async parsing of JSON error payloads wrapped in Blob responses.
 */
export function extractHttpErrorMessage(
  error: unknown,
  options?: Pick<HttpErrorHandlerOptions, 'prefix' | 'notFoundResource' | 'fallback'>,
): Observable<string> {
  const fallback = options?.fallback || DEFAULT_FALLBACK;
  const notFoundResource = options?.notFoundResource || 'Resource';

  if (error instanceof HttpErrorResponse) {
    if (typeof Blob !== 'undefined' && error.error instanceof Blob) {
      return from(error.error.text()).pipe(
        catchError(() => of('')),
        map((text) => {
          let serverMessage: string | null = null;
          try {
            const parsed: unknown = JSON.parse(text);
            serverMessage = extractPayloadMessage(parsed);
          } catch {
            serverMessage = null;
          }
          const resolved = serverMessage ?? mapStatusMessage(error, notFoundResource, fallback);
          return applyPrefix(resolved, options?.prefix);
        }),
      );
    }

    const serverMessage = extractPayloadMessage(error.error);
    const resolved = serverMessage ?? mapStatusMessage(error, notFoundResource, fallback);
    return of(applyPrefix(resolved, options?.prefix));
  }

  if (error instanceof Error) {
    return of(applyPrefix(error.message || fallback, options?.prefix));
  }

  return of(applyPrefix(fallback, options?.prefix));
}

/**
 * RxJS operator that extracts a user-facing API error message, notifies the
 * user via NotificationService (unless `silent` is true), and re-throws the
 * original error so downstream callers retain status/error context.
 */
export function catchApiError<T>(
  notificationService: NotificationService,
  options?: HttpErrorHandlerOptions,
): OperatorFunction<T, T> {
  return catchError((error: unknown) =>
    extractHttpErrorMessage(error, options).pipe(
      switchMap((msg) => {
        if (!options?.silent) {
          if (options?.globalOn401 && error instanceof HttpErrorResponse && error.status === 401) {
            notificationService.showGlobalError(msg);
          } else {
            notificationService.showError(msg);
          }
        }
        return throwError(() => error);
      }),
    ),
  );
}
