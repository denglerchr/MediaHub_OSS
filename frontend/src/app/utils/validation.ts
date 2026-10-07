// frontend/src/app/utils/validation.ts

import { AbstractControl, ValidationErrors } from '@angular/forms';

export const CUSTOM_FIELD_NAME_PATTERN = /^[a-zA-Z_][a-zA-Z0-9_]*$/;

/**
 * FE-049: custom-field names colliding with the standard entry/media fields break
 * the UI (duplicate trackBy keys, wrong filter column) and are now rejected by the
 * backend in AddField (internal/httpserver/databasehandler — see
 * AI/Concept/03_01_Database_Dynamic.md). Kept in sync with the Go-side
 * `reservedCustomFieldNames` list. `created_at`, `updated_at` and
 * `preview_filesize` are the standard *search* fields
 * (internal/repository/sqlite/entries_utils.go `standardFields`) — a custom field
 * with one of those names would silently resolve to the standard column in
 * search/filter instead of its own column.
 */
export const RESERVED_CUSTOM_FIELD_NAMES: ReadonlySet<string> = new Set([
  'id',
  'status',
  'timestamp',
  'filename',
  'filesize',
  'mime_type',
  'width',
  'height',
  'duration',
  'channels',
  'created_at',
  'updated_at',
  'preview_filesize',
]);

/**
 * Validates whether a custom field name starts with a letter or underscore
 * and contains only alphanumeric characters or underscores.
 */
export function isValidCustomFieldName(name: string): boolean {
  return CUSTOM_FIELD_NAME_PATTERN.test(name);
}

/**
 * FE-049: true when the name is one of the reserved standard/media field names.
 */
export function isReservedCustomFieldName(name: string): boolean {
  return RESERVED_CUSTOM_FIELD_NAMES.has(name.trim().toLowerCase());
}

/**
 * FE-049/FE-053: reactive-form validator companion of isReservedCustomFieldName —
 * surfaces `{ reserved: true }` so templates can render a specific error instead of
 * silently disabling the submit button.
 */
export function reservedCustomFieldNameValidator(
  control: AbstractControl,
): ValidationErrors | null {
  const value = control.value;
  return typeof value === 'string' && isReservedCustomFieldName(value) ? { reserved: true } : null;
}
