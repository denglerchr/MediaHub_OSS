import { EntryStatus } from './enums';

export interface MediaFields {
  width?: number;
  height?: number;
  duration?: number;
  channels?: number;
}

export interface Coordinate {
  latitude: number;
  longitude: number;
}

export interface CoordinateBoundingBox {
  min_lat: number;
  max_lat: number;
  min_lng: number;
  max_lng: number;
}

export interface Entry {
  id: number;
  timestamp: number;
  created_at: number;
  updated_at: number;
  status: EntryStatus | string;
  database_id?: string;

  // These might be undefined if the entry is still 'processing' (Async upload)
  mime_type?: string;
  filesize?: number;
  preview_filesize?: number;
  filename?: string;

  // Grouped metadata arrays (Used consistently across all GET endpoints now)
  media_fields?: MediaFields;
  custom_fields?: Record<string, any>;
}

export interface PartialEntryResponse {
  id: number;
  timestamp: number;
  created_at: number;
  updated_at: number;
  database_id: string;
  status: EntryStatus | string;
  custom_fields?: Record<string, any>;
}

export interface SearchFilter {
  operator: string;
  conditions?: SearchFilter[];
  field?: string;
  // FE-042: `unknown` instead of `any` — search values are strings, numbers,
  // booleans, Coordinate or CoordinateBoundingBox; nothing may consume them
  // unchecked anymore.
  value?: unknown;
}

export interface SearchSort {
  field: string;
  direction: 'asc' | 'desc';
}

export interface SearchPagination {
  offset?: number;
  limit: number;
}

export interface SearchRequest {
  filter?: SearchFilter;
  sort?: SearchSort;
  pagination: SearchPagination;
}

/**
 * FE-017: response of `POST /api/database/{id}/entries/delete`
 * (entryhandler/models.go `BulkDeleteResponse`). `errors` is a single
 * server-composed message string; it is empty when every entry was deleted.
 * A partial failure still answers 200 with `deleted_count` < requested ids.
 */
export interface BulkDeleteResponse {
  database_id: string;
  deleted_count: number;
  space_freed_bytes: number;
  message: string;
  errors: string;
}

/**
 * FE-042: the metadata payload sent alongside the file in `POST .../entry`
 * (multipart `metadata` part). Replaces the old
 * `Omit<Entry, 'id' | 'width' | 'height' | ...>` (whose omitted keys were not
 * even `Entry` props) and the `metadata as any` casts at the call sites.
 */
export interface UploadEntryMetadata {
  timestamp: number;
  filename: string;
  custom_fields: Record<string, unknown>;
}

/**
 * FE-042: the `config` part of `POST /api/database/{id}/entries/import`
 * (entryhandler ImportConfigPayload) — typed instead of `any`.
 */
export interface ImportConfig {
  mode: 'generate_new' | 'skip' | 'overwrite';
  unmapped_fields: 'ignore' | 'fail';
  custom_field_mapping?: Record<string, string>;
}
