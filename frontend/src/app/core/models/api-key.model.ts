// frontend/src/app/models/api-key.model.ts

import { User } from './user.model';

export interface ApiKey {
  id: string; // ULID
  name: string;
  key_hint: string;
  token?: string; // Plaintext token secret, returned ONLY ONCE upon creation

  // Token Scopes (Filters)
  scope_view: boolean;
  scope_create: boolean;
  scope_edit: boolean;
  scope_delete: boolean;
  scope_admin: boolean;

  created_at: number; // millisecond timestamp
  expires_at: number | null; // millisecond timestamp
  last_used_at: number | null; // millisecond timestamp

  user?: Partial<User>; // optional embedded user details for global lists
}

/**
 * FE-042: create/update payload for `POST /api/user/{id}/keys` and
 * `PATCH /api/user/{id}/keys/{kid}` — typed instead of `any`.
 */
export interface ApiKeyPayload {
  name?: string;
  expires_at?: number | null;
  scope_view?: boolean;
  scope_create?: boolean;
  scope_edit?: boolean;
  scope_delete?: boolean;
  scope_admin?: boolean;
}
