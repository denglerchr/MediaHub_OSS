// frontend/src/app/interceptors/http-context.tokens.ts

import { HttpContextToken } from '@angular/common/http';

/**
 * FE-007: requests carrying this token must never enter the 401 refresh+retry path.
 *
 * `POST /api/logout` is the only current user: it revokes the refresh token that is
 * stored *at send time*. If the interceptor refreshed (rotated) and retried it, the
 * retried body would carry the pre-rotation token and the backend would delete an
 * already-deleted row while the freshly issued token stays valid server-side.
 */
export const SKIP_AUTH_REFRESH = new HttpContextToken<boolean>(() => false);
